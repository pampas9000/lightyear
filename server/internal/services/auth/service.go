package auth

import (
	"context"
	"errors"
	"fmt"

	"transcoder/server/internal/config"
	"transcoder/server/internal/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserAlreadyExists  = errors.New("user already exists")
	ErrUnsupportedOAuth   = errors.New("unsupported oauth provider")
)

type Service struct {
	db        *gorm.DB
	providers map[string]OAuthProvider
}

func NewService(ctx context.Context, db *gorm.DB, cfg config.Config) (*Service, error) {
	providers := make(map[string]OAuthProvider)

	if cfg.GoogleOAuth.ClientID != "" {
		googleProvider, err := NewGoogleProvider(ctx, cfg.GoogleOAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to init google provider: %w", err)
		}
		providers["google"] = googleProvider
	}

	if cfg.GithubOAuth.ClientID != "" {
		providers["github"] = NewGithubProvider(cfg.GithubOAuth)
	}

	return &Service{
		db:        db,
		providers: providers,
	}, nil
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (*models.User, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	u := &models.User{
		Username:     input.Username,
		Email:        input.Email,
		PasswordHash: string(hashedPassword),
	}

	if err := s.db.WithContext(ctx).Create(u).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, ErrUserAlreadyExists
		}
		if err != nil && (err.Error() == "ERROR: duplicate key value violates unique constraint" || err.Error() == "UNIQUE constraint failed") {
			return nil, ErrUserAlreadyExists
		}
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return u, nil
}

type LoginInput struct {
	Username string
	Password string
}

func (s *Service) Login(ctx context.Context, input LoginInput) (*models.User, error) {
	var u models.User
	if err := s.db.WithContext(ctx).Where("username = ?", input.Username).First(&u).Error; err != nil {
		return nil, ErrInvalidCredentials
	}

	if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(input.Password)) != nil {
		return nil, ErrInvalidCredentials
	}

	return &u, nil
}

func (s *Service) OAuthLoginURL(providerName string, state string) (string, error) {
	provider, exists := s.providers[providerName]
	if !exists {
		return "", ErrUnsupportedOAuth
	}

	return provider.GetLoginURL(state), nil
}

func (s *Service) OAuthLogin(ctx context.Context, providerName string, code string) (*models.User, error) {
	provider, exists := s.providers[providerName]
	if !exists {
		return nil, ErrUnsupportedOAuth
	}

	oauthUser, err := provider.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange failed: %w", err)
	}

	var user models.User

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account models.UserOauthAccount
		err := tx.Where("provider = ? AND provider_account_id = ?", providerName, oauthUser.ID).First(&account).Error

		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Create new user and account
				user = models.User{
					Username: oauthUser.Name, // Or generate a unique one
					Email:    oauthUser.Email,
				}
				if err := tx.Create(&user).Error; err != nil {
					return fmt.Errorf("failed to create user: %w", err)
				}

				account = models.UserOauthAccount{
					UserID:            user.ID,
					Provider:          providerName,
					ProviderAccountID: oauthUser.ID,
					Email:             oauthUser.Email,
				}
				if err := tx.Create(&account).Error; err != nil {
					return fmt.Errorf("failed to create oauth account: %w", err)
				}
				return nil
			}
			return fmt.Errorf("database error: %w", err)
		}

		// Account exists, fetch user
		if err := tx.First(&user, "id = ?", account.UserID).Error; err != nil {
			return fmt.Errorf("failed to fetch user: %w", err)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (s *Service) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	var user models.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}
