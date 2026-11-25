// Package upload signs direct-to-storage uploads and records them as files.
// Both the web console (/api/upload) and the public API (/v1/uploads) use it.
package upload

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/lifecycle"
	"transcoder/server/internal/services/storage"
	"transcoder/server/internal/services/usage"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// MultipartThreshold: files above this size use multipart upload; smaller ones one PUT.
	MultipartThreshold = 10 << 20
	minPartSize        = 5 << 20
	// MaxParts is the S3 limit on parts per upload.
	MaxParts = 10000
	// URLExpiry is how long signed upload and part URLs stay valid. A deleted
	// upload's object is removed only once they have run out.
	URLExpiry = lifecycle.UploadURLExpiry
	// uploadWindow is how long an unfinished upload is kept before cleanup.
	uploadWindow = 24 * time.Hour
)

var (
	ErrInvalidSize     = errors.New("size must be positive")
	ErrInvalidHash     = errors.New("hash must be a hex SHA-256")
	ErrInvalidPart     = fmt.Errorf("part number must be between 1 and %d", MaxParts)
	ErrNotFound        = errors.New("upload not found")
	ErrForbidden       = errors.New("access denied to the specified key")
	ErrNotMultipart    = errors.New("upload is not multipart")
	ErrAlreadyUploaded = errors.New("file is already uploaded")
	// ErrObjectMissing: complete was called before the object reached storage.
	ErrObjectMissing = errors.New("object has not been uploaded to storage")
)

// PartSize mirrors the default chunking of @uppy/aws-s3, so the part URLs
// signed at init match the parts the browser cuts.
func PartSize(size int64) int64 {
	return max(minPartSize, (size+MaxParts-1)/MaxParts)
}

type Service struct {
	db      *gorm.DB
	storage *storage.S3Service
	bucket  string
	files   *lifecycle.Service
	quotas  *usage.Quotas
	now     func() time.Time
}

// WithQuotas makes Init check the storage quota. Without it nothing is checked.
func (s *Service) WithQuotas(q *usage.Quotas) *Service {
	s.quotas = q
	return s
}

// NewService builds the upload service. files sets expires_at as an upload
// completes (its retention) and deletes aborted uploads.
func NewService(db *gorm.DB, storage *storage.S3Service, bucket string, files *lifecycle.Service) *Service {
	return &Service{db: db, storage: storage, bucket: bucket, files: files, now: time.Now}
}

type InitInput struct {
	Filename    string
	ContentType string
	Size        int64
	// Hash is an optional hex SHA-256 of the content.
	Hash string
}

// InitResult describes how to send the file. Exactly one of FastUpload, URL
// (single PUT) or UploadID (multipart) applies.
type InitResult struct {
	File       models.File
	FastUpload bool
	URL        string
	UploadID   string
	PartSize   int64
	PartURLs   []string
	// URLExpiresAt is when URL and PartURLs stop working; zero for fast uploads.
	URLExpiresAt time.Time
}

// Init registers a file in UPLOADING state with its draft and signs the
// upload. A hash matching one of the owner's uploaded files reuses that file.
func (s *Service) Init(ctx context.Context, owner uuid.UUID, in InitInput) (*InitResult, error) {
	if in.Size <= 0 {
		return nil, ErrInvalidSize
	}
	var checksum string
	if in.Hash != "" {
		sum, err := hex.DecodeString(in.Hash)
		if err != nil || len(sum) != sha256.Size {
			return nil, ErrInvalidHash
		}
		in.Hash = strings.ToLower(in.Hash)
		checksum = base64.StdEncoding.EncodeToString(sum)
	}

	// SHA-256 fast upload: reuse the owner's file with the same content.
	if in.Hash != "" {
		var existing models.File
		if err := s.db.WithContext(ctx).Where("owner_id = ? AND hash = ? AND status = ?", owner, in.Hash, models.FileUploaded).
			Where(storage.FreshEnough, time.Now()).Order("created_at DESC").First(&existing).Error; err == nil {
			draft := &models.DraftFile{FileID: existing.ID, OwnerID: owner}
			if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "file_id"}},
				DoNothing: true,
			}).Create(draft).Error; err == nil {
				return &InitResult{File: existing, FastUpload: true}, nil
			}
		}
	}

	// A fast upload stores nothing new; a new upload counts its declared size
	// from now on (see usage.StorageUsed).
	if err := s.quotas.CheckUpload(ctx, owner, in.Size); err != nil {
		return nil, err
	}

	key := fmt.Sprintf("uploads/%s/%s%s", owner, uuid.New(), filepath.Ext(in.Filename))
	contentType := in.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Signing is local; only multipart needs a round trip to the store.
	res := &InitResult{URLExpiresAt: s.now().Add(URLExpiry)}
	if in.Size > MultipartThreshold {
		uploadID, err := s.storage.CreateMultipartUpload(ctx, s.bucket, key, contentType)
		if err != nil {
			return nil, fmt.Errorf("create multipart upload: %w", err)
		}
		res.UploadID = uploadID
		res.PartSize = PartSize(in.Size)
		res.PartURLs = make([]string, (in.Size+res.PartSize-1)/res.PartSize)
		for i := range res.PartURLs {
			res.PartURLs[i], err = s.storage.SignPart(ctx, s.bucket, key, uploadID, int32(i+1), URLExpiry)
			if err != nil {
				_ = s.storage.AbortMultipartUpload(ctx, s.bucket, key, uploadID)
				return nil, fmt.Errorf("sign part: %w", err)
			}
		}
	} else {
		url, err := s.storage.PresignPutObject(ctx, s.bucket, key, contentType, checksum, URLExpiry)
		if err != nil {
			return nil, fmt.Errorf("sign upload: %w", err)
		}
		res.URL = url
	}

	expiresAt := s.now().Add(uploadWindow)
	res.File = models.File{
		OwnerID:         owner,
		Name:            in.Filename,
		Path:            key,
		MimeType:        contentType,
		Size:            in.Size,
		Status:          models.FileUploading,
		UploadID:        res.UploadID,
		UploadExpiresAt: &expiresAt,
		Hash:            in.Hash,
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&res.File).Error; err != nil {
			return err
		}
		return tx.Create(&models.DraftFile{FileID: res.File.ID, OwnerID: owner}).Error
	})
	if err != nil {
		// Compensate by aborting the multipart upload so it does not leak.
		if res.UploadID != "" {
			_ = s.storage.AbortMultipartUpload(ctx, s.bucket, key, res.UploadID)
		}
		return nil, fmt.Errorf("create file and draft: %w", err)
	}
	return res, nil
}

