package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"transcoder/server/internal/config"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
)

type GithubProvider struct {
	oauth2Config *oauth2.Config
}

func NewGithubProvider(cfg config.GithubOAuthConfig) *GithubProvider {
	oauth2Config := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.CallbackURL,
		Endpoint:     github.Endpoint,
		Scopes:       []string{"read:user", "user:email"},
	}

	return &GithubProvider{
		oauth2Config: oauth2Config,
	}
}

func (p *GithubProvider) GetLoginURL(state string) string {
	return p.oauth2Config.AuthCodeURL(state)
}

func (p *GithubProvider) Exchange(ctx context.Context, code string) (*OAuthUser, error) {
	oauth2Token, err := p.oauth2Config.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth2 exchange failed: %w", err)
	}

	client := p.oauth2Config.Client(ctx, oauth2Token)

	// Fetch user profile
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var ghUser struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &ghUser); err != nil {
		return nil, fmt.Errorf("failed to decode user info: %w", err)
	}

	// The profile email carries no verification flag, so the address always
	// comes from /user/emails, which does.
	emails, err := fetchGithubEmails(client)
	if err != nil {
		return nil, err
	}
	email, ok := pickGithubEmail(emails)
	if !ok {
		return nil, ErrOAuthEmailUnverified
	}

	displayName := ghUser.Name
	if displayName == "" {
		displayName = ghUser.Login
	}

	return &OAuthUser{
		ID:       strconv.FormatInt(ghUser.ID, 10),
		Email:    email.Email,
		Name:     displayName,
		Picture:  ghUser.AvatarURL,
		Verified: email.Verified,
	}, nil
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func fetchGithubEmails(client *http.Client) ([]githubEmail, error) {
	resp, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return nil, fmt.Errorf("failed to get user emails: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github emails api returned status %d", resp.StatusCode)
	}
	var emails []githubEmail
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return nil, fmt.Errorf("failed to decode user emails: %w", err)
	}
	return emails, nil
}

// pickGithubEmail returns the primary verified email, else any verified one.
// Unverified addresses are never returned.
func pickGithubEmail(emails []githubEmail) (githubEmail, bool) {
	for _, e := range emails {
		if e.Primary && e.Verified && e.Email != "" {
			return e, true
		}
	}
	for _, e := range emails {
		if e.Verified && e.Email != "" {
			return e, true
		}
	}
	return githubEmail{}, false
}
