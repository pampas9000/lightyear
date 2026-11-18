package response

import (
	"errors"

	"transcoder/server/internal/services/usage"

	"github.com/gofiber/fiber/v3"
)

// QuotaDetail is the details of error.quota.exceeded.
type QuotaDetail struct {
	// Quota is "storage" (bytes) or "monthly_units" (encode rounds).
	Quota     string `json:"quota"`
	Limit     int64  `json:"limit"`
	Used      int64  `json:"used"`
	Reserved  int64  `json:"reserved"`
	Requested int64  `json:"requested"`
}

// QuotaExceeded returns the message and details of error.quota.exceeded for
// a usage.QuotaError; ok is false for any other error.
func QuotaExceeded(err error) (message string, detail QuotaDetail, ok bool) {
	var qe *usage.QuotaError
	if !errors.As(err, &qe) {
		return "", QuotaDetail{}, false
	}
	message = "Monthly processing quota exceeded."
	if qe.Quota == usage.QuotaStorage {
		message = "Storage quota exceeded. Delete files or wait for them to expire."
	}
	return message, QuotaDetail{
		Quota: qe.Quota, Limit: qe.Limit, Used: qe.Used, Reserved: qe.Reserved, Requested: qe.Requested,
	}, true
}

// RespondQuotaExceeded answers a usage.QuotaError with 403
// error.quota.exceeded. Retrying does not help until usage drops (files are
// deleted, or the month ends), so it is not a 429.
func RespondQuotaExceeded(c fiber.Ctx, err error) error {
	message, detail, ok := QuotaExceeded(err)
	if !ok {
		return RespondInternal(c, "Failed to check quota.", err)
	}
	return RespondErrorWithDetails(c, fiber.StatusForbidden, CodeQuotaExceeded, message, detail)
}