// FileByID returns one of the owner's files.
func (s *Service) FileByID(ctx context.Context, owner, id uuid.UUID) (*models.File, error) {
	var file models.File
	if err := s.db.WithContext(ctx).First(&file, "id = ? AND owner_id = ?", id, owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &file, nil
}

// FileByKey returns the owner's file stored at key; keys outside the
// owner's upload prefix are refused.
func (s *Service) FileByKey(ctx context.Context, owner uuid.UUID, key string) (*models.File, error) {
	if !strings.HasPrefix(key, fmt.Sprintf("uploads/%s/", owner)) {
		return nil, ErrForbidden
	}
	var file models.File
	if err := s.db.WithContext(ctx).First(&file, "path = ? AND owner_id = ?", key, owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &file, nil
}

// Part is one uploaded part of a multipart upload.
type Part struct {
	Number int32
	ETag   string
	Size   int64
}

// Complete finishes the upload and marks the file UPLOADED with the size the
// store reports. Parts are required for multipart uploads and ignored for a
// single PUT. Completing an uploaded file returns it unchanged. Only an
// UPLOADING file can be completed: a deleted upload stays deleted, and a URL
// source (FETCHING) is not an upload.
func (s *Service) Complete(ctx context.Context, file *models.File, parts []Part) (*models.File, error) {
	switch file.Status {
	case models.FileUploaded:
		return file, nil
	case models.FileUploading:
	default:
		return nil, ErrNotFound
	}
	if file.UploadID != "" {
		completed := make([]types.CompletedPart, len(parts))
		for i, p := range parts {
			completed[i] = types.CompletedPart{PartNumber: aws.Int32(p.Number), ETag: aws.String(p.ETag)}
		}
		if err := s.storage.CompleteMultipartUpload(ctx, s.bucket, file.Path, file.UploadID, completed); err != nil {
			return nil, fmt.Errorf("complete multipart upload: %w", err)
		}
	}
	head, err := s.storage.HeadObject(ctx, s.bucket, file.Path)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
			return nil, ErrObjectMissing
		}
		return nil, fmt.Errorf("head object: %w", err)
	}
	if head.ContentLength != nil {
		file.Size = *head.ContentLength
	}
	if ct := aws.ToString(head.ContentType); ct != "" && ct != "binary/octet-stream" && ct != "application/octet-stream" {
		file.MimeType = ct
	}
	now := s.now()
	done := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Only an upload still UPLOADING is recorded: one deleted meanwhile
		// stays deleted.
		res := tx.Model(&models.File{}).Where("id = ? AND status = ?", file.ID, models.FileUploading).
			Updates(map[string]any{"status": models.FileUploaded, "size": file.Size, "mime_type": file.MimeType, "uploaded_at": now})
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		done = true
		if _, err := lifecycle.Schedule(tx, s.files.Policy(), file.ID); err != nil {
			return err
		}
		return tx.Raw("SELECT expires_at FROM files WHERE id = ?", file.ID).Row().Scan(&file.ExpiresAt)
	})
	if err != nil {
		return nil, fmt.Errorf("mark file uploaded: %w", err)
	}
	if !done {
		// A concurrent complete may have won; anything else is gone.
		var current models.File
		if err := s.db.WithContext(ctx).First(&current, "id = ?", file.ID).Error; err == nil && current.Status == models.FileUploaded {
			return &current, nil
		}
		return nil, ErrNotFound
	}
	file.Status, file.UploadedAt = models.FileUploaded, &now
	return file, nil
}

