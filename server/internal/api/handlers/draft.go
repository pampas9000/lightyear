package handlers

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/storage"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type DraftHandler struct {
	db      *gorm.DB
	storage *storage.S3Service
	bucket  string
}

func NewDraftHandler(db *gorm.DB, storage *storage.S3Service, bucket string) *DraftHandler {
	return &DraftHandler{
		db:      db,
		storage: storage,
		bucket:  bucket,
	}
}

func (h *DraftHandler) Install(router fiber.Router) {
	router.Get("/drafts/list", h.ListDrafts)
	router.Post("/drafts/:id/delete", h.DeleteDraft)
}

func (h *DraftHandler) ListDrafts(c fiber.Ctx) error {
	userIdStr, ok := c.Locals("user_id").(string)
	if !ok || userIdStr == "" {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID in context")
	}

	// Cursor decoding
	var cursorTime time.Time
	var cursorID uuid.UUID
	if cursorStr := c.Query("cursor"); cursorStr != "" {
		decoded, err := base64.StdEncoding.DecodeString(cursorStr)
		if err == nil {
			parts := strings.Split(string(decoded), ",")
			if len(parts) == 2 {
				unixNs, err1 := strconv.ParseInt(parts[0], 10, 64)
				parsedID, err2 := uuid.Parse(parts[1])
				if err1 == nil && err2 == nil {
					cursorTime = time.Unix(0, unixNs)
					cursorID = parsedID
				}
			}
		}
	}

	// Limit decoding
	limitStr := c.Query("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	query := h.db.WithContext(c.Context()).
		Table("draft_files").
		Select("draft_files.created_at as draft_created_at, files.id, files.name, files.size, files.mime_type, files.status, files.uploaded_at").
		Joins("INNER JOIN files ON files.id = draft_files.file_id").
		Where("draft_files.owner_id = ? AND files.status = ? AND files.uploaded_at IS NOT NULL", ownerID, "UPLOADED")

	if !cursorTime.IsZero() && cursorID != uuid.Nil {
		query = query.Where("draft_files.created_at < ? OR (draft_files.created_at = ? AND draft_files.id < ?)", cursorTime, cursorTime, cursorID)
	}

	type QueryResult struct {
		DraftCreatedAt time.Time
		ID             uuid.UUID
		Name           string
		Size           int64
		MimeType       string
		Status         string
		UploadedAt     *time.Time
	}

	var results []QueryResult
	if err := query.Order("draft_files.created_at DESC, draft_files.id DESC").Limit(limit).Scan(&results).Error; err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to list files", map[string]any{"reason": err.Error()})
	}

	// Generate next cursor
	var nextCursor string
	if len(results) == limit {
		last := results[len(results)-1]
		rawCursor := fmt.Sprintf("%d,%s", last.DraftCreatedAt.UnixNano(), last.ID.String())
		nextCursor = base64.StdEncoding.EncodeToString([]byte(rawCursor))
	}

	type FileDTO struct {
		ID         string    `json:"id"`
		Name       string    `json:"name"`
		Size       int64     `json:"size"`
		MimeType   string    `json:"mime_type"`
		UploadedAt time.Time `json:"uploaded_at"`
		Status     string    `json:"status"`
	}

	dtos := make([]FileDTO, 0, len(results))
	for _, r := range results {
		var uploadedAt time.Time
		if r.UploadedAt != nil {
			uploadedAt = *r.UploadedAt
		}
		dtos = append(dtos, FileDTO{
			ID:         r.ID.String(),
			Name:       r.Name,
			Size:       r.Size,
			MimeType:   r.MimeType,
			UploadedAt: uploadedAt,
			Status:     r.Status,
		})
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Files fetched successfully", fiber.Map{
		"files":  dtos,
		"cursor": nextCursor,
	})
}

func (h *DraftHandler) DeleteDraft(c fiber.Ctx) error {
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

	// 1. Transaction to lock row, delete draft association, and mark file as DELETING/ABORTING
	var file models.File
	var draftFile models.DraftFile
	var shouldDeletePhysical bool = true

	err = h.db.WithContext(c.Context()).Transaction(func(tx *gorm.DB) error {
		// SELECT ... FOR UPDATE on DraftFile
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("file_id = ? AND owner_id = ?", parsedID, ownerID).First(&draftFile).Error; err != nil {
			return err
		}

		// SELECT ... FOR UPDATE on File
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", parsedID).First(&file).Error; err != nil {
			return err
		}

		if file.Status == "DELETING" || file.Status == "ABORTING" {
			return errors.New("file is already being deleted")
		}

		// Verify that it is NOT referenced by any legacy or generalized job relation.
		referenced, err := storage.IsFileReferenced(tx, file.ID)
		if err != nil {
			return err
		}
		if referenced {
			shouldDeletePhysical = false
		} else {
			if file.Status == "UPLOADING" {
				file.Status = "ABORTING"
			} else {
				file.Status = "DELETING"
			}
			if err := tx.Save(&file).Error; err != nil {
				return err
			}
		}

		// Delete the draft association in both cases
		return tx.Delete(&draftFile).Error
	})

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeOK, "File not found.")
		}
		if err.Error() == "file is already being deleted" {
			return response.RespondError(c, fiber.StatusConflict, response.CodeParamInvalid, "File is already being deleted")
		}

		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to delete file record.", map[string]any{"reason": err.Error()})
	}

	if !shouldDeletePhysical {
		return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "Draft association removed successfully")
	}

	return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "File deleted successfully")
}
