package handlers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/config"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type UploadHandler struct {
	db      *gorm.DB
	cfg     config.Config
	storage *storage.S3Service
}

func NewUploadHandler(db *gorm.DB, cfg config.Config, storage *storage.S3Service) *UploadHandler {
	return &UploadHandler{
		db:      db,
		cfg:     cfg,
		storage: storage,
	}
}

func (h *UploadHandler) Install(router fiber.Router) {
	// S3 Multipart Upload procedures
	router.Post("/upload/multipart/init", h.CreateMultipartUpload)
	router.Get("/upload/multipart/:uploadId/list-parts", h.ListParts)
	router.Get("/upload/multipart/:uploadId/sign-part/:partNumber", h.SignPart)
	router.Post("/upload/multipart/:uploadId/complete", h.CompleteMultipartUpload)
	router.Post("/upload/multipart/:uploadId/abort", h.AbortMultipartUpload)
}

type CreateMultipartRequest struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Hash     string `json:"hash"`
}

func (h *UploadHandler) CreateMultipartUpload(c fiber.Ctx) error {
	var req CreateMultipartRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
	}

	// SHA-256 Fast Upload check
	if req.Hash != "" {
		var existing models.File
		if errDb := h.db.WithContext(c.Context()).Where("owner_id = ? AND hash = ? AND status = ?", ownerID, req.Hash, "UPLOADED").First(&existing).Error; errDb == nil {
			// Lock check to ensure DraftFile doesn't already exist for this exact file to avoid duplicates in the same queue
			var existingDraft models.DraftFile
			if errDraft := h.db.WithContext(c.Context()).Where("file_id = ? AND owner_id = ?", existing.ID, ownerID).First(&existingDraft).Error; errDraft != nil {
				draftFile := &models.DraftFile{
					FileID:  existing.ID,
					OwnerID: ownerID,
				}
				if errCreate := h.db.WithContext(c.Context()).Create(draftFile).Error; errCreate == nil {
					return c.JSON(fiber.Map{
						"uploadId":   "",
						"key":        existing.Path,
						"fileId":     existing.ID.String(),
						"fastUpload": true,
					})
				}
			} else {
				// If draft already exists, just return it directly (idempotent / duplicate prevention)
				return c.JSON(fiber.Map{
					"uploadId":   "",
					"key":        existing.Path,
					"fileId":     existing.ID.String(),
					"fastUpload": true,
				})
			}
		}
	}

	ext := filepath.Ext(req.Filename)
	// Create a unique key like: uploads/user-id/uuid/filename.ext
	key := fmt.Sprintf("uploads/%s/%s%s", userIdStr, uuid.New().String(), ext)
	contentType := req.Type
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadId, err := h.storage.CreateMultipartUpload(c.Context(), h.cfg.S3.Bucket, key, contentType)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create multipart upload", map[string]any{"reason": err.Error()})
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	file := &models.File{
		OwnerID:         ownerID,
		Name:            req.Filename,
		Path:            key,
		MimeType:        contentType,
		Status:          "UPLOADING",
		UploadID:        uploadId,
		UploadExpiresAt: &expiresAt,
		Size:            0,
		Hash:            req.Hash,
	}
	err = h.db.WithContext(c.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(file).Error; err != nil {
			return err
		}
		draftFile := &models.DraftFile{
			FileID:  file.ID,
			OwnerID: ownerID,
		}
		return tx.Create(draftFile).Error
	})
	if err != nil {
		// Compensate by aborting the multipart upload in S3 to prevent leaking
		_ = h.storage.AbortMultipartUpload(c.Context(), h.cfg.S3.Bucket, key, uploadId)
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create file and draft record", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{
		"uploadId": uploadId,
		"key":      key,
		"fileId":   file.ID.String(),
	})
}

func (h *UploadHandler) ListParts(c fiber.Ctx) error {
	uploadId := c.Params("uploadId")
	key := c.Query("key")

	if key == "" || uploadId == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "uploadId and key are required")
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
	}

	// Owner validation via DB record check
	var file models.File
	if err := h.db.Where("upload_id = ? AND owner_id = ? AND path = ?", uploadId, ownerID, key).First(&file).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.RespondError(c, fiber.StatusForbidden, response.CodeForbidden, "Access denied or upload not found")
		}
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Database error")
	}

	parts, err := h.storage.ListParts(c.Context(), h.cfg.S3.Bucket, key, uploadId)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to list parts", map[string]any{"reason": err.Error()})
	}

	// Format required by Uppy
	type uppyPart struct {
		PartNumber int32  `json:"PartNumber"`
		Size       int64  `json:"Size"`
		ETag       string `json:"ETag"`
	}

	var formatted []uppyPart
	for _, p := range parts {
		var etag string
		if p.ETag != nil {
			etag = *p.ETag
		}
		var size int64
		if p.Size != nil {
			size = *p.Size
		}
		formatted = append(formatted, uppyPart{
			PartNumber: *p.PartNumber,
			Size:       size,
			ETag:       etag,
		})
	}

	return c.JSON(formatted)
}

