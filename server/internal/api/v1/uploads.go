package v1

import (
	"errors"
	"strconv"
	"time"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/upload"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

type CreateUploadRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256,omitempty"`
}

// Upload tells the client how to send the file's bytes.
type Upload struct {
	File File `json:"file"`
	// Method is "none" (content already stored), "put" (one PUT to url) or
	// "multipart" (PUT part i+1 to part_urls[i], then complete with the ETags).
	Method    string     `json:"method"`
	URL       string     `json:"url,omitempty"`
	PartSize  int64      `json:"part_size,omitempty"`
	PartURLs  []string   `json:"part_urls,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CompletePart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
}

type CompleteUploadRequest struct {
	Parts []CompletePart `json:"parts,omitempty"`
}

type Part struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
}

type PartURL struct {
	PartNumber int32     `json:"part_number"`
	URL        string    `json:"url"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func (h *Handler) CreateUpload(c fiber.Ctx) error {
	var req CreateUploadRequest
	if err := decodeJSON(c.Body(), &req, false); err != nil {
		return badBody(c, err)
	}
	if req.Filename == "" {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "filename is required.", response.ValidationDetail{Field: "filename", Reason: "empty"})
	}
	res, err := h.uploads.Init(c.Context(), owner(c), upload.InitInput{
		Filename: req.Filename, ContentType: req.ContentType, Size: req.Size, Hash: req.SHA256,
	})
	if err != nil {
		return respondUploadError(c, err, "create upload")
	}
	out := NewUpload(res)
	if res.FastUpload {
		return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "File with this sha256 already uploaded; no upload needed.", out)
	}
	return response.RespondSuccess(c, fiber.StatusCreated, response.CodeOK, "Upload created.", out)
}

// NewUpload describes how to send the bytes of a file registered by upload.Service.Init.
func NewUpload(res *upload.InitResult) Upload {
	out := Upload{File: *newFile(&res.File)}
	switch {
	case res.FastUpload:
		out.Method = "none"
		return out
	case res.UploadID != "":
		out.Method, out.PartSize, out.PartURLs = "multipart", res.PartSize, res.PartURLs
	default:
		out.Method, out.URL = "put", res.URL
	}
	out.ExpiresAt = &res.URLExpiresAt
	return out
}

var errBadUploadID = errors.New("invalid upload id")

func (h *Handler) uploadFile(c fiber.Ctx) (*models.File, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return nil, errBadUploadID
	}
	return h.uploads.FileByID(c.Context(), owner(c), id)
}

func (h *Handler) CompleteUpload(c fiber.Ctx) error {
	var req CompleteUploadRequest
	if err := decodeJSON(c.Body(), &req, true); err != nil {
		return badBody(c, err)
	}
	file, err := h.uploadFile(c)
	if err != nil {
		return respondUploadError(c, err, "complete upload")
	}
	if file.UploadID != "" && file.Status != "UPLOADED" && len(req.Parts) == 0 {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamRequired, "parts are required for a multipart upload.", response.ValidationDetail{Field: "parts", Reason: "empty"})
	}
	parts := make([]upload.Part, len(req.Parts))
	for i, p := range req.Parts {
		parts[i] = upload.Part{Number: p.PartNumber, ETag: p.ETag}
	}
	if file, err = h.uploads.Complete(c.Context(), file, parts); err != nil {
		return respondUploadError(c, err, "complete upload")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Upload completed.", newFile(file))
}

func (h *Handler) ListParts(c fiber.Ctx) error {
	file, err := h.uploadFile(c)
	if err != nil {
		return respondUploadError(c, err, "list parts")
	}
	parts, err := h.uploads.ListParts(c.Context(), file)
	if err != nil {
		return respondUploadError(c, err, "list parts")
	}
	out := make([]Part, len(parts))
	for i, p := range parts {
		out[i] = Part{PartNumber: p.Number, ETag: p.ETag, Size: p.Size}
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Parts fetched.", fiber.Map{"parts": out})
}

func (h *Handler) SignPart(c fiber.Ctx) error {
	number, err := strconv.ParseInt(c.Params("part_number"), 10, 32)
	if err != nil {
		return respondUploadError(c, upload.ErrInvalidPart, "sign part")
	}
	file, err := h.uploadFile(c)
	if err != nil {
		return respondUploadError(c, err, "sign part")
	}
	url, expires, err := h.uploads.SignPart(c.Context(), file, int32(number))
	if err != nil {
		return respondUploadError(c, err, "sign part")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Part URL signed.", PartURL{PartNumber: int32(number), URL: url, ExpiresAt: expires})
}

func (h *Handler) AbortUpload(c fiber.Ctx) error {
	file, err := h.uploadFile(c)
	if err != nil {
		return respondUploadError(c, err, "abort upload")
	}
	if err := h.uploads.Abort(c.Context(), file); err != nil {
		return respondUploadError(c, err, "abort upload")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Upload aborted.", newFile(file))
}

// DeleteFile deletes one of the owner's files (a source or an output). An
// unfinished upload is aborted; the source of an unfinished job is a 409.
func (h *Handler) DeleteFile(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return badID(c, "id")
	}
	f, err := h.files.DeleteFile(c.Context(), owner(c), id)
	switch {
	case errors.Is(err, lifecycle.ErrFileNotFound):
		return response.RespondError(c, fiber.StatusNotFound, response.CodeFileNotFound, "File not found.")
	case errors.Is(err, lifecycle.ErrFileInUse):
		return response.RespondError(c, fiber.StatusConflict, response.CodeFileConflict, err.Error())
	case err != nil:
		return internalError(c, "delete file", err)
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "File deleted.", newFile(f))
}
