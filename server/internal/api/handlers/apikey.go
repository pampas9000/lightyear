package handlers

import (
	"errors"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/apikey"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// APIKeyHandler lets the web console manage the keys used by /v1. It is
// mounted behind session auth, so an API key cannot mint other keys.
type APIKeyHandler struct {
	service *apikey.Service
}

func NewAPIKeyHandler(service *apikey.Service) *APIKeyHandler {
	return &APIKeyHandler{service: service}
}

func (h *APIKeyHandler) Install(router fiber.Router) {
	router.Get("/api-keys", h.List)
	router.Post("/api-keys", h.Create)
	router.Delete("/api-keys/:id", h.Revoke)
}

type CreateAPIKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (h *APIKeyHandler) Create(c fiber.Ctx) error {
	owner, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}
	var req CreateAPIKeyRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeBodyInvalid, "Invalid request body.", map[string]any{"reason": err.Error()})
	}
	key, plaintext, err := h.service.Create(c.Context(), owner, req.Name, req.Scopes)
	switch {
	case errors.Is(err, apikey.ErrInvalidName):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "name", Reason: "must be 1-64 characters"})
	case errors.Is(err, apikey.ErrInvalidScopes):
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, err.Error(), response.ValidationDetail{Field: "scopes", Reason: "unknown scope"})
	case errors.Is(err, apikey.ErrTooManyKeys):
		return response.RespondError(c, fiber.StatusConflict, response.CodeAPIKeyLimit, err.Error())
	case err != nil:
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to create API key.", map[string]any{"reason": err.Error()})
	}
	return response.RespondSuccess(c, fiber.StatusCreated, response.CodeOK, "API key created. Store it now; it is not shown again.", fiber.Map{
		"api_key": key,
		"key":     plaintext,
	})
}

func (h *APIKeyHandler) List(c fiber.Ctx) error {
	owner, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}
	keys, err := h.service.List(c.Context(), owner)
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to list API keys.", map[string]any{"reason": err.Error()})
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "API keys fetched.", fiber.Map{
		"api_keys": keys,
		"scopes":   apikey.AllScopes,
	})
}

func (h *APIKeyHandler) Revoke(c fiber.Ctx) error {
	owner, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid, "Invalid API key id.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}
	key, err := h.service.Revoke(c.Context(), owner, id)
	if errors.Is(err, apikey.ErrNotFound) {
		return response.RespondError(c, fiber.StatusNotFound, response.CodeAPIKeyNotFound, "API key not found.")
	}
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal, "Failed to revoke API key.", map[string]any{"reason": err.Error()})
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "API key revoked.", key)
}
