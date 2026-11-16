package handlers

import (
	"errors"
	"strconv"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/upload"

	"github.com/gofiber/fiber/v3"
)

// UploadHandler serves the web console's upload endpoints. Their bodies are
// bare camelCase objects because Uppy expects that shape; /v1/uploads wraps
// the same service in the standard envelope.
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
	case errors.Is(err, upload.ErrNotMultipart), errors.Is(err, upload.ErrAlreadyUploaded):
		return response.RespondError(c, fiber.StatusConflict, response.CodeParamInvalid, err.Error())
	}
	return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to "+action+".", map[string]any{"reason": err.Error()})
}

func (h *UploadHandler) InitUpload(c fiber.Ctx) error {
	var req InitUploadRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body")
	}
	owner, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
	}
	res, err := h.service.Init(c.Context(), owner, upload.InitInput{
		Filename: req.Filename, ContentType: req.Type, Size: req.Size, Hash: req.Hash,
	})
	if err != nil {
		return respondUploadError(c, err, "initialize upload")
	}
	body := fiber.Map{"key": res.File.Path, "fileId": res.File.ID.String()}
	switch {
	case res.FastUpload:
		body["fastUpload"] = true
	case res.UploadID != "":
		body["uploadId"] = res.UploadID
		body["partUrls"] = res.PartURLs
	default:
		body["url"] = res.URL
	}
	return c.JSON(body)
}

// multipartFile resolves the owner's file for a multipart route and checks
// that the upload ID in the path belongs to it.
func (h *UploadHandler) multipartFile(c fiber.Ctx, key string) (*models.File, error) {
	owner, err := ownerFromContext(c)
	if err != nil {
		return nil, err
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
	// Format required by Uppy.
	type uppyPart struct {
		PartNumber int32  `json:"PartNumber"`
		Size       int64  `json:"Size"`
		ETag       string `json:"ETag"`
	}
	formatted := make([]uppyPart, len(parts))
	for i, p := range parts {
		formatted[i] = uppyPart{PartNumber: p.Number, Size: p.Size, ETag: p.ETag}
	}
	return c.JSON(formatted)
}

func (h *UploadHandler) SignPart(c fiber.Ctx) error {
	number, err := strconv.ParseInt(c.Params("partNumber"), 10, 32)
	if err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid partNumber")
	}
	file, err := h.multipartFile(c, c.Query("key"))
	if err != nil {
		return respondUploadError(c, err, "sign part")
	}
	url, _, err := h.service.SignPart(c.Context(), file, int32(number))
	if err != nil {
		return respondUploadError(c, err, "sign part")
	}
	return c.JSON(fiber.Map{"url": url})
}

type CompleteUploadRequest struct {
	Key string `json:"key"`
	// Parts is required for multipart uploads and ignored for a single PUT.
	Parts []struct {
		PartNumber int32  `json:"PartNumber"`
		ETag       string `json:"ETag"`
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
	owner, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context")
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
	return c.JSON(fiber.Map{
		"fileId":   file.ID.String(),
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
	return c.JSON(fiber.Map{})
}
