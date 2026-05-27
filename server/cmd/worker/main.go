package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"transcoder/server/internal/config"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/task"
	taskqueue "transcoder/server/internal/task"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker daemon failed", "error", err)
		os.Exit(1)
	}
}

func setupLogger(cfg config.Config) {
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}

	if cfg.IsProduction() {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func run() error {
	cfg := config.Load()
	setupLogger(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS shutdown signals gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		slog.Info("received shutdown signal, shutting down gracefully", "signal", sig)
		cancel()
	}()

	db, err := openDatabase(ctx, cfg.DB)
	if err != nil {
		return err
	}

	kv, err := openRedis(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer kv.Close()

	// Initialize Consumer Groups
	initConsumerGroups(ctx, kv)

	// Unique consumer name generator
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	consumerName := fmt.Sprintf("orchestrator-%s-%d-%d", hostname, os.Getpid(), rand.Intn(10000))
	slog.Info("starting orchestrator worker loops", "consumer_name", consumerName)

	// Run dispatcher loop
	go runJobDispatcher(ctx, db, kv, consumerName)

	// Run result processor loop
	go runResultProcessor(ctx, db, kv, consumerName)

	// Run stale recovery loop
	go runStaleJobRecovery(ctx, db, kv)

	<-ctx.Done()
	slog.Info("orchestrator daemon stopped")
	return nil
}

func openDatabase(ctx context.Context, cfg config.DatabaseConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.URL), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	slog.Info("postgres connected")
	return db, nil
}

func openRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	kv := redis.NewClient(opt)

	if err := kv.Ping(ctx).Err(); err != nil {
		_ = kv.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	slog.Info("redis connected")
	return kv, nil
}

func initConsumerGroups(ctx context.Context, kv *redis.Client) {
	// Create consumer groups if they don't exist. MKSTREAM ensures stream creation.
	_ = kv.XGroupCreateMkStream(ctx, taskqueue.JobStreamKey, "transcoder:jobs:group", "$").Err()
	_ = kv.XGroupCreateMkStream(ctx, taskqueue.ResultStreamKey, "transcoder:results:group", "$").Err()
}

// Run Job Dispatcher
func runJobDispatcher(ctx context.Context, db *gorm.DB, kv *redis.Client, consumerName string) {
	group := "transcoder:jobs:group"
	stream := taskqueue.JobStreamKey

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// 1. Try to claim stale entries from PEL first
		claimed, _, err := kv.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   stream,
			Group:    group,
			Consumer: consumerName,
			MinIdle:  10 * time.Second,
			Start:    "0-0",
			Count:    1,
		}).Result()

		var entries []redis.XMessage
		if err == nil && len(claimed) > 0 {
			entries = claimed
		} else {
			// 2. Read new messages
			streams, err := kv.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    group,
				Consumer: consumerName,
				Streams:  []string{stream, ">"},
				Count:    1,
				Block:    1 * time.Second,
			}).Result()
			if err != nil {
				if err != redis.Nil {
					slog.Error("XReadGroup jobs failed", "error", err)
					time.Sleep(1 * time.Second)
				}
				continue
			}
			if len(streams) > 0 && len(streams[0].Messages) > 0 {
				entries = streams[0].Messages
			}
		}

		for _, msg := range entries {
			jobIDStr, ok := msg.Values["job_id"].(string)
			if !ok || jobIDStr == "" {
				slog.Warn("skipping empty or invalid job_id in jobs stream", "id", msg.ID)
				kv.XAck(ctx, stream, group, msg.ID)
				continue
			}

			slog.Info("processing job from queue", "job_id", jobIDStr, "msg_id", msg.ID)
			jobID, err := uuid.Parse(jobIDStr)
			if err != nil {
				slog.Error("failed to parse job uuid", "job_id", jobIDStr, "error", err)
				kv.XAck(ctx, stream, group, msg.ID)
				continue
			}

			err = db.Transaction(func(tx *gorm.DB) error {
				var job models.Job
				attemptID := uuid.New()
				res := tx.Clauses(clause.Returning{}).
					Model(&job).
					Where("id = ? AND status = ?", jobID, models.JobPending).
					Updates(map[string]any{
						"status":     models.JobProcessing,
						"attempt_id": attemptID,
						"updated_at": time.Now(),
					})
				if res.Error != nil {
					return res.Error
				}

				if res.RowsAffected == 0 {
					slog.Info("job already claimed or processed, skipping", "job_id", jobIDStr)
					return nil
				}

				if job.OutputFileID != nil {
					if err := tx.Model(&models.File{}).Where("id = ?", *job.OutputFileID).Update("status", "GENERATING").Error; err != nil {
						return fmt.Errorf("update file status: %w", err)
					}
				}

				if job.TaskID != nil {
					var siblingJobs []models.Job
					if err := tx.Where("task_id = ?", *job.TaskID).Find(&siblingJobs).Error; err != nil {
						return fmt.Errorf("fetch sibling jobs: %w", err)
					}
					var statuses []models.JobStatus
					for _, sj := range siblingJobs {
						if sj.ID == job.ID {
							statuses = append(statuses, models.JobProcessing)
						} else {
							statuses = append(statuses, sj.Status)
						}
					}
					newTaskStatus := task.AggregateTaskStatus(statuses)
					if err := tx.Model(&models.Task{}).Where("id = ?", *job.TaskID).Update("status", newTaskStatus).Error; err != nil {
						return fmt.Errorf("update task status: %w", err)
					}
				}

				var inputFormat string
				if job.InputFileID != nil {
					var inputFile models.File
					if err := tx.First(&inputFile, "id = ?", *job.InputFileID).Error; err == nil {
						inputFormat = mimeToFormat(inputFile.MimeType)
					}
				}

				payload := &taskqueue.ComputePayload{
					SchemaVersion: "1.0",
					JobID:         jobID.String(),
					AttemptID:     attemptID.String(),
					InputPath:     job.InputPath,
					InputFormat:   inputFormat,
					OutputPath:    job.OutputPath,
					TargetFormat:  job.TargetFormat,
					Params:        job.Params,
				}
				if err := taskqueue.EnqueueComputeJob(ctx, kv, payload); err != nil {
					return fmt.Errorf("enqueue compute payload: %w", err)
				}

				return nil
			})

			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					slog.Warn("job not found in DB, acking stream entry", "job_id", jobIDStr)
					kv.XAck(ctx, stream, group, msg.ID)
				} else {
					slog.Error("failed to dispatch job", "job_id", jobIDStr, "error", err)
					time.Sleep(1 * time.Second)
				}
				continue
			}

			if err := kv.XAck(ctx, stream, group, msg.ID).Err(); err != nil {
				slog.Error("failed to ACK jobs stream entry", "msg_id", msg.ID, "error", err)
			}
		}
	}
}