// Finish completes an upload from what storage already holds, for clients
// that send the bytes but do not report back (the MCP server). A single PUT
// is checked as in Complete. A multipart upload is completed with the parts
// in storage only when they are 1..n and add up to the declared size;
// otherwise the bytes are not all there and ErrObjectMissing is returned.
func (s *Service) Finish(ctx context.Context, file *models.File) (*models.File, error) {
	switch {
	case file.Status == models.FileUploaded:
		return file, nil
	case file.Status != models.FileUploading:
		return nil, ErrNotFound
	case file.UploadID == "":
		return s.Complete(ctx, file, nil)
	}
	parts, err := s.ListParts(ctx, file)
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("list parts: %w", err)
	}
	slices.SortFunc(parts, func(a, b Part) int { return int(a.Number - b.Number) })
	var total int64
	for i, p := range parts {
		if p.Number != int32(i+1) {
			return nil, fmt.Errorf("%w: part %d is missing", ErrObjectMissing, i+1)
		}
		total += p.Size
	}
	if total != file.Size {
		return nil, fmt.Errorf("%w: %d of %d bytes uploaded", ErrObjectMissing, total, file.Size)
	}
	return s.Complete(ctx, file, parts)
}

// ListParts lists the parts of an unfinished multipart upload.
func (s *Service) ListParts(ctx context.Context, file *models.File) ([]Part, error) {
	if err := unfinishedMultipart(file); err != nil {
		return nil, err
	}
	raw, err := s.storage.ListParts(ctx, s.bucket, file.Path, file.UploadID)
	if err != nil {
		return nil, err
	}
	parts := make([]Part, 0, len(raw))
	for _, p := range raw {
		parts = append(parts, Part{Number: aws.ToInt32(p.PartNumber), ETag: aws.ToString(p.ETag), Size: aws.ToInt64(p.Size)})
	}
	return parts, nil
}

// SignPart signs a fresh URL for one part of an unfinished multipart upload.
func (s *Service) SignPart(ctx context.Context, file *models.File, number int32) (string, time.Time, error) {
	if number < 1 || number > MaxParts {
		return "", time.Time{}, ErrInvalidPart
	}
	if err := unfinishedMultipart(file); err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().Add(URLExpiry)
	url, err := s.storage.SignPart(ctx, s.bucket, file.Path, file.UploadID, number, URLExpiry)
	return url, expires, err
}

// unfinishedMultipart checks that the file is a multipart upload still in
// progress.
func unfinishedMultipart(file *models.File) error {
	switch {
	case file.UploadID == "":
		return ErrNotMultipart
	case file.Status == models.FileUploaded:
		return ErrAlreadyUploaded
	case file.Status != models.FileUploading:
		return ErrNotFound
	}
	return nil
}

// Abort deletes an unfinished multipart upload. The file is DELETED at once
// and its multipart upload aborted; lifecycle removes whatever reached
// storage once the signed part URLs run out.
func (s *Service) Abort(ctx context.Context, file *models.File) error {
	if err := unfinishedMultipart(file); err != nil {
		return err
	}
	deleted, err := s.files.DeleteFile(ctx, file.OwnerID, file.ID)
	if errors.Is(err, lifecycle.ErrFileNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	*file = *deleted
	return nil
}

// Location is the bucket-qualified object path reported to Uppy.
func (s *Service) Location(file *models.File) string {
	return "/" + s.bucket + "/" + file.Path
}

// Draft is an uploaded file the owner has not submitted in a task yet.
type Draft struct {
	ID         uuid.UUID
	Name       string
	Size       int64
	MimeType   string
	Status     string
	UploadedAt time.Time
	// DraftedAt and DraftID order the list; the newest draft comes first.
	DraftedAt time.Time
	DraftID   uuid.UUID
}

// ListDrafts returns one page of the owner's drafts whose upload has
// finished, newest first (limit drafts after skipping offset), and how many
// there are in all.
func (s *Service) ListDrafts(ctx context.Context, owner uuid.UUID, limit, offset int) ([]Draft, int64, error) {
	q := s.db.WithContext(ctx).Table("draft_files").
		Joins("JOIN files ON files.id = draft_files.file_id").
		Where("draft_files.owner_id = ? AND files.status = ? AND files.uploaded_at IS NOT NULL", owner, models.FileUploaded)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var drafts []Draft
	if err := q.Select("draft_files.created_at AS drafted_at, draft_files.id AS draft_id, files.id, files.name, files.size, files.mime_type, files.status, files.uploaded_at").
		Order("draft_files.created_at DESC, draft_files.id DESC").Limit(limit).Offset(offset).Scan(&drafts).Error; err != nil {
		return nil, 0, err
	}
	return drafts, total, nil
}
