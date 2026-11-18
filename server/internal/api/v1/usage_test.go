package v1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/usage"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// withQuotas checks tasks, uploads and GET /v1/usage against limits.
func withQuotas(limits usage.Limits) func(*Handler, *gorm.DB) {
	return func(h *Handler, db *gorm.DB) {
		q := usage.NewQuotas(db, limits)
		h.tasks.WithQuotas(q)
		h.uploads.WithQuotas(q)
		h.quotas = q
	}
}

// fakeLimiter allows the first n calls per key.
type fakeLimiter struct {
	n     int
	calls map[string]int
	err   error
}

func (l *fakeLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	if l.err != nil {
		return false, 0, l.err
	}
	if l.calls == nil {
		l.calls = map[string]int{}
	}
	l.calls[key]++
	return l.calls[key] <= l.n, 1500 * time.Millisecond, nil
}

func TestQuotaExceededIsForbiddenWithDetails(t *testing.T) {
	e := setup(t, withQuotas(usage.Limits{MonthlyUnits: 1, StorageBytes: 1500}))
	key := e.key(apikey.ScopeTasksWrite)
	file := e.uploaded("a.png") // 1000 bytes

	res, env := e.do("POST", "/v1/tasks", key, taskBody(file.ID, 70))
	if res.StatusCode != 202 {
		t.Fatalf("first task: %d %s", res.StatusCode, env.Message)
	}
	res, env = e.do("POST", "/v1/tasks", key, taskBody(file.ID, 71))
	if res.StatusCode != 403 || env.Code != "error.quota.exceeded" {
		t.Fatalf("want 403 error.quota.exceeded, got %d %s", res.StatusCode, env.Code)
	}
	var d struct {
		Quota                            string
		Limit, Used, Reserved, Requested int64
	}
	if err := json.Unmarshal(env.Details, &d); err != nil {
		t.Fatal(err)
	}
	if d.Quota != "monthly_units" || d.Limit != 1 || d.Used != 0 || d.Reserved != 1 || d.Requested != 1 {
		t.Fatalf("unexpected details: %s", env.Details)
	}

	res, env = e.do("POST", "/v1/uploads", key, `{"filename":"b.png","size":600}`)
	if res.StatusCode != 403 || env.Code != "error.quota.exceeded" || !json.Valid(env.Details) {
		t.Fatalf("want a storage quota error, got %d %s", res.StatusCode, env.Code)
	}
	if err := json.Unmarshal(env.Details, &d); err != nil || d.Quota != "storage" || d.Used != 1000 || d.Requested != 600 {
		t.Fatalf("unexpected details: %s", env.Details)
	}
	if res, _ := e.do("POST", "/v1/uploads", key, `{"filename":"b.png","size":500}`); res.StatusCode != 201 && res.StatusCode != 200 {
		t.Fatalf("an upload within the quota must be accepted, got %d", res.StatusCode)
	}
}

func TestGetUsage(t *testing.T) {
	e := setup(t, withQuotas(usage.Limits{MonthlyUnits: 100}))
	read := e.key(apikey.ScopeTasksRead)
	rec := models.UsageRecord{OwnerID: e.owner, Kind: models.UsageProcess, AttemptID: new(uuid.New()),
		JobID: uuid.New(), TaskID: uuid.New(), Units: 6}
	if err := e.db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	res, env := e.do("GET", "/v1/usage", read, "")
	if res.StatusCode != 200 {
		t.Fatalf("GET /v1/usage: %d %s", res.StatusCode, env.Message)
	}
	var s usage.Summary
	if err := json.Unmarshal(env.Data, &s); err != nil {
		t.Fatal(err)
	}
	if s.Month != time.Now().UTC().Format("2006-01") || s.Units.Used != 6 || s.Units.Limit == nil ||
		*s.Units.Limit != 100 || s.Storage.LimitBytes != nil || len(s.Daily) != 1 {
		t.Fatalf("unexpected usage: %s", env.Data)
	}
	if res, env := e.do("GET", "/v1/usage?month=2026-13", read, ""); res.StatusCode != 400 || env.Code != "error.param.invalid" {
		t.Fatalf("invalid month: %d %s", res.StatusCode, env.Code)
	}
	res, env = e.do("GET", "/v1/usage?month=2020-01", read, "")
	if err := json.Unmarshal(env.Data, &s); res.StatusCode != 200 || err != nil || s.Units.Used != 0 || s.Month != "2020-01" {
		t.Fatalf("past month: %d %s", res.StatusCode, env.Data)
	}
}

func TestWriteRequestsAreRateLimitedPerUser(t *testing.T) {
	limiter := &fakeLimiter{n: 1}
	e := setup(t, func(h *Handler, _ *gorm.DB) { h.limiter = limiter })
	key, other := e.key(apikey.ScopeTasksWrite, apikey.ScopeTasksRead), e.key(apikey.ScopeTasksWrite)
	file := e.uploaded("a.png")

	if res, _ := e.do("POST", "/v1/tasks", key, taskBody(file.ID, 70)); res.StatusCode != 202 {
		t.Fatalf("first request: %d", res.StatusCode)
	}
	// Another key of the same user shares the bucket.
	res, env := e.do("POST", "/v1/tasks", other, taskBody(file.ID, 71))
	if res.StatusCode != 429 || env.Code != "error.rate_limit.exceeded" || res.Header.Get("Retry-After") != "2" {
		t.Fatalf("want 429 with Retry-After 2, got %d %s %q", res.StatusCode, env.Code, res.Header.Get("Retry-After"))
	}
	// Reads and the later steps of an upload are not limited.
	if res, _ := e.do("GET", "/v1/tasks", key, ""); res.StatusCode != 200 {
		t.Fatalf("reads must not be limited: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/v1/uploads/"+uuid.NewString()+"/abort", key, ""); res.StatusCode == 429 {
		t.Fatal("upload steps must not be limited")
	}
	if limiter.calls[e.owner.String()] != 2 {
		t.Fatalf("limiter consulted %d times, want 2", limiter.calls[e.owner.String()])
	}
}

func TestRateLimiterFailureLetsRequestsThrough(t *testing.T) {
	e := setup(t, func(h *Handler, _ *gorm.DB) { h.limiter = &fakeLimiter{err: errors.New("redis down")} })
	key := e.key(apikey.ScopeTasksWrite)
	file := e.uploaded("a.png")
	if res, _ := e.do("POST", "/v1/tasks", key, taskBody(file.ID, 70)); res.StatusCode != 202 {
		t.Fatalf("want 202 while the limiter is down, got %d", res.StatusCode)
	}
}
