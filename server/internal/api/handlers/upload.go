package handlers

import (
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
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
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

	// 5. Update File record status to UPLOADED and set S3-verified Size
	file.Status = "UPLOADED"
	file.Size = actualSize
	if err := h.db.Save(&file).Error; err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to update file record status to UPLOADED", map[string]any{"reason": err.Error()})
	}

	return c.JSON(fiber.Map{
		"fileId":   file.ID.String(),
		"key":      req.Key,
		"location": fmt.Sprintf("/%s/%s", h.cfg.S3.Bucket, req.Key),
	})
}

func (h *UploadHandler) AbortMultipartUpload(c fiber.Ctx) error {
	uploadId := c.Params("uploadId")
	key := c.Query("key")
	if key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "key query parameter is required")
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
	if !strings.HasPrefix(key, expectedPrefix) {
		return response.RespondError(c, fiber.StatusForbidden, response.CodeForbidden, "Access denied to the specified key prefix")
	}

	err = h.storage.AbortMultipartUpload(c.Context(), h.cfg.S3.Bucket, key, uploadId)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to abort multipart upload", map[string]any{"reason": err.Error()})
	}

	// Update status of the File record to ABORTED in the database
	var file models.File
	if err := h.db.Where("path = ? AND owner_id = ?", key, ownerID).First(&file).Error; err == nil {
		file.Status = "ABORTED"
		_ = h.db.Save(&file)
	}

	return c.JSON(fiber.Map{})
}

