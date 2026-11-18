package v1

import (
	"errors"
	"time"

	"transcoder/server/internal/api/response"

	"github.com/gofiber/fiber/v3"
)

var errMonth = errors.New("month must be YYYY-MM")

// ParseMonth reads the optional ?month=YYYY-MM; empty is the current month.
func ParseMonth(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return now, nil
	}
	m, err := time.Parse("2006-01", raw)
	if err != nil {
		return time.Time{}, errMonth
	}
	return m, nil
}

// GetUsage reports the caller's usage in a calendar month (UTC) and quotas.
func (h *Handler) GetUsage(c fiber.Ctx) error {
	month, err := ParseMonth(c.Query("month"), h.now())
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid,
			"Invalid month.", response.ValidationDetail{Field: "month", Reason: "must be YYYY-MM"})
	}
	if h.quotas == nil {
		return internalError(c, "fetch usage", errors.New("usage not configured"))
	}
	s, err := h.quotas.Summary(c.Context(), owner(c), month)
	if err != nil {
		return internalError(c, "fetch usage", err)
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeOK, "Usage fetched.", s)
}
