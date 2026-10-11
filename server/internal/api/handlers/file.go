package handlers

import (
	"errors"
	"time"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type FileHandler struct {
	db      *gorm.DB
	storage *storage.S3Service
	bucket  string
}

func NewFileHandler(db *gorm.DB, storage *storage.S3Service, bucket string) *FileHandler {
	return &FileHandler{
		db:      db,
		storage: storage,
		bucket:  bucket,
	}
}

func (h *FileHandler) Install(router fiber.Router) {
	// File download URL generation
	router.Get("/files/:id/download-url", h.GetDownloadURL)
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
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to fetch file record.", map[string]any{"reason": err.Error()})
	}

	// Restrict to UPLOADED files only to prevent downloading partial or failed items
	if file.Status != "UPLOADED" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, "File is not fully uploaded or completed yet.")
	}

	// Generate a 15-minute presigned download URL
	url, err := h.storage.GetPresignedURL(c.Context(), h.bucket, file.Path, false, 15*time.Minute)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to generate download URL", map[string]any{"reason": err.Error()})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Download URL generated successfully.", fiber.Map{
		"url": url,
	})
}
