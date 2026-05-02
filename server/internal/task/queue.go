package task

import (
	"context"

	"github.com/redis/go-redis/v9"
)

const JobStreamKey = "transcoder:jobs:stream"

func EnqueueJob(ctx context.Context, kv *redis.Client, jobID string) error {
	return kv.XAdd(ctx, &redis.XAddArgs{
		Stream: JobStreamKey,
		Values: map[string]any{
			"job_id": jobID,
		},
	}).Err()
}
