// Package dberr classifies database errors by their Postgres error code, not
// their text.
package dberr

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// UniqueViolation is the Postgres SQLSTATE for a unique constraint violation.
const UniqueViolation = "23505"

// IsUniqueViolation reports whether err is a unique constraint violation,
// from pgx (the GORM driver), lib/pq, or GORM's translated ErrDuplicatedKey.
func IsUniqueViolation(err error) bool {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return pgErr.Code == UniqueViolation
	}
	if pqErr, ok := errors.AsType[*pq.Error](err); ok {
		return pqErr.Code == UniqueViolation
	}
	return errors.Is(err, gorm.ErrDuplicatedKey)
}
