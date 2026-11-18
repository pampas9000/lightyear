package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"transcoder/server/internal/models"
)

func urlTaskBody(items string) string {
	return `{"items":` + items + `,"options":{"format":"webp"}}`
}

func TestCreateTaskFromURL(t *testing.T) {
	e := setup(t)
	key := e.key()
	const url = "https://images.example.com/a/b/Cat%20Photo.JPG?X-Amz-Signature=abc&X-Amz-Expires=600"
	quoted, _ := json.Marshal(url)
	res, body := e.do(http.MethodPost, "/v1/tasks", key, urlTaskBody(`[{"url":`+string(quoted)+`}]`))
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s %s", res.StatusCode, body.Message, body.Details)
	}
	got := decode[Task](t, body.Data)
	if len(got.Jobs) != 1 || got.Jobs[0].Status != models.JobPending || got.Jobs[0].SourceFile == nil {
		t.Fatalf("unexpected task %+v", got)
	}
	src := got.Jobs[0].SourceFile
	if src.Status != models.FileFetching || src.SourceURL != url || src.Name != "Cat Photo.JPG" || src.Size != 0 {
		t.Fatalf("unexpected source file %+v", src)
	}
	var file models.File
	if err := e.db.First(&file, "id = ?", src.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(file.Path, "uploads/"+e.owner.String()+"/") || !strings.HasSuffix(file.Path, ".jpg") {
		t.Fatalf("path %q", file.Path)
	}
	// A URL source is no upload: completing it is refused.
	if res, _ := e.do(http.MethodPost, "/v1/uploads/"+src.ID.String()+"/complete", key, `{}`); res.StatusCode != http.StatusNotFound {
		t.Fatalf("completing a URL source: %d", res.StatusCode)
	}

	// Items may mix uploads and URLs; the same URL twice is two sources.
	upload := e.uploaded("a.png")
	res, body = e.do(http.MethodPost, "/v1/tasks", key, urlTaskBody(
		`[{"file_id":"`+upload.ID.String()+`"},{"url":"http://example.com/x"},{"url":"http://example.com/x"}]`))
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("mixed items: %d %s", res.StatusCode, body.Details)
	}
	mixed := decode[Task](t, body.Data)
	names := map[string]int{}
	for _, j := range mixed.Jobs {
		names[j.SourceFile.Name]++
	}
	if len(mixed.Jobs) != 3 || names["a.png"] != 1 || names["x"] != 2 || mixed.Jobs[1].SourceFile.ID == mixed.Jobs[2].SourceFile.ID {
		t.Fatalf("mixed task %+v", names)
	}

	// The idempotency fingerprint covers URLs.
	idem := []string{HeaderIdempotencyKey, "url-key"}
	if res, _ := e.do(http.MethodPost, "/v1/tasks", key, urlTaskBody(`[{"url":"https://example.com/1.png"}]`), idem...); res.StatusCode != http.StatusAccepted {
		t.Fatalf("first: %d", res.StatusCode)
	}
	if res, _ := e.do(http.MethodPost, "/v1/tasks", key, urlTaskBody(`[{"url":"https://example.com/1.png"}]`), idem...); res.Header.Get(HeaderIdempotentReplayed) != "true" {
		t.Fatalf("replay: %d", res.StatusCode)
	}
	if res, _ := e.do(http.MethodPost, "/v1/tasks", key, urlTaskBody(`[{"url":"https://example.com/2.png"}]`), idem...); res.StatusCode != http.StatusConflict {
		t.Fatalf("another URL under the same key: %d", res.StatusCode)
	}
}

func TestTaskItemsNeedExactlyOneSource(t *testing.T) {
	e := setup(t)
	key := e.key()
	file := e.uploaded("a.png")
	long, _ := json.Marshal("https://example.com/" + strings.Repeat("a", 2048))
	for items, code := range map[string]string{
		`[{}]`: "error.param.required",
		`[{"file_id":"00000000-0000-0000-0000-000000000000"}]`:                       "error.param.required",
		`[{"file_id":"` + file.ID.String() + `","url":"https://example.com/a.png"}]`: "error.param.required",
		`[{"url":""}]`:                             "error.param.required",
		`[{"url":"ftp://example.com/a.png"}]`:      "error.param.invalid",
		`[{"url":"file:///etc/passwd"}]`:           "error.param.invalid",
		`[{"url":"example.com/a.png"}]`:            "error.param.invalid",
		`[{"url":"https://"}]`:                     "error.param.invalid",
		`[{"url":"https://user:pw@example.com/"}]`: "error.param.invalid",
		`[{"url":" https://example.com/a.png"}]`:   "error.param.invalid",
		`[{"url":"https://exa mple.com/"}]`:        "error.param.invalid",
		`[{"url":` + string(long) + `}]`:           "error.param.invalid",
	} {
		res, body := e.do(http.MethodPost, "/v1/tasks", key, urlTaskBody(items))
		if res.StatusCode != http.StatusBadRequest || body.Code != code {
			t.Errorf("%s: %d %s %s", items, res.StatusCode, body.Code, body.Message)
		}
	}
	var count int64
	e.db.Model(&models.File{}).Where("status = ?", models.FileFetching).Count(&count)
	if count != 0 {
		t.Fatalf("rejected requests created %d URL files", count)
	}
}
