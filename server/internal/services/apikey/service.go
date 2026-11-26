// Package apikey issues, lists, revokes and verifies the API keys that
// authenticate /v1 requests.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Scopes. A key carries any non-empty subset; neither implies the other.
const (
	ScopeTasksRead  = "tasks:read"  // list and fetch tasks
	ScopeTasksWrite = "tasks:write" // upload files, create, cancel and retry tasks
)

// AllScopes lists every scope in display order.
var AllScopes = []string{ScopeTasksRead, ScopeTasksWrite}

const (
	// KeyPrefix starts every key, so leaked keys are easy to recognise and grep for.
	KeyPrefix = "ly_"
	// displayPrefixLen is how much of the key is stored and shown to tell keys apart.
	displayPrefixLen = len(KeyPrefix) + 8
	maxNameLen       = 64
	// MaxActiveKeys bounds the unrevoked keys per user.
	MaxActiveKeys = 20
	// lastUsedGranularity limits last_used_at writes to one per key per interval.
	lastUsedGranularity = time.Minute
)

var (
	ErrInvalidName   = errors.New("name must be 1-64 characters")
	ErrInvalidScopes = errors.New("scopes must be a non-empty subset of tasks:read, tasks:write")
	ErrTooManyKeys   = fmt.Errorf("at most %d active API keys are allowed", MaxActiveKeys)
	ErrNotFound      = errors.New("api key not found")
	// ErrInvalidKey covers malformed, unknown and revoked keys alike.
	ErrInvalidKey = errors.New("invalid or revoked API key")
)

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// Hash returns the stored form of a plaintext key.
func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func generate() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return KeyPrefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// NormalizeScopes validates scopes and returns them deduplicated in AllScopes
// order. An empty list means every scope.
func NormalizeScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return slices.Clone(AllScopes), nil
	}
	for _, s := range scopes {
		if !slices.Contains(AllScopes, s) {
			return nil, ErrInvalidScopes
		}
	}
	out := make([]string, 0, len(AllScopes))
	for _, s := range AllScopes {
		if slices.Contains(scopes, s) {
			out = append(out, s)
		}
	}
	return out, nil
}

// Create issues a key. The returned plaintext is not stored and cannot be
// recovered later.
func (s *Service) Create(ctx context.Context, owner uuid.UUID, name string, scopes []string) (*models.APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return nil, "", ErrInvalidName
	}
	normalized, err := NormalizeScopes(scopes)
	if err != nil {
		return nil, "", err
	}
	plaintext, err := generate()
	if err != nil {
		return nil, "", err
	}
	key := models.APIKey{
		OwnerID: owner,
		Name:    name,
		Prefix:  plaintext[:displayPrefixLen],
		KeyHash: Hash(plaintext),
		Scopes:  models.StringArray(normalized),
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize creations per owner so the active-key limit holds.
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('lightyear:api_keys:' || ?, 0))`, owner.String()).Error; err != nil {
			return err
		}
		var active int64
		if err := tx.Model(&models.APIKey{}).Where("owner_id = ? AND revoked_at IS NULL", owner).Count(&active).Error; err != nil {
			return err
		}
		if active >= MaxActiveKeys {
			return ErrTooManyKeys
		}
		return tx.Create(&key).Error
	})
	if err != nil {
		return nil, "", err
	}
	return &key, plaintext, nil
}

// List returns one page of the owner's keys, newest first, revoked ones
// included (limit after skipping offset), and how many there are in all.
func (s *Service) List(ctx context.Context, owner uuid.UUID, limit, offset int) ([]models.APIKey, int64, error) {
	q := s.db.WithContext(ctx).Model(&models.APIKey{}).Where("owner_id = ?", owner)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	keys := []models.APIKey{}
	if err := q.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&keys).Error; err != nil {
		return nil, 0, err
	}
	return keys, total, nil
}

// Revoke disables a key immediately. Revoking a revoked key is a no-op.
func (s *Service) Revoke(ctx context.Context, owner, id uuid.UUID) (*models.APIKey, error) {
	var key models.APIKey
	if err := s.db.WithContext(ctx).First(&key, "id = ? AND owner_id = ?", id, owner).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if key.RevokedAt != nil {
		return &key, nil
	}
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&models.APIKey{}).
		Where("id = ? AND revoked_at IS NULL", key.ID).Update("revoked_at", now).Error; err != nil {
		return nil, err
	}
	key.RevokedAt = &now
	return &key, nil
}

// Authenticate resolves a plaintext key to its active record and records the
// use (at most once per lastUsedGranularity).
func (s *Service) Authenticate(ctx context.Context, plaintext string) (*models.APIKey, error) {
	if !strings.HasPrefix(plaintext, KeyPrefix) || len(plaintext) < displayPrefixLen {
		return nil, ErrInvalidKey
	}
	var key models.APIKey
	if err := s.db.WithContext(ctx).First(&key, "key_hash = ?", Hash(plaintext)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidKey
		}
		return nil, err
	}
	if key.RevokedAt != nil {
		return nil, ErrInvalidKey
	}
	now := s.now()
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= lastUsedGranularity {
		if err := s.db.WithContext(ctx).Model(&models.APIKey{}).Where("id = ?", key.ID).
			Update("last_used_at", now).Error; err != nil {
			return nil, err
		}
		key.LastUsedAt = &now
	}
	return &key, nil
}

// HasScope reports whether key grants scope.
func HasScope(key *models.APIKey, scope string) bool {
	return key != nil && slices.Contains(key.Scopes, scope)
}
