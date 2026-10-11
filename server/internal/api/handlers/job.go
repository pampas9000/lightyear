package handlers

import (
	"errors"
	"log"

	"transcoder/server/internal/api/response"
	"transcoder/server/internal/services/job"
	"transcoder/server/internal/transcode"

	"gorm.io/gorm"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Handler owns the HTTP transport for job-related APIs.
type Handler struct {
	service *job.Service
}

// NewHandler builds a jobs handler with its application dependencies.
func NewHandler(db *gorm.DB, kv *redis.Client) *Handler {
	return &Handler{
		service: job.NewService(db, kv),
	}
}

// Install registers all job routes under the provided router.
func (h *Handler) Install(router fiber.Router) {
	// router.Post("/jobs", h.CreateJob) // Removed in favor of Task creation
	router.Get("/jobs/:id", h.GetJob)
}

type CreateJobRequest struct {
	InputPath    string           `json:"inputPath"`
	OutputPath   string           `json:"outputPath"`
	TargetFormat string           `json:"targetFormat"`
	Params       transcode.Params `json:"params,omitempty"`
}

func (h *Handler) CreateJob(c fiber.Ctx) error {
	var req CreateJobRequest
	if err := c.Bind().Body(&req); err != nil {
		return response.RespondErrorWithDetails(
			c,
			fiber.StatusBadRequest,
			response.CodeBodyInvalid,
			"Invalid request body.",
			map[string]any{"reason": err.Error()},
		)
	}

	userIdStr, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found in context.")
	}

	ownerID, err := uuid.Parse(userIdStr)
	if err != nil {
		return response.RespondError(c, fiber.StatusInternalServerError, response.CodeInternal, "Invalid user ID in context.")
	}

	createdJob, err := h.service.CreateJob(c.Context(), job.CreateJobInput{
		OwnerID:      ownerID,
		InputPath:    req.InputPath,
		OutputPath:   req.OutputPath,
		TargetFormat: req.TargetFormat,
		Params:       req.Params,
	})
	if err == nil {
		return response.RespondSuccess(
			c,
			fiber.StatusCreated,
			response.CodeJobCreated,
			"Job created successfully.",
			createdJob,
		)
	}

	switch {
	case errors.Is(err, job.ErrInputPathRequired):
		return response.RespondErrorWithDetails(
			c,
			fiber.StatusBadRequest,
			response.CodeParamRequired,
			"inputPath is required.",
			response.ValidationDetail{Field: "inputPath", Reason: "required"},
		)
	case errors.Is(err, job.ErrOutputPathRequired):
		return response.RespondErrorWithDetails(
			c,
			fiber.StatusBadRequest,
			response.CodeParamRequired,
			"outputPath is required.",
			response.ValidationDetail{Field: "outputPath", Reason: "required"},
		)
	case errors.Is(err, job.ErrTargetFormatRequired):
		return response.RespondErrorWithDetails(
			c,
			fiber.StatusBadRequest,
			response.CodeParamRequired,
			"targetFormat is required.",
			response.ValidationDetail{Field: "targetFormat", Reason: "required"},
		)
	}

	var queueErr *job.QueueEnqueueError
	if errors.As(err, &queueErr) && createdJob != nil {
		log.Printf("job %s created but queue handoff failed: %v", queueErr.JobID, queueErr.Err)

		return c.Status(fiber.StatusCreated).JSON(response.Response[any]{
			Success: true,
			Code:    response.CodeJobCreatedPartial.String(),
			Message: "Job created, but queue handoff failed.",
			Data:    createdJob,
			Details: map[string]any{
				"warning_code": response.CodeQueueEnqueueFail.String(),
				"reason":       queueErr.Err.Error(),
			},
		})
	}

	return response.RespondErrorWithDetails(
		c,
		fiber.StatusInternalServerError,
		response.CodeInternal,
		"Failed to create job.",
		map[string]any{"reason": err.Error()},
	)
}

func (h *Handler) GetJob(c fiber.Ctx) error {
	idStr := c.Params("id")
	parsedID, err := uuid.Parse(idStr)
	if err != nil {
		return response.RespondErrorWithDetails(
			c,
			fiber.StatusBadRequest,
			response.CodeParamInvalid,
			"Invalid job id format.",
			response.ValidationDetail{Field: "id", Reason: "must be a valid UUID"},
		)
	}

	userID, ok := c.Locals("user_id").(string)
	if !ok {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "User ID not found.")
	}
	ownerID, err := uuid.Parse(userID)
	if err != nil {
		return response.RespondError(c, fiber.StatusUnauthorized, response.CodeUnauthorized, "Invalid user ID.")
	}
	result, err := h.service.GetJob(c.Context(), ownerID, parsedID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return response.RespondError(
				c,
				fiber.StatusNotFound,
				response.CodeJobNotFound,
				"Job not found.",
			)
		}

		return response.RespondErrorWithDetails(
			c,
			fiber.StatusInternalServerError,
			response.CodeInternal,
			"Failed to fetch job.",
			map[string]any{"reason": err.Error()},
		)
	}

	return response.RespondSuccess(
		c,
		fiber.StatusOK,
		response.CodeJobFetched,
		"Job fetched successfully.",
		result,
	)
}
