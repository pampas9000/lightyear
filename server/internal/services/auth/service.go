package auth

import (
	"context"
	"errors"
	"fmt"

	"transcoder/server/internal/config"
	"transcoder/server/internal/models"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserAlreadyExists  = errors.New("user already exists")
	ErrUnsupportedOAuth   = errors.New("unsupported oauth provider")
	// ErrOAuthEmailTaken: the provider account is not bound yet and its email
	// belongs to an existing user. Accounts are never merged by email.
	ErrOAuthEmailTaken = errors.New("email already registered")
	// ErrOAuthEmailUnverified: the provider did not return a verified email.
	ErrOAuthEmailUnverified = errors.New("oauth email not verified")
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

// NewServiceWithProviders builds a Service with the given OAuth providers
// instead of the configured ones. Tests use it to stub a provider.
func NewServiceWithProviders(db *gorm.DB, providers map[string]OAuthProvider) *Service {
	return &Service{db: db, providers: providers}
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

// OAuthLogin exchanges code with the provider and returns the user bound to
// the provider account, creating a new user on first login. It never links a
// provider account to an existing user by email: an unverified address, or a
// password account registered with someone else's address, would otherwise
// hand that account over. An email already in use returns ErrOAuthEmailTaken
// and changes nothing.
func (s *Service) OAuthLogin(ctx context.Context, providerName string, code string) (*models.User, error) {
	provider, exists := s.providers[providerName]
	if !exists {
		return nil, ErrUnsupportedOAuth
	}

	oauthUser, err := provider.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oauth exchange failed: %w", err)
	}
	// Providers reject unverified addresses themselves; this check holds for
	// any provider added later.
	if !oauthUser.Verified || oauthUser.Email == "" {
		return nil, ErrOAuthEmailUnverified
	}
	if oauthUser.ID == "" {
		return nil, errors.New("oauth provider returned no account id")
	}

	var user models.User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account models.UserOauthAccount
		err := tx.Where("provider = ? AND provider_account_id = ?", providerName, oauthUser.ID).First(&account).Error
		if err == nil {
			if err := tx.First(&user, "id = ?", account.UserID).Error; err != nil {
				return fmt.Errorf("failed to fetch user: %w", err)
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("database error: %w", err)
		}

		var taken int64
		if err := tx.Model(&models.User{}).Where("LOWER(email) = LOWER(?)", oauthUser.Email).Count(&taken).Error; err != nil {
			return fmt.Errorf("failed to check existing user email: %w", err)
		}
		if taken > 0 {
			return ErrOAuthEmailTaken
		}

		// Ensure the username is unique.
		username := oauthUser.Name
		if username == "" {
			username = oauthUser.Email
		}
		baseUsername := username
		var tempUser models.User
		suffix := 1
		for {
			err := tx.Where("username = ?", username).First(&tempUser).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					break
				}
				return fmt.Errorf("failed to verify username uniqueness: %w", err)
			}
			username = fmt.Sprintf("%s-%d", baseUsername, suffix)
			suffix++
		}

		user = models.User{
			Username: username,
			Email:    oauthUser.Email,
		}
		if err := tx.Create(&user).Error; err != nil {
			if isUniqueViolation(err) {
				// A concurrent registration took the email.
				return ErrOAuthEmailTaken
			}
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

func isUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == "23505"
	}
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
