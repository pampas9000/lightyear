package handlers

import (
	"context"
	"errors"

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
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, err.Error())
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
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, err.Error())
	}

	return h.setSessionAndRespond(c, u)
}

func (h *AuthHandler) OAuthLogin(c fiber.Ctx) error {
	provider := c.Params("provider")
	// Generate a random state. For production, store it in session and verify it later
	state := uuid.New().String()

	sess, err := h.store.Get(c)
	if err == nil {
		sess.Set("oauth_state", state)
		sess.Save()
	}

	url, err := h.service.OAuthLoginURL(provider, state)
	if err != nil {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Unsupported OAuth provider.")
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Success.", fiber.Map{"url": url})
}

func (h *AuthHandler) OAuthCallback(c fiber.Ctx) error {
	provider := c.Params("provider")
	state := c.Query("state")
	code := c.Query("code")

	sess, err := h.store.Get(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Session error.")
	}

	savedState := sess.Get("oauth_state")
	if savedState == nil || savedState.(string) != state {
		return response.RespondError(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid state.")
	}
	sess.Delete("oauth_state") // Consume state

	u, err := h.service.OAuthLogin(c.Context(), provider, code)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).SendString("OAuth login failed.")
	}

	sess.Set("user_id", u.ID.String())
	if err := sess.Save(); err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to save session.")
	}

	// For browser OAuth flow, redirect to the frontend index explicitly
	return c.Redirect().To(h.frontendURL)
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

// Set session and
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
		"token": sess.ID(),
		"user":  user,
	})
}
