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
	"strings"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/storage"

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
	// URLExpiry is how long signed upload and part URLs stay valid.
	URLExpiry = time.Hour
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
	now     func() time.Time
}

func NewService(db *gorm.DB, storage *storage.S3Service, bucket string) *Service {
	return &Service{db: db, storage: storage, bucket: bucket, now: time.Now}
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
		Status:          "UPLOADING",
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
// single PUT. Completing an uploaded file returns it unchanged.
func (s *Service) Complete(ctx context.Context, file *models.File, parts []Part) (*models.File, error) {
	if file.Status == "UPLOADED" {
		return file, nil
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
	file.Status = "UPLOADED"
	file.UploadedAt = &now
	if err := s.db.WithContext(ctx).Save(file).Error; err != nil {
		return nil, fmt.Errorf("mark file uploaded: %w", err)
	}
	return file, nil
}

// ListParts lists the parts of an unfinished multipart upload.
func (s *Service) ListParts(ctx context.Context, file *models.File) ([]Part, error) {
	if file.UploadID == "" {
		return nil, ErrNotMultipart
	}
	if file.Status == "UPLOADED" {
		return nil, ErrAlreadyUploaded
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
	if file.UploadID == "" {
		return "", time.Time{}, ErrNotMultipart
	}
	if file.Status == "UPLOADED" {
		return "", time.Time{}, ErrAlreadyUploaded
	}
	expires := s.now().Add(URLExpiry)
	url, err := s.storage.SignPart(ctx, s.bucket, file.Path, file.UploadID, number, URLExpiry)
	return url, expires, err
}

// Abort cancels an unfinished multipart upload and marks the file ABORTED.
func (s *Service) Abort(ctx context.Context, file *models.File) error {
	if file.UploadID == "" {
		return ErrNotMultipart
	}
	if file.Status == "UPLOADED" {
		return ErrAlreadyUploaded
	}
	if err := s.storage.AbortMultipartUpload(ctx, s.bucket, file.Path, file.UploadID); err != nil {
		return err
	}
	file.Status = "ABORTED"
	return s.db.WithContext(ctx).Model(file).Update("status", "ABORTED").Error
}

// Location is the bucket-qualified object path reported to Uppy.
func (s *Service) Location(file *models.File) string {
	return "/" + s.bucket + "/" + file.Path
}
