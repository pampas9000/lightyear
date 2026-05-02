package handlers

import (
	"errors"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/workflow"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type WorkflowHandler struct {
	service *workflow.Service
}

func NewWorkflowHandler(db *gorm.DB) *WorkflowHandler {
	return &WorkflowHandler{
		service: workflow.NewService(db),
	}
}

func (h *WorkflowHandler) Install(router fiber.Router) {
	router.Post("/workflows", h.CreateWorkflow)
	router.Get("/workflows/:id", h.GetWorkflow)
	router.Get("/workflows", h.ListWorkflows)
}

type CreateWorkflowRequest struct {
	Name         string         `json:"name"`
	TargetFormat string         `json:"targetFormat"`
	Params       map[string]any `json:"params,omitempty"`
}

func (h *WorkflowHandler) CreateWorkflow(c fiber.Ctx) error {
	var req CreateWorkflowRequest
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

	wf, err := h.service.CreateWorkflow(c.Context(), workflow.CreateWorkflowInput{
		OwnerID:      ownerID,
		Name:         req.Name,
		TargetFormat: req.TargetFormat,
		Params:       req.Params,
	})
	if err == nil {
		return response.RespondSuccess(c, fiber.StatusCreated, response.CodeOK, "Workflow created.", wf)
	}

	switch {
	case errors.Is(err, workflow.ErrNameRequired):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "name is required.", validationDetail{Field: "name", Reason: "required"})
	case errors.Is(err, workflow.ErrTargetFormatRequired):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "targetFormat is required.", validationDetail{Field: "targetFormat", Reason: "required"})
	}

	return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create workflow.", map[string]any{"reason": err.Error()})
}

func (h *WorkflowHandler) GetWorkflow(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid ID.", validationDetail{Field: "id", Reason: "must be a valid UUID"})
	}

	wf, err := h.service.GetWorkflow(c.Context(), parsedID)
	if err != nil {
		// TODO: specific error check for gorm
		if err.Error() == "record not found" {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeOK, "Workflow not found.")
		}
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to fetch workflow.", map[string]any{"reason": err.Error()})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Workflow fetched.", wf)
}

func (h *WorkflowHandler) ListWorkflows(c fiber.Ctx) error {
	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID.")
	}

	wfs, err := h.service.ListWorkflows(c.Context(), ownerID)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to list workflows.", map[string]any{"reason": err.Error()})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Workflows fetched.", wfs)
}