func runResultProcessor(ctx context.Context, db *gorm.DB, kv *redis.Client, consumerName string) {
	group := "transcoder:results:group"
	stream := taskqueue.ResultStreamKey

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		claimed, _, err := kv.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   stream,
			Group:    group,
			Consumer: consumerName,
			MinIdle:  10 * time.Second,
			Start:    "0-0",
			Count:    1,
		}).Result()

		var entries []redis.XMessage
		if err == nil && len(claimed) > 0 {
			entries = claimed
		} else {
			streams, err := kv.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    group,
				Consumer: consumerName,
				Streams:  []string{stream, ">"},
				Count:    1,
				Block:    1 * time.Second,
			}).Result()
			if err != nil {
				if err != redis.Nil {
					slog.Error("XReadGroup results failed", "error", err)
					time.Sleep(1 * time.Second)
				}
				continue
			}
			if len(streams) > 0 && len(streams[0].Messages) > 0 {
				entries = streams[0].Messages
			}
		}

		for _, msg := range entries {
			payloadStr, ok := msg.Values["payload"].(string)
			if !ok || payloadStr == "" {
				slog.Warn("skipping empty or invalid payload in results stream", "id", msg.ID)
				kv.XAck(ctx, stream, group, msg.ID)
				continue
			}

			var payload taskqueue.ResultPayload
			if err := json.Unmarshal([]byte(payloadStr), &payload); err != nil {
				slog.Error("failed to unmarshal result payload", "msg_id", msg.ID, "error", err)
				kv.XAck(ctx, stream, group, msg.ID)
				continue
			}

			slog.Info("received transcode result", "job_id", payload.JobID, "attempt_id", payload.AttemptID, "status", payload.Status)
			jobID, err := uuid.Parse(payload.JobID)
			if err != nil {
				slog.Error("failed to parse job uuid in result", "job_id", payload.JobID, "error", err)
				kv.XAck(ctx, stream, group, msg.ID)
				continue
			}

			err = db.Transaction(func(tx *gorm.DB) error {
				var job models.Job
				if err := tx.First(&job, "id = ?", jobID).Error; err != nil {
					return err
				}

				if job.AttemptID == nil || job.AttemptID.String() != payload.AttemptID {
					slog.Warn("ignoring stale or mismatched result attempt", "job_id", payload.JobID, "expected_attempt", job.AttemptID, "received_attempt", payload.AttemptID)
					return nil
				}

				if job.Status == models.JobCompleted || job.Status == models.JobFailed {
					slog.Info("job already in terminal state, skipping DB update", "job_id", payload.JobID, "status", job.Status)
					return nil
				}

				if payload.Status == "COMPLETED" {
					job.Status = models.JobCompleted
					job.Progress = 100
					job.ErrorMessage = ""
				} else if payload.Status == "PROCESSING" {
					job.Status = models.JobProcessing
					if payload.Progress != nil {
						job.Progress = *payload.Progress
					}
					job.UpdatedAt = time.Now()
					if err := tx.Save(&job).Error; err != nil {
						return fmt.Errorf("save job progress: %w", err)
					}
					return nil // Skip sibling check and task status updates for intermediate progress heartbeats
				} else {
					job.Status = models.JobFailed
					job.Progress = 0
					job.ErrorMessage = payload.ErrorMessage
				}

				if err := tx.Save(&job).Error; err != nil {
					return fmt.Errorf("save job: %w", err)
				}

				if job.OutputFileID != nil {
					fileStatus := "FAILED"
					if payload.Status == "COMPLETED" {
						fileStatus = "UPLOADED"
					}
					var size int64
					if sizeVal, ok := payload.Metadata["output_size_bytes"].(float64); ok {
						size = int64(sizeVal)
					}

					var mimeType string
					if mimeVal, ok := payload.Metadata["mime_type"].(string); ok {
						mimeType = mimeVal
					}

					fileUpdate := tx.Model(&models.File{}).Where("id = ?", *job.OutputFileID)
					updates := map[string]any{"status": fileStatus}
					if size > 0 {
						updates["size"] = size
					}
					if mimeType != "" {
						updates["mime_type"] = mimeType
					}
					if err := fileUpdate.Updates(updates).Error; err != nil {
						return fmt.Errorf("update output file: %w", err)
					}
				}

				if job.TaskID != nil {
					var taskRow models.Task
					if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&taskRow, "id = ?", *job.TaskID).Error; err != nil {
						return fmt.Errorf("lock task row: %w", err)
					}

					var siblingJobs []models.Job
					if err := tx.Where("task_id = ?", *job.TaskID).Find(&siblingJobs).Error; err != nil {
						return fmt.Errorf("find sibling jobs: %w", err)
					}

					var statuses []models.JobStatus
					for _, sj := range siblingJobs {
						if sj.ID == job.ID {
							statuses = append(statuses, job.Status)
						} else {
							statuses = append(statuses, sj.Status)
						}
					}
					newTaskStatus := task.AggregateTaskStatus(statuses)
					if err := tx.Model(&taskRow).Update("status", newTaskStatus).Error; err != nil {
						return fmt.Errorf("update task status: %w", err)
					}
				}

				return nil
			})

			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					slog.Warn("job not found in DB for result payload, acking stream entry", "job_id", payload.JobID)
					kv.XAck(ctx, stream, group, msg.ID)
				} else {
					slog.Error("failed to process transcode result", "job_id", payload.JobID, "error", err)
					time.Sleep(1 * time.Second)
				}
				continue
			}

			if err := kv.XAck(ctx, stream, group, msg.ID).Err(); err != nil {
				slog.Error("failed to ACK results stream entry", "msg_id", msg.ID, "error", err)
			}
		}
	}
}

