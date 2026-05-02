package util

import "github.com/google/uuid"

// NewUuidV7 returns a new UUIDv7.
// uuid.newV7 can return an error, so we use Must
// UUIDv7 is a time-ordered UUID, which is useful for database primary keys
// and time-series data
func NewUuidV7() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
