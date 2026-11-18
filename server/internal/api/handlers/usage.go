package handlers

import (
	"time"

	"transcoder/server/internal/api/response"
	v1 "transcoder/server/internal/api/v1"
	"transcoder/server/internal/services/usage"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// UsageHandler serves the console's usage, the same summary as GET /v1/usage.
type UsageHandler struct {
	quotas *usage.Quotas
}

func NewUsageHandler(quotas *usage.Quotas) *UsageHandler {
	return &UsageHandler{quotas: quotas}
}

func (h *UsageHandler) Install(router fiber.Router) {
	router.Get("/usage", h.GetUsage)
}

func (h *UsageHandler) GetUsage(c fiber.Ctx) error {
	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found.")
	}
	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID.")
	}
	month, err := v1.ParseMonth(c.Query("month"), time.Now())
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid,
			"Invalid month.", response.ValidationDetail{Field: "month", Reason: "must be YYYY-MM"})
	}
	s, err := h.quotas.Summary(c.Context(), ownerID, month)
	if err != nil {
		return response.RespondInternal(c, "Failed to fetch usage.", err)
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Usage fetched.", s)
}
