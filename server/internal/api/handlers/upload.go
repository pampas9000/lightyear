package handlers

import (
	"errors"
	"strconv"

	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/upload"
	"transcoder/server/internal/services/usage"

	"github.com/gofiber/fiber/v3"
)

// UploadHandler serves the web console's upload endpoints. Like every /api
// route they answer with the response envelope; the console adapts the data
// to what Uppy expects. /v1/uploads serves the same service to API callers.
type UploadHandler struct {
	service *upload.Service
}

func NewUploadHandler(service *upload.Service) *UploadHandler {
	return &UploadHandler{service: service}
}

func (h *UploadHandler) Install(router fiber.Router) {
	router.Post("/upload/init", h.InitUpload)
	router.Post("/upload/complete", h.CompleteUpload)
	// Multipart only: resume and re-sign an expired part URL.
	router.Get("/upload/multipart/:uploadId/list-parts", h.ListParts)
	router.Get("/upload/multipart/:uploadId/sign-part/:partNumber", h.SignPart)
	router.Post("/upload/multipart/:uploadId/abort", h.AbortMultipartUpload)
}

type InitUploadRequest struct {
	Filename string `json:"filename"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Hash     string `json:"hash"`
}

// respondUploadError maps upload service errors for both /api and /v1.
func respondUploadError(c fiber.Ctx, err error, action string) error {
	switch {
	case errors.Is(err, errNoUser):
		return respondNoUser(c)
	case errors.Is(err, usage.ErrQuotaExceeded):
		return response.RespondQuotaExceeded(c, err)
	case errors.Is(err, upload.ErrInvalidSize):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "size", Reason: "must be positive"})
	case errors.Is(err, upload.ErrInvalidHash):
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error())
	case errors.Is(err, upload.ErrInvalidPart):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "part_number", Reason: "out of range"})
	case errors.Is(err, upload.ErrForbidden):
		return response.RespondError(c, fiber.StatusForbidden, response.CodeForbidden, "Access denied to the specified upload.")
	case errors.Is(err, upload.ErrNotFound):
		return response.RespondError(c, fiber.StatusNotFound, response.CodeFileNotFound, "Upload not found.")
	case errors.Is(err, upload.ErrNotMultipart), errors.Is(err, upload.ErrAlreadyUploaded), errors.Is(err, upload.ErrObjectMissing):
		return response.RespondError(c, fiber.StatusConflict, response.CodeParamInvalid, err.Error())
	}
	return response.RespondInternal(c, "Failed to "+action+".", err)
}

func (h *UploadHandler) InitUpload(c fiber.Ctx) error {
	var req InitUploadRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}
	owner, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}
	res, err := h.service.Init(c.Context(), owner, upload.InitInput{
		Filename: req.Filename, ContentType: req.Type, Size: req.Size, Hash: req.Hash,
	})
	if err != nil {
		return respondUploadError(c, err, "initialize upload")
	}
	out := InitUploadData{Key: res.File.Path, FileID: res.File.ID.String()}
	switch {
	case res.FastUpload:
		out.FastUpload = true
		return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "File already uploaded; no upload needed.", out)
	case res.UploadID != "":
		out.UploadID, out.PartURLs = res.UploadID, res.PartURLs
	default:
		out.URL = res.URL
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Upload initialized.", out)
}

// InitUploadData tells the console how to send a file's bytes: nothing
// (fast_upload, the content is already stored), one PUT to url, or a
// multipart upload whose part i+1 goes to part_urls[i].
type InitUploadData struct {
	FileID     string   `json:"file_id"`
	Key        string   `json:"key"`
	FastUpload bool     `json:"fast_upload,omitempty"`
	URL        string   `json:"url,omitempty"`
	UploadID   string   `json:"upload_id,omitempty"`
	PartURLs   []string `json:"part_urls,omitempty"`
}

// UploadPart is a part S3 has stored for a multipart upload.
type UploadPart struct {
	PartNumber int32  `json:"part_number"`
	Size       int64  `json:"size"`
	ETag       string `json:"etag"`
}

// multipartFile resolves the owner's file for a multipart route and checks
// that the upload ID in the path belongs to it.
func (h *UploadHandler) multipartFile(c fiber.Ctx, key string) (*models.File, error) {
	owner, ok := middleware.UserID(c)
	if !ok {
		return nil, errNoUser
	}
	if key == "" {
		return nil, upload.ErrNotFound
	}
	file, err := h.service.FileByKey(c.Context(), owner, key)
	if err != nil {
		return nil, err
	}
	if file.UploadID == "" || file.UploadID != c.Params("uploadId") {
		return nil, upload.ErrForbidden
	}
	return file, nil
}

func (h *UploadHandler) ListParts(c fiber.Ctx) error {
	file, err := h.multipartFile(c, c.Query("key"))
	if err != nil {
		return respondUploadError(c, err, "list parts")
	}
	parts, err := h.service.ListParts(c.Context(), file)
	if err != nil {
		return respondUploadError(c, err, "list parts")
	}
	out := make([]UploadPart, len(parts))
	for i, p := range parts {
		out[i] = UploadPart{PartNumber: p.Number, Size: p.Size, ETag: p.ETag}
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Parts fetched.", fiber.Map{"parts": out})
}

func (h *UploadHandler) SignPart(c fiber.Ctx) error {
	number, err := strconv.ParseInt(c.Params("partNumber"), 10, 32)
	if err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid partNumber.")
	}
	file, err := h.multipartFile(c, c.Query("key"))
	if err != nil {
		return respondUploadError(c, err, "sign part")
	}
	url, expires, err := h.service.SignPart(c.Context(), file, int32(number))
	if err != nil {
		return respondUploadError(c, err, "sign part")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Part URL signed.", fiber.Map{
		"part_number": int32(number), "url": url, "expires_at": expires,
	})
}

type CompleteUploadRequest struct {
	Key string `json:"key"`
	// Parts is required for multipart uploads and ignored for a single PUT.
	Parts []struct {
		PartNumber int32  `json:"part_number"`
		ETag       string `json:"etag"`
	} `json:"parts"`
}

func (h *UploadHandler) CompleteUpload(c fiber.Ctx) error {
	var req CompleteUploadRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}
	if req.Key == "" {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamRequired, "key is required")
	}
	owner, ok := middleware.UserID(c)
	if !ok {
		return respondNoUser(c)
	}
	file, err := h.service.FileByKey(c.Context(), owner, req.Key)
	if err != nil {
		return respondUploadError(c, err, "complete upload")
	}
	parts := make([]upload.Part, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = upload.Part{Number: p.PartNumber, ETag: p.ETag}
	}
	if file, err = h.service.Complete(c.Context(), file, parts); err != nil {
		return respondUploadError(c, err, "complete upload")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Upload completed.", fiber.Map{
		"file_id":  file.ID.String(),
		"key":      file.Path,
		"location": h.service.Location(file),
	})
}

type AbortMultipartRequest struct {
	Key string `json:"key"`
}

func (h *UploadHandler) AbortMultipartUpload(c fiber.Ctx) error {
	var req AbortMultipartRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}
	file, err := h.multipartFile(c, req.Key)
	if err != nil {
		return respondUploadError(c, err, "abort upload")
	}
	if err := h.service.Abort(c.Context(), file); err != nil {
		return respondUploadError(c, err, "abort upload")
	}
	return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "Upload aborted.")
}
