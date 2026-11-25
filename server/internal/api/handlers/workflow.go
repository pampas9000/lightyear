package handlers

import (
	"errors"

	"encoding/json"

	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/api/response"
	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
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
	router.Put("/workflows/:id", h.UpdateWorkflow)
	router.Delete("/workflows/:id", h.DeleteWorkflow)
}

type CreateWorkflowRequest struct {
	Name    string          `json:"name"`
	Outputs json.RawMessage `json:"outputs,omitempty"`
	Options json.RawMessage `json:"options,omitempty"`
	Target  json.RawMessage `json:"target,omitempty"`
}

func (h *WorkflowHandler) CreateWorkflow(c fiber.Ctx) error {
	var req CreateWorkflowRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body.", map[string]any{"reason": err.Error()})
	}

	ownerID, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}

	wf, err := h.service.CreateWorkflow(c.Context(), workflow.CreateWorkflowInput{
		OwnerID: ownerID,
		Name:    req.Name,
		Outputs: req.Outputs,
		Options: req.Options,
		Target:  req.Target,
	})
	if err == nil {
		return response.RespondSuccess(c, fiber.StatusCreated, response.CodeOK, "Workflow created.", wf)
	}

	switch {
	case errors.Is(err, workflow.ErrNameRequired):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "name is required.", response.ValidationDetail{Field: "name", Reason: "required"})
	case errors.Is(err, workflow.ErrOutputsRequired),
		errors.Is(err, imageoptions.ErrInvalid),
		errors.Is(err, compression.ErrInvalidTarget),
		errors.Is(err, compression.ErrInvalidOutputs),
		errors.Is(err, compression.ErrTooManyOutputs):
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error())
	}

	return response.RespondInternal(c, "Failed to create workflow.", err)
}

func (h *WorkflowHandler) GetWorkflow(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid ID.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}

	ownerID, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}

	wf, err := h.service.GetWorkflow(c.Context(), ownerID, parsedID)
	if err != nil {
		if errors.Is(err, workflow.ErrWorkflowNotFound) {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeWorkflowNotFound, "Workflow not found.")
		}
		return response.RespondInternal(c, "Failed to fetch workflow.", err)
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Workflow fetched.", wf)
}

type UpdateWorkflowRequest struct {
	Name    string          `json:"name"`
	Outputs json.RawMessage `json:"outputs,omitempty"`
	Options json.RawMessage `json:"options,omitempty"`
	Target  json.RawMessage `json:"target,omitempty"`
}

func (h *WorkflowHandler) UpdateWorkflow(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid ID.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}

	ownerID, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}

	var req UpdateWorkflowRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body.", map[string]any{"reason": err.Error()})
	}

	wf, err := h.service.UpdateWorkflow(c.Context(), ownerID, parsedID, workflow.CreateWorkflowInput{
		OwnerID: ownerID,
		Name:    req.Name,
		Outputs: req.Outputs,
		Options: req.Options,
		Target:  req.Target,
	})
	if err == nil {
		return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Workflow updated.", wf)
	}

	switch {
	case errors.Is(err, workflow.ErrNameRequired):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "name is required.", response.ValidationDetail{Field: "name", Reason: "required"})
	case errors.Is(err, workflow.ErrOutputsRequired),
		errors.Is(err, imageoptions.ErrInvalid),
		errors.Is(err, compression.ErrInvalidTarget),
		errors.Is(err, compression.ErrInvalidOutputs),
		errors.Is(err, compression.ErrTooManyOutputs):
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error())
	case errors.Is(err, workflow.ErrWorkflowNotFound):
		return response.RespondError(c, fiber.StatusNotFound, response.CodeWorkflowNotFound, "Workflow not found.")
	}

	return response.RespondInternal(c, "Failed to update workflow.", err)
}

func (h *WorkflowHandler) DeleteWorkflow(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid ID.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}

	ownerID, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}

	if err := h.service.DeleteWorkflow(c.Context(), ownerID, parsedID); err != nil {
		if errors.Is(err, workflow.ErrWorkflowNotFound) {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeWorkflowNotFound, "Workflow not found.")
		}
		return response.RespondInternal(c, "Failed to delete workflow.", err)
	}

	return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "Workflow deleted.")
}

func (h *WorkflowHandler) ListWorkflows(c fiber.Ctx) error {
	ownerID, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}

	p, ok, err := response.ParsePage(c)
	if !ok {
		return err
	}
	wfs, total, err := h.service.ListWorkflows(c.Context(), ownerID, p.Limit, p.Offset())
	if err != nil {
		return response.RespondInternal(c, "Failed to list workflows.", err)
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Workflows fetched.", response.NewList(wfs, p, total))
}
