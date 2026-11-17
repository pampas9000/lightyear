package handlers

import (
	"errors"
	"time"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type FileHandler struct {
	db      *gorm.DB
	storage *storage.S3Service
	bucket  string
	files   *lifecycle.Service
}

func NewFileHandler(db *gorm.DB, storage *storage.S3Service, bucket string, files *lifecycle.Service) *FileHandler {
	return &FileHandler{
		db:      db,
		storage: storage,
		bucket:  bucket,
		files:   files,
	}
}

func (h *FileHandler) Install(router fiber.Router) {
	// File download URL generation
	router.Get("/files/:id/download-url", h.GetDownloadURL)
	router.Delete("/files/:id", h.DeleteFile)
}

// DeleteFile deletes one of the user's files (a source or an output).
func (h *FileHandler) DeleteFile(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid file ID format.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}
	ownerID, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}
	f, err := h.files.DeleteFile(c.Context(), ownerID, id)
	switch {
	case errors.Is(err, lifecycle.ErrFileNotFound):
		return response.RespondError(c, fiber.StatusNotFound, response.CodeFileNotFound, "File not found.")
	case errors.Is(err, lifecycle.ErrFileInUse):
		return response.RespondError(c, fiber.StatusConflict, response.CodeFileConflict, err.Error())
	case err != nil:
		return response.RespondInternal(c, "Failed to delete file.", err)
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "File deleted.", f)
}

func (h *FileHandler) GetDownloadURL(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid file ID format.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
	}

	var file models.File
	if err := h.db.Where("id = ? AND owner_id = ?", parsedID, ownerID).First(&file).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeOK, "File not found.")
		}
		return response.RespondInternal(c, "Failed to fetch file record.", err)
	}

	switch file.Status {
	case models.FileExpired, models.FileDeleted:
		return response.RespondErrorWithDetails(c, fiber.StatusGone, response.CodeFileGone, "File has expired or was deleted.", map[string]any{"status": file.Status})
	}
	// Restrict to UPLOADED files only to prevent downloading partial or failed items
	if file.Status != models.FileUploaded {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, "File is not fully uploaded or completed yet.")
	}

	// Generate a 15-minute presigned download URL
	url, err := h.storage.GetPresignedURL(c.Context(), h.bucket, file.Path, file.Name, false, 15*time.Minute)
	if err != nil {
		return response.RespondInternal(c, "Failed to generate download URL", err)
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Download URL generated successfully.", fiber.Map{
		"url": url,
	})
}
