package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/testutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// fakeS3 answers the calls the upload service makes: create, list, complete
// and abort a multipart upload, and HEAD an object.
func fakeS3(t *testing.T) *storage.S3Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.Method == http.MethodHead:
			w.Header().Set("Content-Length", "1024")
			w.Header().Set("Content-Type", "image/png")
		case r.Method == http.MethodPost && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == http.MethodGet && q.Has("uploadId"):
			fmt.Fprint(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>"e1"</ETag><Size>5242880</Size></Part></ListPartsResult>`)
		case r.Method == http.MethodPost && q.Has("uploadId"):
			fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"all"</ETag></CompleteMultipartUploadResult>`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected store call %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return storage.NewS3Service(s3sdk.New(s3sdk.Options{
		Region:           "auto",
		Credentials:      credentials.NewStaticCredentialsProvider("id", "secret", ""),
		BaseEndpoint:     aws.String(srv.URL),
		UsePathStyle:     true,
		RetryMaxAttempts: 1,
	}))
}

type envelope struct {
	Success bool                       `json:"success"`
	Code    string                     `json:"code"`
	Message string                     `json:"message"`
	Data    map[string]json.RawMessage `json:"data"`
	Details json.RawMessage            `json:"details"`
}

// The console's upload endpoints answer with the envelope and snake_case
// fields, single PUT, fast upload and multipart alike.
func TestUploadEndpointsUseEnvelope(t *testing.T) {
	db := testutil.OpenDatabase(t)
	s3 := fakeS3(t)
	files := lifecycle.NewService(db, s3, "bucket", lifecycle.Policy{})
	app := fiber.New()
	app.Use(asTestUser)
	NewUploadHandler(upload.NewService(db, s3, "bucket", files)).Install(app.Group("/api"))
	owner := uuid.NewString()

	call := func(method, path, body string) envelope {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Test-User", owner)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil || res.StatusCode != http.StatusOK || !env.Success || env.Code != "ok" {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
		}
		for k := range env.Data {
			if strings.ToLower(k) != k {
				t.Errorf("%s %s: field %q is not snake_case", method, path, k)
			}
		}
		return env
	}
	str := func(raw json.RawMessage) string {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	hash := func(n int) string { return fmt.Sprintf("%064x", n) }

	// Single PUT, then complete.
	put := call(http.MethodPost, "/api/upload/init", `{"filename":"a.png","type":"image/png","size":1024,"hash":"`+hash(1)+`"}`)
	fileID, key := str(put.Data["file_id"]), str(put.Data["key"])
	if fileID == "" || key == "" || str(put.Data["url"]) == "" || put.Data["upload_id"] != nil {
		t.Fatalf("single PUT init: %v", put.Data)
	}
	done := call(http.MethodPost, "/api/upload/complete", `{"key":"`+key+`"}`)
	if str(done.Data["file_id"]) != fileID || str(done.Data["key"]) != key {
		t.Fatalf("complete: %v", done.Data)
	}

	// The same content again is a fast upload of the same file.
	fast := call(http.MethodPost, "/api/upload/init", `{"filename":"b.png","type":"image/png","size":1024,"hash":"`+hash(1)+`"}`)
	if string(fast.Data["fast_upload"]) != "true" || str(fast.Data["file_id"]) != fileID {
		t.Fatalf("fast upload: %v", fast.Data)
	}

	// Multipart: init, list parts, sign a part, complete.
	mp := call(http.MethodPost, "/api/upload/init", `{"filename":"big.png","type":"image/png","size":20971520,"hash":"`+hash(2)+`"}`)
	var partURLs []string
	_ = json.Unmarshal(mp.Data["part_urls"], &partURLs)
	mpKey, uploadID := str(mp.Data["key"]), str(mp.Data["upload_id"])
	if uploadID != "up-1" || len(partURLs) != 4 {
		t.Fatalf("multipart init: %v", mp.Data)
	}
	q := "?key=" + url.QueryEscape(mpKey)
	parts := call(http.MethodGet, "/api/upload/multipart/"+uploadID+"/list-parts"+q, "")
	var listed []UploadPart
	if err := json.Unmarshal(parts.Data["parts"], &listed); err != nil || len(listed) != 1 ||
		listed[0] != (UploadPart{PartNumber: 1, Size: 5242880, ETag: `"e1"`}) {
		t.Fatalf("list parts: %s", parts.Data["parts"])
	}
	signed := call(http.MethodGet, "/api/upload/multipart/"+uploadID+"/sign-part/2"+q, "")
	if string(signed.Data["part_number"]) != "2" || str(signed.Data["url"]) == "" || str(signed.Data["expires_at"]) == "" {
		t.Fatalf("sign part: %v", signed.Data)
	}
	call(http.MethodPost, "/api/upload/complete", `{"key":"`+mpKey+`","parts":[{"part_number":1,"etag":"\"e1\""}]}`)

	// Abort answers data: null.
	ab := call(http.MethodPost, "/api/upload/init", `{"filename":"c.png","type":"image/png","size":20971520,"hash":"`+hash(3)+`"}`)
	res := call(http.MethodPost, "/api/upload/multipart/"+str(ab.Data["upload_id"])+"/abort", `{"key":"`+str(ab.Data["key"])+`"}`)
	if res.Data != nil || string(res.Details) != "null" {
		t.Fatalf("abort: data %v details %s", res.Data, res.Details)
	}
}
