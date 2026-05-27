package handlers

import (
	"errors"
	"strconv"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/transcode"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type TaskHandler struct {
	service *task.Service
}

func NewTaskHandler(db *gorm.DB, kv *redis.Client) *TaskHandler {
	return &TaskHandler{
		service: task.NewService(db, kv),
	}
}

func (h *TaskHandler) Install(router fiber.Router) {
	router.Post("/tasks", h.CreateTask)
	router.Get("/tasks", h.ListTasks)
	router.Get("/tasks/stats", h.GetStats)
	router.Get("/tasks/:id", h.GetTask)
}

func (h *TaskHandler) ListTasks(c fiber.Ctx) error {
	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found.")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID.")
	}

	pageStr := c.Query("page", "1")
	countStr := c.Query("count", "20")
	status := c.Query("status", "")

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid limit.", map[string]any{"reason": err.Error()})
	}
	page, err := strconv.Atoi(pageStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid offset.", map[string]any{"reason": err.Error()})
	}

	results, total, err := h.service.ListTasks(c.Context(), ownerID, task.ListTasksParams{
		Page:   page,
		Count:  count,
		Status: status,
	})
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to list tasks.", map[string]any{"reason": err.Error()})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Tasks fetched.", fiber.Map{
		"tasks": results,
		"total": total,
	})
}

func (h *TaskHandler) GetStats(c fiber.Ctx) error {
	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found.")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID.")
	}

	stats, err := h.service.GetStats(c.Context(), ownerID)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to fetch stats.", map[string]any{"reason": err.Error()})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Stats fetched.", stats)
}

type CreateTaskRequest struct {
	WorkflowID     *uuid.UUID           `json:"workflow_id,omitempty"`
	Items          []task.TaskItemInput `json:"items"`
	TargetFormat   string               `json:"target_format,omitempty"`
	Params         *transcode.Params    `json:"params,omitempty"`
	IdempotencyKey string               `json:"idempotency_key,omitempty"`
}

func (h *TaskHandler) CreateTask(c fiber.Ctx) error {
	var req CreateTaskRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body.", map[string]any{"reason": err.Error()})
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID.")
	}

	var idempotencyPtr *string
	if req.IdempotencyKey != "" {
		idempotencyPtr = &req.IdempotencyKey
	}

	var transcodeParams transcode.Params
	if req.Params != nil {
		transcodeParams = *req.Params
	}

	created, err := h.service.CreateTask(c.Context(), task.CreateTaskInput{
		OwnerID:        ownerID,
		WorkflowID:     req.WorkflowID,
		Items:          req.Items,
		TargetFormat:   req.TargetFormat,
		Params:         transcodeParams,
		IdempotencyKey: idempotencyPtr,
	})
	if err == nil {
		return response.RespondSuccess(c, fiber.StatusCreated, response.CodeOK, "Task created successfully.", created)
	}

	switch {
	case errors.Is(err, task.ErrNoItemsProvided):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "items are required.", validationDetail{Field: "items", Reason: "empty"})
	case errors.Is(err, task.ErrWorkflowNotFound):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "workflow not found.", validationDetail{Field: "workflowId", Reason: "not found"})
	case errors.Is(err, task.ErrInvalidFileID):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "file_id is required and must not be nil.", validationDetail{Field: "items", Reason: "file_id is required"})
	case errors.Is(err, task.ErrFileNotFound):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "input file not found or not uploaded.", validationDetail{Field: "items", Reason: "input file not found or not uploaded"})
	case errors.Is(err, task.ErrIdempotencyConflict):
		return response.RespondError(c, fiber.StatusConflict, response.CodeParamInvalid, "Idempotency key conflict: request payload mismatch.")
	}

	return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create task.", map[string]any{"reason": err.Error()})
}

func (h *TaskHandler) GetTask(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid task id format.", validationDetail{Field: "id", Reason: "must be a valid UUID"})
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found.")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID.")
	}

	result, err := h.service.GetTask(c.Context(), ownerID, parsedID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || err.Error() == "record not found" {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeOK, "Task not found.")
		}
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to fetch task.", map[string]any{"reason": err.Error()})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Task fetched.", result)
}