func runStaleJobRecovery(ctx context.Context, db *gorm.DB, kv *redis.Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// 1. Recover PENDING jobs that were never enqueued (or failed initial enqueue)
		pendingThreshold := time.Now().Add(-2 * time.Minute)
		var pendingJobs []models.Job
		if err := db.Where("status = ? AND created_at < ?", models.JobPending, pendingThreshold).Find(&pendingJobs).Error; err == nil {
			for _, job := range pendingJobs {
				slog.Warn("sweeper: found stuck pending job, re-dispatching", "job_id", job.ID, "created_at", job.CreatedAt)
				if err := taskqueue.EnqueueJob(ctx, kv, job.ID.String()); err != nil {
					slog.Error("sweeper: failed to re-enqueue pending job", "job_id", job.ID, "error", err)
				} else {
					slog.Info("sweeper: successfully re-enqueued pending job", "job_id", job.ID)
				}
			}
		} else {
			slog.Error("sweeper: failed to query stuck pending jobs", "error", err)
		}

		// 2. Recover stale PROCESSING jobs (lease expired)
		staleThreshold := time.Now().Add(-5 * time.Minute)
		var staleJobs []models.Job
		err := db.Where("status = ? AND updated_at < ?", models.JobProcessing, staleThreshold).Find(&staleJobs).Error
		if err != nil {
			slog.Error("failed to query stale jobs", "error", err)
			continue
		}

		for _, job := range staleJobs {
			slog.Warn("found stale processing job, scheduling retry attempt", "job_id", job.ID, "updated_at", job.UpdatedAt)

			err := db.Transaction(func(tx *gorm.DB) error {
				var freshJob models.Job
				if err := tx.First(&freshJob, "id = ?", job.ID).Error; err != nil {
					return err
				}
				if freshJob.Status != models.JobProcessing {
					return nil
				}

				newAttemptID := uuid.New()
				freshJob.AttemptID = &newAttemptID
				freshJob.UpdatedAt = time.Now()

				if err := tx.Save(&freshJob).Error; err != nil {
					return err
				}

				var inputFormat string
				if freshJob.InputFileID != nil {
					var inputFile models.File
					if err := tx.First(&inputFile, "id = ?", *freshJob.InputFileID).Error; err == nil {
						inputFormat = mimeToFormat(inputFile.MimeType)
					}
				}

				payload := &taskqueue.ComputePayload{
					SchemaVersion: "1.0",
					JobID:         freshJob.ID.String(),
					AttemptID:     newAttemptID.String(),
					InputPath:     freshJob.InputPath,
					InputFormat:   inputFormat,
					OutputPath:    freshJob.OutputPath,
					TargetFormat:  freshJob.TargetFormat,
					Params:        freshJob.Params,
				}
				return taskqueue.EnqueueComputeJob(ctx, kv, payload)
			})

			if err != nil {
				slog.Error("failed to recover stale job", "job_id", job.ID, "error", err)
			} else {
				slog.Info("successfully recovered stale job with new attempt_id", "job_id", job.ID)
			}
		}
	}
}

func mimeToFormat(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/avif":
		return "avif"
	case "image/jxl":
		return "jxl"
	case "image/heic", "image/heif":
		return "heic"
	case "video/mp4":
		return "mp4"
	case "video/quicktime":
		return "mov"
	case "video/x-matroska":
		return "mkv"
	case "video/x-flv":
		return "flv"
	default:
		return ""
	}
}
