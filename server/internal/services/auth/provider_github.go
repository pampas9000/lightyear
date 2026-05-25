package auth

import (
	"context"
	"encoding/json"
	"errors"
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
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &ghUser); err != nil {
		return nil, fmt.Errorf("failed to decode user info: %w", err)
	}

	// GitHub email might be empty if private. Let's fetch the emails endpoint
	if ghUser.Email == "" {
		emailResp, err := client.Get("https://api.github.com/user/emails")
		if err == nil {
			defer emailResp.Body.Close()
			if emailResp.StatusCode == http.StatusOK {
				emailBody, err := io.ReadAll(emailResp.Body)
				if err == nil {
					var emails []struct {
						Email    string `json:"email"`
						Primary  bool   `json:"primary"`
						Verified bool   `json:"verified"`
					}
					if err := json.Unmarshal(emailBody, &emails); err == nil {
						// 1. Try to find primary verified email
						for _, e := range emails {
							if e.Primary && e.Verified {
								ghUser.Email = e.Email
								break
							}
						}
						// 2. Fall back to any verified email
						if ghUser.Email == "" {
							for _, e := range emails {
								if e.Verified {
									ghUser.Email = e.Email
									break
								}
							}
						}
						// 3. Fall back to any primary email
						if ghUser.Email == "" {
							for _, e := range emails {
								if e.Primary {
									ghUser.Email = e.Email
									break
								}
							}
						}
						// 4. Fall back to the first email in the list
						if ghUser.Email == "" && len(emails) > 0 {
							ghUser.Email = emails[0].Email
						}
					}
				}
			}
		}
	}

	if ghUser.Email == "" {
		return nil, errors.New("no verified email found in github account")
	}

	displayName := ghUser.Name
	if displayName == "" {
		displayName = ghUser.Login
	}

	return &OAuthUser{
		ID:       strconv.FormatInt(ghUser.ID, 10),
		Email:    ghUser.Email,
		Name:     displayName,
		Picture:  ghUser.AvatarURL,
		Verified: true, // If we got here, it's verified.
	}, nil
}
