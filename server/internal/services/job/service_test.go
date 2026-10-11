package job

import (
	"errors"
	"testing"

	"transcoder/server/internal/models"
	"transcoder/server/internal/testutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestGetJobIsOwnerScoped(t *testing.T) {
	db := testutil.OpenDatabase(t)
	owner, stranger := uuid.New(), uuid.New()
	j := models.Job{OwnerID: owner, Status: models.JobPending}
	if err := db.Create(&j).Error; err != nil {
		t.Fatal(err)
	}
	s := NewService(db, nil)
	got, err := s.GetJob(t.Context(), owner, j.ID)
	if err != nil || got.ID != j.ID {
		t.Fatalf("owner cannot read own job: %v", err)
	}
	if _, err := s.GetJob(t.Context(), stranger, j.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected not found for another owner, got %v", err)
	}
}
