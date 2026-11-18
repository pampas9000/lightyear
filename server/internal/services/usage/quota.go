package usage

import (
	"context"
	"errors"
	"time"

	"transcoder/server/internal/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Limits are a user's quotas; 0 is unlimited.
type Limits struct {
	StorageBytes int64
	MonthlyUnits int64
}

// Quotas checks work and uploads against each user's limits. A nil *Quotas
// checks nothing.
type Quotas struct {
	db       *gorm.DB
	defaults Limits
	now      func() time.Time
}

func NewQuotas(db *gorm.DB, defaults Limits) *Quotas {
	return &Quotas{db: db, defaults: defaults, now: time.Now}
}

// LimitsFor applies the owner's overrides to the global limits.
func (q *Quotas) LimitsFor(tx *gorm.DB, owner uuid.UUID) (Limits, error) {
	var u models.User
	err := tx.Select("storage_quota_bytes", "monthly_unit_quota").Take(&u, "id = ?", owner).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return Limits{}, err
	}
	l := q.defaults
	if u.StorageQuotaBytes != nil {
		l.StorageBytes = max(*u.StorageQuotaBytes, 0)
	}
	if u.MonthlyUnitQuota != nil {
		l.MonthlyUnits = max(*u.MonthlyUnitQuota, 0)
	}
	return l, nil
}

// lockOwner serializes quota checks of one owner until tx ends, so two
// concurrent requests cannot both fit under the same remaining quota.
func lockOwner(tx *gorm.DB, owner uuid.UUID) error {
	return tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('lightyear:quota:' || ?, 0))`, owner.String()).Error
}

// CheckWork is called in the transaction that accepts work (creating or
// retrying a task), after the new PENDING jobs are written, with requested
// the units they reserve (jobs completed by reuse reserve none). It rejects
// the work when this month's settled units plus every unfinished job's
// reservation exceed the monthly quota, or when stored files already fill
// the storage quota. Work that reserves nothing is always accepted.
func (q *Quotas) CheckWork(tx *gorm.DB, owner uuid.UUID, requested int64) error {
	if q == nil || requested <= 0 {
		return nil
	}
	l, err := q.LimitsFor(tx, owner)
	if err != nil || (l.MonthlyUnits == 0 && l.StorageBytes == 0) {
		return err
	}
	if err := lockOwner(tx, owner); err != nil {
		return err
	}
	if l.MonthlyUnits > 0 {
		used, err := unitsSince(tx, owner, MonthStart(q.now()))
		if err != nil {
			return err
		}
		all, err := reserved(tx, owner)
		if err != nil {
			return err
		}
		if used+all > l.MonthlyUnits {
			return reject(owner, &QuotaError{Quota: QuotaMonthlyUnits, Limit: l.MonthlyUnits,
				Used: used, Reserved: max(all-requested, 0), Requested: requested})
		}
	}
	if l.StorageBytes > 0 {
		// Outputs are stored as jobs finish, so their size is unknown here:
		// the storage quota is soft for work, which is refused only once
		// stored files already fill it.
		stored, err := StorageUsed(tx, owner)
		if err != nil {
			return err
		}
		if stored >= l.StorageBytes {
			return reject(owner, &QuotaError{Quota: QuotaStorage, Limit: l.StorageBytes, Used: stored})
		}
	}
	return nil
}

// CheckUpload rejects an upload of size bytes that would take the owner's
// stored files past the storage quota.
func (q *Quotas) CheckUpload(ctx context.Context, owner uuid.UUID, size int64) error {
	if q == nil {
		return nil
	}
	db := q.db.WithContext(ctx)
	l, err := q.LimitsFor(db, owner)
	if err != nil || l.StorageBytes == 0 {
		return err
	}
	stored, err := StorageUsed(db, owner)
	if err != nil {
		return err
	}
	if stored+size > l.StorageBytes {
		return reject(owner, &QuotaError{Quota: QuotaStorage, Limit: l.StorageBytes, Used: stored, Requested: size})
	}
	return nil
}
