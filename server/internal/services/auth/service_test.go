package auth

import (
	"context"
	"errors"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"gorm.io/gorm"
)

// stubProvider returns a fixed profile for any code.
type stubProvider struct{ user OAuthUser }

func (p *stubProvider) GetLoginURL(state string) string {
	return "https://provider.test/?state=" + state
}

func (p *stubProvider) Exchange(context.Context, string) (*OAuthUser, error) {
	u := p.user
	return &u, nil
}

func newStubService(db *gorm.DB, user OAuthUser) *Service {
	return NewServiceWithProviders(db, map[string]OAuthProvider{"stub": &stubProvider{user: user}})
}

func countRows(t *testing.T, db *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOAuthLoginRejectsEmailOfExistingAccount(t *testing.T) {
	db := testutil.OpenDatabase(t)
	ctx := context.Background()
	victim, err := NewServiceWithProviders(db, nil).Register(ctx, RegisterInput{Username: "victim", Email: "victim@example.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}

	// Same address with different casing must not slip through either.
	for _, email := range []string{"victim@example.com", "Victim@Example.com"} {
		s := newStubService(db, OAuthUser{ID: "attacker-1", Email: email, Name: "attacker", Verified: true})
		u, err := s.OAuthLogin(ctx, "stub", "code")
		if !errors.Is(err, ErrOAuthEmailTaken) || u != nil {
			t.Fatalf("%s: got user %v, err %v; want ErrOAuthEmailTaken", email, u, err)
		}
	}
	if n := countRows(t, db, &models.UserOauthAccount{}); n != 0 {
		t.Fatalf("created %d oauth bindings", n)
	}
	if n := countRows(t, db, &models.User{}); n != 1 {
		t.Fatalf("users = %d, want only the victim", n)
	}
	var reloaded models.User
	if err := db.First(&reloaded, "id = ?", victim.ID).Error; err != nil || reloaded.Email != "victim@example.com" {
		t.Fatalf("victim changed: %v %+v", err, reloaded)
	}
}

func TestOAuthLoginRejectsUnverifiedEmail(t *testing.T) {
	db := testutil.OpenDatabase(t)
	s := newStubService(db, OAuthUser{ID: "gh-1", Email: "new@example.com", Name: "new", Verified: false})
	u, err := s.OAuthLogin(context.Background(), "stub", "code")
	if !errors.Is(err, ErrOAuthEmailUnverified) || u != nil {
		t.Fatalf("got user %v, err %v; want ErrOAuthEmailUnverified", u, err)
	}
	if countRows(t, db, &models.User{}) != 0 || countRows(t, db, &models.UserOauthAccount{}) != 0 {
		t.Fatal("an unverified login created rows")
	}
}

func TestOAuthLoginCreatesThenReusesBinding(t *testing.T) {
	db := testutil.OpenDatabase(t)
	ctx := context.Background()
	if _, err := NewServiceWithProviders(db, nil).Register(ctx, RegisterInput{Username: "alice", Email: "other@example.com", Password: "password123"}); err != nil {
		t.Fatal(err)
	}

	s := newStubService(db, OAuthUser{ID: "g-42", Email: "alice@example.com", Name: "alice", Verified: true})
	first, err := s.OAuthLogin(ctx, "stub", "code")
	if err != nil {
		t.Fatal(err)
	}
	if first.Username != "alice-1" || first.Email != "alice@example.com" {
		t.Fatalf("new user = %+v; want a deduplicated username", first)
	}

	// The binding, not the email, identifies the user: a later login with a
	// changed provider email still lands on the same account.
	s = newStubService(db, OAuthUser{ID: "g-42", Email: "renamed@example.com", Name: "alice", Verified: true})
	again, err := s.OAuthLogin(ctx, "stub", "code")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("second login = %s, want %s", again.ID, first.ID)
	}
	if n := countRows(t, db, &models.UserOauthAccount{}); n != 1 {
		t.Fatalf("bindings = %d, want 1", n)
	}
}

func TestPickGithubEmail(t *testing.T) {
	cases := []struct {
		name   string
		emails []githubEmail
		want   string
	}{
		{"primary verified wins", []githubEmail{{"a@x", false, true}, {"b@x", true, true}}, "b@x"},
		{"any verified", []githubEmail{{"p@x", true, false}, {"v@x", false, true}}, "v@x"},
		{"unverified primary rejected", []githubEmail{{"p@x", true, false}}, ""},
		{"unverified first rejected", []githubEmail{{"a@x", false, false}, {"b@x", false, false}}, ""},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		got, ok := pickGithubEmail(tc.emails)
		if got.Email != tc.want || ok != (tc.want != "") || (ok && !got.Verified) {
			t.Errorf("%s: got %+v %v, want %q", tc.name, got, ok, tc.want)
		}
	}
}
