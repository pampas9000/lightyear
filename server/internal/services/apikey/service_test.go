package apikey

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
)

func TestCreateStoresOnlyTheHash(t *testing.T) {
	db := testutil.OpenDatabase(t)
	s := NewService(db)
	owner := uuid.New()
	key, plaintext, err := s.Create(t.Context(), owner, "  ci  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plaintext, KeyPrefix) || len(plaintext) < 40 {
		t.Fatalf("unexpected key %q", plaintext)
	}
	if key.Name != "ci" || !strings.HasPrefix(plaintext, key.Prefix) || !slices.Equal(key.Scopes, AllScopes) {
		t.Fatalf("unexpected record %+v", key)
	}
	var stored models.APIKey
	if err := db.First(&stored, "id = ?", key.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.KeyHash != Hash(plaintext) || strings.Contains(stored.KeyHash, plaintext[len(KeyPrefix):]) {
		t.Fatalf("stored hash %q does not match the key", stored.KeyHash)
	}
	_, again, err := s.Create(t.Context(), owner, "ci", nil)
	if err != nil || again == plaintext {
		t.Fatalf("second key: %v (same plaintext: %v)", err, again == plaintext)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	db := testutil.OpenDatabase(t)
	s := NewService(db)
	owner := uuid.New()
	for name, test := range map[string]struct {
		name   string
		scopes []string
		err    error
	}{
		"empty name":    {"", nil, ErrInvalidName},
		"long name":     {strings.Repeat("x", 65), nil, ErrInvalidName},
		"unknown scope": {"k", []string{"tasks:read", "admin"}, ErrInvalidScopes},
	} {
		if _, _, err := s.Create(t.Context(), owner, test.name, test.scopes); !errors.Is(err, test.err) {
			t.Errorf("%s: got %v, want %v", name, err, test.err)
		}
	}
	key, _, err := s.Create(t.Context(), owner, "k", []string{ScopeTasksWrite, ScopeTasksRead, ScopeTasksWrite})
	if err != nil || !slices.Equal(key.Scopes, []string{ScopeTasksRead, ScopeTasksWrite}) {
		t.Fatalf("scopes not normalized: %v %v", key, err)
	}
}

func TestActiveKeyLimit(t *testing.T) {
	db := testutil.OpenDatabase(t)
	s := NewService(db)
	owner := uuid.New()
	var first *models.APIKey
	for i := range MaxActiveKeys {
		key, _, err := s.Create(t.Context(), owner, "k", nil)
		if err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
		first = key
	}
	if _, _, err := s.Create(t.Context(), owner, "k", nil); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("expected limit error, got %v", err)
	}
	if _, err := s.Revoke(t.Context(), owner, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(t.Context(), owner, "k", nil); err != nil {
		t.Fatalf("revoking should free a slot: %v", err)
	}
}

func TestAuthenticateRevokeAndLastUsed(t *testing.T) {
	db := testutil.OpenDatabase(t)
	s := NewService(db)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	owner := uuid.New()
	key, plaintext, err := s.Create(t.Context(), owner, "k", []string{ScopeTasksRead})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Authenticate(t.Context(), plaintext)
	if err != nil || got.ID != key.ID || got.OwnerID != owner || !HasScope(got, ScopeTasksRead) || HasScope(got, ScopeTasksWrite) {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
	lastUsed := func() time.Time {
		var k models.APIKey
		if err := db.First(&k, "id = ?", key.ID).Error; err != nil || k.LastUsedAt == nil {
			t.Fatalf("last_used_at: %+v %v", k, err)
		}
		return *k.LastUsedAt
	}
	if !lastUsed().Equal(clock) {
		t.Fatalf("last_used_at = %v, want %v", lastUsed(), clock)
	}
	clock = clock.Add(10 * time.Second)
	if _, err := s.Authenticate(t.Context(), plaintext); err != nil {
		t.Fatal(err)
	}
	if !lastUsed().Equal(clock.Add(-10 * time.Second)) {
		t.Fatal("last_used_at should be written at most once a minute")
	}
	for _, bad := range []string{"", "nope", KeyPrefix, plaintext + "x", strings.TrimPrefix(plaintext, KeyPrefix)} {
		if _, err := s.Authenticate(t.Context(), bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("%q: expected ErrInvalidKey, got %v", bad, err)
		}
	}
	if _, err := s.Revoke(t.Context(), uuid.New(), key.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner revoked the key: %v", err)
	}
	revoked, err := s.Revoke(t.Context(), owner, key.ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", revoked, err)
	}
	if _, err := s.Authenticate(t.Context(), plaintext); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("revoked key authenticated: %v", err)
	}
	keys, err := s.List(t.Context(), owner)
	if err != nil || len(keys) != 1 || keys[0].RevokedAt == nil {
		t.Fatalf("list: %+v %v", keys, err)
	}
}
