package handlers

import (
	"errors"
	"strconv"
	"time"

	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/upload"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// DraftHandler serves the console's drafts: uploaded files not yet submitted
// in a task.
type DraftHandler struct {
	uploads *upload.Service
	files   *lifecycle.Service
}

func NewDraftHandler(uploads *upload.Service, files *lifecycle.Service) *DraftHandler {
	return &DraftHandler{uploads: uploads, files: files}
}

func (h *DraftHandler) Install(router fiber.Router) {
	router.Get("/drafts/list", h.ListDrafts)
	router.Post("/drafts/:id/delete", h.DeleteDraft)
}

func (h *DraftHandler) ListDrafts(c fiber.Ctx) error {
	owner, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}
	limit, err := strconv.Atoi(c.Query("limit", "20"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)
	drafts, cursor, err := h.uploads.ListDrafts(c.Context(), owner, c.Query("cursor"), limit)
	if err != nil {
		return response.RespondInternal(c, "Failed to list files", err)
	}

	type FileDTO struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		Size       int64     `json:"size"`
		MimeType   string    `json:"mime_type"`
		UploadedAt time.Time `json:"uploaded_at"`
		Status     string    `json:"status"`
	}
	dtos := make([]FileDTO, 0, len(drafts))
	for _, d := range drafts {
		dtos = append(dtos, FileDTO{ID: d.ID.String(), Name: d.Name, Size: d.Size, MimeType: d.MimeType,
			UploadedAt: d.UploadedAt, Status: d.Status})
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Files fetched successfully", fiber.Map{
		"files":  dtos,
		"cursor": cursor,
	})
}

// DeleteDraft removes a draft. Its file is deleted too unless a task uses it.
func (h *DraftHandler) DeleteDraft(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid file ID format.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}
	owner, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}
	deleted, err := h.files.DeleteDraft(c.Context(), owner, id)
	switch {
	case errors.Is(err, lifecycle.ErrDraftNotFound):
		return response.RespondError(c, fiber.StatusNotFound, response.CodeFileNotFound, "File not found.")
	case err != nil:
		return response.RespondInternal(c, "Failed to delete file.", err)
	case !deleted:
		return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "Draft association removed successfully")
	}
	return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "File deleted successfully")
}
