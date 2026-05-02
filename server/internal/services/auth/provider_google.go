package auth

import (
	"context"
	"errors"
	"fmt"

	"transcoder/server/internal/config"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type GoogleProvider struct {
	oauth2Config *oauth2.Config
	oidcVerifier *oidc.IDTokenVerifier
}

func NewGoogleProvider(ctx context.Context, cfg config.GoogleOAuthConfig) (*GoogleProvider, error) {
	provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("failed to get oidc provider: %w", err)
	}

	oauth2Config := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.CallbackURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	oidcConfig := &oidc.Config{
		ClientID: cfg.ClientID,
	}
	verifier := provider.Verifier(oidcConfig)

	return &GoogleProvider{
		oauth2Config: oauth2Config,
		oidcVerifier: verifier,
	}, nil
}

func (p *GoogleProvider) GetLoginURL(state string) string {
	return p.oauth2Config.AuthCodeURL(state)
}

func (p *GoogleProvider) Exchange(ctx context.Context, code string) (*OAuthUser, error) {
	oauth2Token, err := p.oauth2Config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth2 exchange failed: %w", err)
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token field in oauth2 token")
	}

	idToken, err := p.oidcVerifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to verify id token: %w", err)
	}

	var claims struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
		Name     string `json:"name"`
		Picture  string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse claims: %w", err)
	}

	return &OAuthUser{
		ID:       idToken.Subject,
		Email:    claims.Email,
		Name:     claims.Name,
		Picture:  claims.Picture,
		Verified: claims.Verified,
	}, nil
}
