package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/url"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/config"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/auth"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/google/uuid"
)

type AuthHandler struct {
	service     *auth.Service
	store       *session.Store
	frontendURL string
}

func NewAuthHandler(db *gorm.DB, store *session.Store, cfg config.Config) *AuthHandler {
	service, err := auth.NewService(context.Background(), db, cfg)
	if err != nil {
		panic(err)
	}

	return &AuthHandler{
		service:     service,
		store:       store,
		frontendURL: cfg.FrontendURL,
	}
}

func (h *AuthHandler) Install(router fiber.Router) {
	router.Post("/register", h.Register)
	router.Post("/login", h.Login)
	router.Get("/auth/:provider", h.OAuthLogin)
	router.Get("/auth/:provider/callback", h.OAuthCallback)
	router.Get("/auth/callback/:provider", h.OAuthCallback) // Support alternative callback URL formats like /api/auth/callback/github
	router.Post("/logout", h.Logout)
}

type RegisterRequest struct {
	Username string `json:"username" validate:"required,min=3,max=20"`
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

func (h *AuthHandler) Register(c fiber.Ctx) error {
	var req RegisterRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body.")
	}

	u, err := h.service.Register(c.Context(), auth.RegisterInput{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
	})

	if err != nil {
		if errors.Is(err, auth.ErrUserAlreadyExists) {
			return response.RespondError(c, fiber.StatusConflict, response.CodeUserExists, "Username or email already exists.")
		}
		return response.RespondInternal(c, "Failed to register.", err)
	}

	return h.setSessionAndRespond(c, u)
}

type LoginRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

func (h *AuthHandler) Login(c fiber.Ctx) error {
	var req LoginRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body.")
	}

	u, err := h.service.Login(c.Context(), auth.LoginInput{
		Username: req.Username,
		Password: req.Password,
	})

	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return response.RespondError(c, fiber.StatusUnauthorized, response.CodeInvalidCreds, "Invalid username or password.")
		}
		return response.RespondInternal(c, "Failed to log in.", err)
	}

	return h.setSessionAndRespond(c, u)
}

func (h *AuthHandler) OAuthLogin(c fiber.Ctx) error {
	provider := c.Params("provider")
	// The callback accepts only the state stored in this session.
	state := uuid.New().String()

	loginURL, err := h.service.OAuthLoginURL(provider, state)
	if err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Unsupported OAuth provider.")
	}

	sess, err := h.store.Get(c)
	if err != nil {
		return response.RespondInternal(c, "Failed to start OAuth login.", err)
	}
	sess.Set("oauth_state", state)
	if err := sess.Save(); err != nil {
		return response.RespondInternal(c, "Failed to start OAuth login.", err)
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Success.", fiber.Map{"url": loginURL})
}

// OAuth callback error codes, sent to the frontend as ?auth_error=<code>.
const (
	OAuthErrorInvalidState    = "invalid_state"
	OAuthErrorEmailTaken      = "email_taken"
	OAuthErrorEmailUnverified = "email_unverified"
	OAuthErrorFailed          = "oauth_failed"
	OAuthErrorSession         = "session_failed"
)

// OAuthCallback finishes a browser OAuth flow. Every outcome is a redirect to
// the frontend; failures carry an auth_error code for the UI to show.
func (h *AuthHandler) OAuthCallback(c fiber.Ctx) error {
	provider := c.Params("provider")
	state := c.Query("state")
	code := c.Query("code")

	sess, err := h.store.Get(c)
	if err != nil {
		slog.Error("OAuth callback: load session", "provider", provider, "error", err)
		return h.redirectOAuthError(c, OAuthErrorSession)
	}

	savedState, _ := sess.Get("oauth_state").(string)
	if savedState == "" || savedState != state {
		return h.redirectOAuthError(c, OAuthErrorInvalidState)
	}
	// Consume the state so it cannot be replayed, whatever happens next.
	sess.Delete("oauth_state")
	if err := sess.Save(); err != nil {
		slog.Error("OAuth callback: save session", "provider", provider, "error", err)
		return h.redirectOAuthError(c, OAuthErrorSession)
	}

	u, err := h.service.OAuthLogin(c.Context(), provider, code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthEmailTaken):
			return h.redirectOAuthError(c, OAuthErrorEmailTaken)
		case errors.Is(err, auth.ErrOAuthEmailUnverified):
			return h.redirectOAuthError(c, OAuthErrorEmailUnverified)
		default:
			slog.Error("OAuth login failed", "provider", provider, "error", err)
			return h.redirectOAuthError(c, OAuthErrorFailed)
		}
	}

	sess.Set("user_id", u.ID.String())
	if err := sess.Save(); err != nil {
		slog.Error("OAuth callback: save session", "provider", provider, "error", err)
		return h.redirectOAuthError(c, OAuthErrorSession)
	}

	return c.Redirect().To(h.frontendURL)
}

func (h *AuthHandler) redirectOAuthError(c fiber.Ctx, code string) error {
	target, err := url.Parse(h.frontendURL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Invalid FRONTEND_URL.")
	}
	q := target.Query()
	q.Set("auth_error", code)
	target.RawQuery = q.Encode()
	return c.Redirect().To(target.String())
}

func (h *AuthHandler) Logout(c fiber.Ctx) error {
	sess, err := h.store.Get(c)
	if err == nil {
		sess.Destroy()
	}
	return response.RespondEmptySuccess(c, fiber.StatusOK, response.CodeOK, "Logged out successfully.")
}

func (h *AuthHandler) Me(c fiber.Ctx) error {
	userID := c.Locals("user_id")
	if userID == nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Not authenticated.")
	}

	idStr, ok := userID.(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID format.")
	}

	user, err := h.service.GetUserByID(c.Context(), idStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User not found.")
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Success.", user)
}

// setSessionAndRespond logs the user in. The session ID travels only in the
// HttpOnly cookie, never in the body, so page scripts cannot read it.
func (h *AuthHandler) setSessionAndRespond(c fiber.Ctx, user *models.User) error {
	sess, err := h.store.Get(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create session.")
	}

	sess.Set("user_id", user.ID.String())
	if err := sess.Save(); err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to save session.")
	}

	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Success.", fiber.Map{
		"user": user,
	})
}
