package usage

import (
	"context"
	"time"

	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Summary is a user's usage in one calendar month (UTC) and their quotas.
type Summary struct {
	Month       string       `json:"month"`
	PeriodStart time.Time    `json:"period_start"`
	PeriodEnd   time.Time    `json:"period_end"`
	Units       UnitUsage    `json:"units"`
	Storage     StorageUsage `json:"storage"`
	Totals      Totals       `json:"totals"`
	Daily       []Day        `json:"daily"`
}

// UnitUsage: Used is settled; Reserved is the estimate held by unfinished
// jobs (current month only); Limit is null when unlimited.
type UnitUsage struct {
	Used     int64  `json:"used"`
	Reserved int64  `json:"reserved"`
	Limit    *int64 `json:"limit"`
}

// StorageUsage is current, whatever the month.
type StorageUsage struct {
	UsedBytes  int64  `json:"used_bytes"`
	LimitBytes *int64 `json:"limit_bytes"`
}

// Totals counts the month's events.
type Totals struct {
	Outputs        int64 `json:"outputs"`
	ReusedOutputs  int64 `json:"reused_outputs"`
	FailedAttempts int64 `json:"failed_attempts"`
	EncodeRounds   int64 `json:"encode_rounds"`
	ComputeMS      int64 `json:"compute_ms"`
	OutputBytes    int64 `json:"output_bytes"`
	OutputPixels   int64 `json:"output_pixels"`
	FetchedBytes   int64 `json:"fetched_bytes"`
}

// Day is one UTC day with any usage.
type Day struct {
	Date           string `json:"date"`
	Units          int64  `json:"units"`
	Outputs        int64  `json:"outputs"`
	ReusedOutputs  int64  `json:"reused_outputs"`
	FailedAttempts int64  `json:"failed_attempts"`
}

const dayColumns = `
	COALESCE(SUM(units), 0) AS units,
	COUNT(*) FILTER (WHERE kind = 'process') AS outputs,
	COUNT(*) FILTER (WHERE kind = 'reuse') AS reused_outputs,
	COUNT(*) FILTER (WHERE kind = 'failed') AS failed_attempts`

// Summary reports the owner's usage in the month containing month.
func (q *Quotas) Summary(ctx context.Context, owner uuid.UUID, month time.Time) (*Summary, error) {
	db := q.db.WithContext(ctx)
	start := MonthStart(month)
	end := start.AddDate(0, 1, 0)
	s := &Summary{Month: start.Format("2006-01"), PeriodStart: start, PeriodEnd: end, Daily: []Day{}}

	l, err := q.LimitsFor(db, owner)
	if err != nil {
		return nil, err
	}
	if l.MonthlyUnits > 0 {
		s.Units.Limit = &l.MonthlyUnits
	}
	if l.StorageBytes > 0 {
		s.Storage.LimitBytes = &l.StorageBytes
	}
	if s.Storage.UsedBytes, err = StorageUsed(db, owner); err != nil {
		return nil, err
	}
	if start.Equal(MonthStart(q.now())) {
		if s.Units.Reserved, err = reserved(db, owner); err != nil {
			return nil, err
		}
	}

	period := db.Model(&models.UsageRecord{}).
		Where("owner_id = ? AND created_at >= ? AND created_at < ?", owner, start, end).
		Session(&gorm.Session{})
	var totals struct {
		Units int64
		Totals
	}
	if err := period.Select(dayColumns + `,
		COALESCE(SUM(encode_rounds), 0) AS encode_rounds,
		COALESCE(SUM(compute_ms), 0) AS compute_ms,
		COALESCE(SUM(output_bytes), 0) AS output_bytes,
		COALESCE(SUM(output_pixels), 0) AS output_pixels,
		COALESCE(SUM(input_bytes) FILTER (WHERE kind = 'fetch'), 0) AS fetched_bytes`).
		Scan(&totals).Error; err != nil {
		return nil, err
	}
	s.Units.Used, s.Totals = totals.Units, totals.Totals

	if err := period.
		Select(`to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS date,` + dayColumns).
		Group("date").Order("date").Scan(&s.Daily).Error; err != nil {
		return nil, err
	}
	return s, nil
}
