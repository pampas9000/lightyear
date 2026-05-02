package auth

import (
	"context"
)

// OAuthUser represents a unified user profile returned by any OAuth provider.
type OAuthUser struct {
	ID       string
	Email    string
	Name     string
	Picture  string
	Verified bool
}

// OAuthProvider defines the contract for any supported OAuth provider (Google, GitHub, etc.).
type OAuthProvider interface {
	// GetLoginURL returns the provider's authorization URL with the given state.
	GetLoginURL(state string) string
	// Exchange exchanges the authorization code for an OAuthUser profile.
	Exchange(ctx context.Context, code string) (*OAuthUser, error)
}
