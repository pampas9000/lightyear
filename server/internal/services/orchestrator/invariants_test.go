package orchestrator_test

import (
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/services/jobstate"
	"transcoder/server/internal/testutil"

	"gorm.io/gorm"
)

// Every test of this package ends with all stored jobs consistent with their
// status, so each flow it drives doubles as a test of the job invariants.
func init() {
	testutil.CheckAtEnd(func(t *testing.T, db *gorm.DB) {
		var jobs []models.Job
		if err := db.Find(&jobs).Error; err != nil {
			t.Errorf("load jobs for the invariant check: %v", err)
			return
		}
		for _, j := range jobs {
			if err := jobstate.Check(j); err != nil {
				t.Errorf("stored job: %v", err)
			}
		}
	})
}
