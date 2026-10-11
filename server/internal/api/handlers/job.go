package handlers

import (
	"errors"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/job"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// Handler owns the HTTP transport for job-related APIs.
type Handler struct {
	service *job.Service
}

// NewHandler builds a jobs handler with its application dependencies.
func NewHandler(db *gorm.DB) *Handler {
	return &Handler{service: job.NewService(db)}
}

// Install registers all job routes under the provided router. Jobs are
// created through tasks.
func (h *Handler) Install(router fiber.Router) {
	router.Get("/jobs/:id", h.GetJob)
}

func (h *Handler) GetJob(c fiber.Ctx) error {
	parsedID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return response.RespondErrorWithDetails(c, fiber.StatusBadRequest, response.CodeParamInvalid,
			"Invalid job id format.", response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"})
	}
	ownerID, err := ownerFromContext(c)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found.")
	}
	result, err := h.service.GetJob(c.Context(), ownerID, parsedID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.RespondError(c, fiber.StatusNotFound, response.CodeJobNotFound, "Job not found.")
		}
		return response.RespondErrorWithDetails(c, fiber.StatusInternalServerError, response.CodeInternal,
			"Failed to fetch job.", map[string]any{"reason": err.Error()})
	}
	return response.RespondSuccess(c, fiber.StatusOK, response.CodeJobFetched, "Job fetched successfully.", result)
}
