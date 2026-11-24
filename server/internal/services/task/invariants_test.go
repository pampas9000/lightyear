package task

import (
	"testing"

	"github.com/google/uuid"

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

// failed and completedWith are the job columns of a run that ended, for
// fixtures that skip the orchestrator.
var failed = map[string]any{"status": models.JobFailed, "error_code": "processing_failed", "next_run_at": nil, "lease_expires_at": nil}

func completedWith(output uuid.UUID) map[string]any {
	return map[string]any{"status": models.JobCompleted, "progress": 100, "output_file_id": output,
		"resolved_options": gorm.Expr("options"), "next_run_at": nil, "lease_expires_at": nil}
}
