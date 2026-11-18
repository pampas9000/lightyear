package v1

import (
	"encoding/json"
	"strconv"
	"strings"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/task"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type CreateTaskRequest struct {
	Items      []task.TaskItemInput `json:"items"`
	Options    json.RawMessage      `json:"options,omitempty"`
	Target     json.RawMessage      `json:"target,omitempty"`
	Outputs    json.RawMessage      `json:"outputs,omitempty"`
	WorkflowID *uuid.UUID           `json:"workflow_id,omitempty"`
}

type RetryTaskRequest struct {
	JobIDs   []uuid.UUID     `json:"job_ids,omitempty"`
	RetryAll bool            `json:"retry_all,omitempty"`
	Options  json.RawMessage `json:"options,omitempty"`
	Target   json.RawMessage `json:"target,omitempty"`
}

type TaskList struct {
	Tasks []Task `json:"tasks"`
	Total int64  `json:"total"`
	Page  int    `json:"page"`
	Limit int    `json:"limit"`
}

// accepted answers a request that queued work with 202, the task and where to poll.
func (h *Handler) accepted(c fiber.Ctx, t *models.Task, message string) error {
	c.Location("/v1/tasks/" + t.ID.String())
	setRetryAfter(c)
	return response.RespondSuccess(c, fiber.StatusAccepted, response.CodeOK, message, NewTask(t))
}

func (h *Handler) CreateTask(c fiber.Ctx) error {
	var req CreateTaskRequest
	if err := decodeJSON(c.Body(), &req, false); err != nil {
		return badBody(c, err)
	}
	t, replayed, err := h.tasks.Create(c.Context(), task.CreateTaskInput{
		OwnerID:        owner(c),
		WorkflowID:     req.WorkflowID,
		Items:          req.Items,
		Options:        req.Options,
		Target:         req.Target,
		Outputs:        req.Outputs,
		IdempotencyKey: strings.TrimSpace(c.Get(HeaderIdempotencyKey)),
	})
	if err != nil {
		return respondTaskError(c, err, "create task")
	}
	if replayed {
		c.Set(HeaderIdempotentReplayed, "true")
		return h.accepted(c, t, "Task already created for this Idempotency-Key.")
	}
	return h.accepted(c, t, "Task accepted.")
}

func (h *Handler) GetTask(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badID(c, "id")
	}
	t, err := h.tasks.GetTask(c.Context(), owner(c), id)
	if err != nil {
		return respondTaskError(c, err, "fetch task")
	}
	out := NewTask(t)
	now := h.now()
	etag, err := taskETag(&out, now, etagWindow)
	if err != nil {
		return internalError(c, "fetch task", err)
	}
	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderCacheControl, "private, no-cache")
	if !Terminal(t.Status) {
		setRetryAfter(c)
	}
	if inm := c.Get(fiber.HeaderIfNoneMatch); inm != "" && matchesETag(inm, etag) {
		return c.SendStatus(fiber.StatusNotModified)
	}
	if err := AttachDownloads(c.Context(), &out, t, h.sign, DownloadURLTTL, now); err != nil {
		return internalError(c, "sign download URLs", err)
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Task fetched.", out)
}

func Terminal(s models.TaskStatus) bool {
	switch s {
	case models.TaskCompleted, models.TaskFailed, models.TaskPartiallyFailed, models.TaskCancelled:
		return true
	}
	return false
}

var taskStatuses = []models.TaskStatus{
	models.TaskPending, models.TaskProcessing, models.TaskCompleted,
	models.TaskFailed, models.TaskPartiallyFailed, models.TaskCancelled,
}

func (h *Handler) ListTasks(c fiber.Ctx) error {
	page, err := strconv.Atoi(c.Query("page", "1"))
	if err != nil || page < 1 {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid page.", response.ValidationDetail{Field: "page", Reason: "must be a positive integer"})
	}
	limit, err := strconv.Atoi(c.Query("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid limit.", response.ValidationDetail{Field: "limit", Reason: "must be 1-100"})
	}
	status := strings.ToUpper(c.Query("status"))
	if status != "" && !containsStatus(models.TaskStatus(status)) {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid status.", response.ValidationDetail{Field: "status", Reason: "unknown status"})
	}
	tasks, total, err := h.tasks.ListTasks(c.Context(), owner(c), task.ListTasksParams{Page: page, Count: limit, Status: status})
	if err != nil {
		return internalError(c, "list tasks", err)
	}
	out := TaskList{Tasks: make([]Task, len(tasks)), Total: total, Page: page, Limit: limit}
	for i := range tasks {
		out.Tasks[i] = NewTask(&tasks[i])
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Tasks fetched.", out)
}

func containsStatus(s models.TaskStatus) bool {
	for _, v := range taskStatuses {
		if v == s {
			return true
		}
	}
	return false
}

func (h *Handler) CancelTask(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badID(c, "id")
	}
	t, err := h.tasks.CancelTask(c.Context(), owner(c), id)
	if err != nil {
		return respondTaskError(c, err, "cancel task")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Task cancelled.", NewTask(t))
}

func (h *Handler) RetryTask(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badID(c, "id")
	}
	var req RetryTaskRequest
	if err := decodeJSON(c.Body(), &req, true); err != nil {
		return badBody(c, err)
	}
	t, err := h.tasks.RetryTask(c.Context(), task.RetryTaskInput{
		OwnerID: owner(c), TaskID: id, JobIDs: req.JobIDs, RetryAll: req.RetryAll,
		Options: req.Options, Target: req.Target,
	})
	if err != nil {
		return respondTaskError(c, err, "retry task")
	}
	return h.accepted(c, t, "Task retry accepted.")
}

// DeleteTask deletes a finished task with its jobs and the outputs no other
// task reuses. A task with unfinished jobs is a 409: cancel it first.
func (h *Handler) DeleteTask(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badID(c, "id")
	}
	res, err := h.files.DeleteTask(c.Context(), owner(c), id)
	if err != nil {
		return respondTaskError(c, err, "delete task")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Task deleted.", res)
}
