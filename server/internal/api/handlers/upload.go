package handlers

import (
	"fmt"
	"path/filepath"
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
	s3Group := router.Group("/s3/multipart")
	s3Group.Post("/", h.CreateMultipartUpload)
	s3Group.Get("/:uploadId", h.ListParts)
	s3Group.Get("/:uploadId/:partNumber", h.SignPart)
	s3Group.Post("/:uploadId/complete", h.CompleteMultipartUpload)
	s3Group.Delete("/:uploadId", h.AbortMultipartUpload)
}

type CreateMultipartRequest struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
}

func (h *UploadHandler) CreateMultipartUpload(c fiber.Ctx) error {
	var req CreateMultipartRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		userIdStr = "anonymous"
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

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID")
	}

	file := &models.File{
		OwnerID:  ownerID,
		Name:     req.Filename,
		Path:     key,
		MimeType: contentType,
	}
	if err := h.db.Create(file).Error; err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create file record", map[string]any{"reason": err.Error()})
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
	if key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "key query parameter is required")
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
	partNumber := c.Params("partNumber")
	key := c.Query("key")
	if key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "key query parameter is required")
	}

	var pNum int32
	if _, err := fmt.Sscanf(partNumber, "%d", &pNum); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid partNumber")
	}

	url, err := h.storage.SignPart(c.Context(), h.cfg.S3.Bucket, key, uploadId, pNum, 1*time.Hour)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to sign part", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{
		"url": url,
	})
}

type CompleteMultipartRequest struct {
	Key   string `json:"key"`
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

	if req.Key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "key is required")
	}

	var completedParts []types.CompletedPart
	for _, p := range req.Parts {
		// ETag needs to be quoted in AWS if it isn't already, but typically S3 requires it exactly as received.
		// AWS SDK handles it, but let's make sure it's passed directly.
		etag := p.ETag
		completedParts = append(completedParts, types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(etag),
		})
	}

	err := h.storage.CompleteMultipartUpload(c.Context(), h.cfg.S3.Bucket, req.Key, uploadId, completedParts)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to complete multipart upload", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{
		"location": fmt.Sprintf("/%s/%s", h.cfg.S3.Bucket, req.Key), // Or an actual CDN URL if configured
	})
}

func (h *UploadHandler) AbortMultipartUpload(c fiber.Ctx) error {
	uploadId := c.Params("uploadId")
	key := c.Query("key")
	if key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "key query parameter is required")
	}

	err := h.storage.AbortMultipartUpload(c.Context(), h.cfg.S3.Bucket, key, uploadId)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to abort multipart upload", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{})
}
