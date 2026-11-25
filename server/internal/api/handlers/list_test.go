package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/testutil"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// The console's lists answer {items, page, limit, total}, newest first, and
// take page and limit.
func TestConsoleListsArePaged(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner := uuid.New()
	now := time.Now()
	var taskIDs, workflowIDs []string
	for i := range 3 {
		tk := models.Task{OwnerID: owner, Status: models.TaskCompleted}
		wf := models.Workflow{OwnerID: owner, Name: "wf"}
		if err := db.Create(&tk).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&wf).Error; err != nil {
			t.Fatal(err)
		}
		at := now.Add(time.Duration(i) * time.Second)
		db.Model(&tk).Update("created_at", at)
		db.Model(&wf).Update("created_at", at)
		taskIDs = append(taskIDs, tk.ID.String())
		workflowIDs = append(workflowIDs, wf.ID.String())
	}
	// Another user's rows are not counted.
	db.Create(&models.Task{OwnerID: uuid.New(), Status: models.TaskCompleted})

	app := fiber.New()
	app.Use(asTestUser)
	api := app.Group("/api")
	NewTaskHandler(task.NewService(db, nil), nil).Install(api)
	NewWorkflowHandler(db).Install(api)

	type list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Page  int   `json:"page"`
		Limit int   `json:"limit"`
		Total int64 `json:"total"`
	}
	get := func(path string) (int, string, list) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Test-User", owner.String())
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct {
			Code string `json:"code"`
			Data list   `json:"data"`
		}
		_ = json.NewDecoder(res.Body).Decode(&body)
		return res.StatusCode, body.Code, body.Data
	}

	for _, tc := range []struct {
		path string
		ids  []string
	}{
		{"/api/tasks", taskIDs},
		{"/api/workflows", workflowIDs},
	} {
		status, _, first := get(tc.path + "?page=1&limit=2")
		_, _, second := get(tc.path + "?page=2&limit=2")
		if status != http.StatusOK || first.Page != 1 || first.Limit != 2 || first.Total != 3 || second.Page != 2 ||
			len(first.Items) != 2 || len(second.Items) != 1 ||
			first.Items[0].ID != tc.ids[2] || first.Items[1].ID != tc.ids[1] || second.Items[0].ID != tc.ids[0] {
			t.Errorf("%s: %d %+v %+v", tc.path, status, first, second)
		}
		// Defaults: page 1, limit 20.
		if _, _, all := get(tc.path); all.Page != 1 || all.Limit != 20 || len(all.Items) != 3 {
			t.Errorf("%s defaults: %+v", tc.path, all)
		}
		for _, q := range []string{"?page=0", "?limit=0", "?limit=101", "?page=x"} {
			if status, code, _ := get(tc.path + q); status != http.StatusBadRequest || code != "error.param.invalid" {
				t.Errorf("%s%s: %d %s, want 400 error.param.invalid", tc.path, q, status, code)
			}
		}
	}
}