func (h *UploadHandler) SignPart(c fiber.Ctx) error {
	uploadId := c.Params("uploadId")
	partNumberStr := c.Params("partNumber")
	key := c.Query("key")

	if key == "" || uploadId == "" || partNumberStr == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "uploadId, key and partNumber are required")
	}

	var partNumber int32
	if _, err := fmt.Sscanf(partNumberStr, "%d", &partNumber); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid partNumber")
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
	if err := h.db.Where("upload_id = ? AND owner_id = ? AND path = ?", uploadId, ownerID, key).First(&file).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.RespondError(c, fiber.StatusForbidden, response.CodeForbidden, "Access denied or upload not found")
		}
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Database error")
	}

	url, err := h.storage.SignPart(c.Context(), h.cfg.S3.Bucket, key, uploadId, partNumber, 1*time.Hour)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to sign part", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{
		"url": url,
	})
}

type CompleteMultipartRequest struct {
	Key   string `json:"key" validate:"required"`
	Parts []struct {
		PartNumber int32  `json:"PartNumber"`
		ETag       string `json:"ETag"`
	} `json:"parts"`
}

func (h *UploadHandler) CompleteMultipartUpload(c fiber.Ctx) error {
	uploadId := c.Params("uploadId")
	var req CompleteMultipartRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}

	if uploadId == "" || req.Key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "uploadId and key are required")
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
	}

	// 1. Validate prefix to prevent unauthorized access
	expectedPrefix := fmt.Sprintf("uploads/%s/", userIdStr)
	if !strings.HasPrefix(req.Key, expectedPrefix) {
		return response.RespondError(c, fiber.StatusForbidden, response.CodeForbidden, "Access denied to the specified key prefix")
	}

	// 2. Fetch current file upload record in DB
	var file models.File
	if err := h.db.Where("path = ? AND owner_id = ?", req.Key, ownerID).First(&file).Error; err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusNotFound, response.CodeInternal, "Associated file upload record not found", map[string]any{"reason": err.Error()})
	}

	var completedParts []types.CompletedPart
	for _, p := range req.Parts {
		etag := p.ETag
		completedParts = append(completedParts, types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(etag),
		})
	}

	// 3. Complete multipart upload on S3/R2
	err = h.storage.CompleteMultipartUpload(c.Context(), h.cfg.S3.Bucket, req.Key, uploadId, completedParts)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to complete multipart upload on S3", map[string]any{"reason": err.Error()})
	}

	// 4. Retrieve actual uploaded size and type from S3 using HeadObject
	headOut, err := h.storage.HeadObject(c.Context(), h.cfg.S3.Bucket, req.Key)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to query S3 object metadata", map[string]any{"reason": err.Error()})
	}

	actualSize := int64(0)
	if headOut.ContentLength != nil {
		actualSize = *headOut.ContentLength
	}

	if headOut.ContentType != nil && *headOut.ContentType != "" && *headOut.ContentType != "binary/octet-stream" && *headOut.ContentType != "application/octet-stream" {
		file.MimeType = *headOut.ContentType
	}

	// 5. Update File record status to UPLOADED and set S3-verified Size and UploadedAt
	file.Status = "UPLOADED"
	file.Size = actualSize
	now := time.Now()
	file.UploadedAt = &now
	if err := h.db.Save(&file).Error; err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to update file record status to UPLOADED", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{
		"fileId":   file.ID.String(),
		"key":      req.Key,
		"location": fmt.Sprintf("/%s/%s", h.cfg.S3.Bucket, req.Key),
	})
}

type AbortMultipartRequest struct {
	Key string `json:"key" validate:"required"`
}

func (h *UploadHandler) AbortMultipartUpload(c fiber.Ctx) error {
	uploadId := c.Params("uploadId")
	var req AbortMultipartRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}

	if req.Key == "" || uploadId == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "uploadId and key are required")
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
	}

	// Validate prefix
	expectedPrefix := fmt.Sprintf("uploads/%s/", userIdStr)
	if !strings.HasPrefix(req.Key, expectedPrefix) {
		return response.RespondError(c, fiber.StatusForbidden, response.CodeForbidden, "Access denied to the specified key prefix")
	}

	err = h.storage.AbortMultipartUpload(c.Context(), h.cfg.S3.Bucket, req.Key, uploadId)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to abort multipart upload", map[string]any{"reason": err.Error()})
	}

	// Update status of the File record to ABORTED in the database
	var file models.File
	if err := h.db.Where("path = ? AND owner_id = ?", req.Key, ownerID).First(&file).Error; err == nil {
		file.Status = "ABORTED"
		_ = h.db.Save(&file)
	}

	return c.JSON(fiber.Map{})
}
