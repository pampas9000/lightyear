package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"transcoder/server/internal/imageoptions"
	"transcoder/server/pkg/util"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Base struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (b *Base) BeforeCreate(tx *gorm.DB) (err error) {
	if b.ID == uuid.Nil {
		b.ID = util.NewUuidV7()
	}
	return
}

// JSON is a raw jsonb column.
type JSON json.RawMessage

func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return json.RawMessage(j).MarshalJSON()
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	if !json.Valid(b) {
		return errors.New("invalid JSON")
	}
	*j = append((*j)[:0], b...)
	return nil
}

func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

func (j *JSON) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*j = nil
		return nil
	case string:
		return j.UnmarshalJSON([]byte(v))
	case []byte:
		return j.UnmarshalJSON(v)
	default:
		return fmt.Errorf("scan JSON: unsupported %T", value)
	}
}

type User struct {
	Base
	Username     string     `gorm:"uniqueIndex;not null" json:"username"`
	Email        string     `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string     `json:"-"`
	Tasks        []Task     `gorm:"foreignKey:OwnerID" json:"tasks,omitempty"`
	Workflows    []Workflow `gorm:"foreignKey:OwnerID" json:"workflows,omitempty"`
	Files        []File     `gorm:"foreignKey:OwnerID" json:"files,omitempty"`
}

type UserOauthAccount struct {
	Base
	UserID            uuid.UUID `gorm:"not null;uniqueIndex:idx_provider_account" json:"user_id"`
	User              User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Provider          string    `gorm:"not null;uniqueIndex:idx_provider_account" json:"provider"`
	ProviderAccountID string    `gorm:"not null;uniqueIndex:idx_provider_account" json:"provider_account_id"`
	Email             string    `json:"email"`
}

type File struct {
	Base
	Name            string     `gorm:"not null" json:"name"`
	Path            string     `gorm:"not null" json:"path"`
	Size            int64      `json:"size"`
	MimeType        string     `json:"mime_type"`
	Status          string     `gorm:"type:varchar(20);default:'UPLOADING';index:idx_files_owner_status_uploaded,priority:2" json:"status"`
	UploadID        string     `gorm:"type:text" json:"upload_id,omitempty"`
	UploadExpiresAt *time.Time `json:"upload_expires_at,omitempty"`
	UploadedAt      *time.Time `gorm:"index:idx_files_owner_status_uploaded,priority:3" json:"uploaded_at,omitempty"`
	Hash            string     `gorm:"type:varchar(64);index:idx_files_hash" json:"hash,omitempty"`
	Width           int        `json:"width"`
	Height          int        `json:"height"`
	OwnerID         uuid.UUID  `gorm:"not null;index:idx_files_owner_status_uploaded,priority:1" json:"owner_id"`
	Owner           User       `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
}

type DraftFile struct {
	Base
	FileID  uuid.UUID `gorm:"type:uuid;not null;uniqueIndex" json:"file_id"`
	File    File      `gorm:"foreignKey:FileID" json:"file,omitempty"`
	OwnerID uuid.UUID `gorm:"type:uuid;not null;index" json:"owner_id"`
	Owner   User      `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
}

type JobStatus string

const (
	JobPending    JobStatus = "PENDING"
	JobProcessing JobStatus = "PROCESSING"
	JobCompleted  JobStatus = "COMPLETED"
	JobFailed     JobStatus = "FAILED"
	JobCancelled  JobStatus = "CANCELLED"
)

type TaskStatus string

const (
	TaskPending         TaskStatus = "PENDING"
	TaskProcessing      TaskStatus = "PROCESSING"
	TaskCompleted       TaskStatus = "COMPLETED"
	TaskFailed          TaskStatus = "FAILED"
	TaskPartiallyFailed TaskStatus = "PARTIALLY_FAILED"
	TaskCancelled       TaskStatus = "CANCELLED"
)

// OpImageConvert is the only operation today; new operations add a value and
// an executor queue, not new tables.
const OpImageConvert = "image.convert"

// Job is one unit of work: one source file turned into one output.
type Job struct {
	Base
	OwnerID      uuid.UUID            `gorm:"type:uuid;not null;index" json:"owner_id"`
	TaskID       uuid.UUID            `gorm:"type:uuid;not null;index" json:"task_id"`
	WorkflowID   *uuid.UUID           `gorm:"type:uuid" json:"workflow_id,omitempty"`
	Operation    string               `gorm:"type:varchar(32);not null" json:"operation"`
	Options      imageoptions.Options `gorm:"type:jsonb;not null" json:"options"`
	SourceFileID uuid.UUID            `gorm:"type:uuid;not null;index" json:"source_file_id"`
	SourceFile   *File                `gorm:"foreignKey:SourceFileID" json:"source_file,omitempty"`
	OutputFileID *uuid.UUID           `gorm:"type:uuid;index" json:"output_file_id,omitempty"`
	OutputFile   *File                `gorm:"foreignKey:OutputFileID" json:"output_file,omitempty"`
	// ArtifactKey identifies (source, canonical options) for reusing outputs.
	ArtifactKey      string     `gorm:"type:varchar(64);index" json:"-"`
	Status           JobStatus  `gorm:"type:job_status;not null;default:'PENDING';index:idx_jobs_due,priority:1" json:"status"`
	NextRunAt        *time.Time `gorm:"index:idx_jobs_due,priority:2" json:"next_run_at,omitempty"`
	Progress         int        `gorm:"not null;default:0" json:"progress"`
	Attempts         int        `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts      int        `gorm:"not null;default:3" json:"max_attempts"`
	CurrentAttemptID *uuid.UUID `gorm:"type:uuid" json:"current_attempt_id,omitempty"`
	LeaseExpiresAt   *time.Time `gorm:"index" json:"lease_expires_at,omitempty"`
	ErrorCode        string     `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
}

type AttemptStatus string

const (
	AttemptQueued    AttemptStatus = "QUEUED"
	AttemptRunning   AttemptStatus = "RUNNING"
	AttemptCompleted AttemptStatus = "COMPLETED"
	AttemptFailed    AttemptStatus = "FAILED"
	AttemptExpired   AttemptStatus = "EXPIRED"
	AttemptCancelled AttemptStatus = "CANCELLED"
)

// Attempt is one dispatch of a job. Its row is written before the queue
// message, so it doubles as the outbox: a lost message is re-sent from Payload.
type Attempt struct {
	Base
	JobID        uuid.UUID     `gorm:"type:uuid;not null;index" json:"job_id"`
	Status       AttemptStatus `gorm:"type:varchar(20);not null" json:"status"`
	Queue        string        `gorm:"not null" json:"-"`
	Payload      JSON          `gorm:"type:jsonb;not null" json:"-"`
	OutputKey    string        `gorm:"not null" json:"-"`
	DispatchedAt *time.Time    `json:"dispatched_at,omitempty"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	ErrorCode    string        `gorm:"type:varchar(64)" json:"error_code,omitempty"`
}

type Task struct {
	Base
	Status             TaskStatus `gorm:"type:varchar(20);default:'PENDING'" json:"status"`
	OwnerID            uuid.UUID  `gorm:"not null;uniqueIndex:idx_tasks_owner_idempotency,priority:1;column:owner_id" json:"owner_id"`
	Owner              User       `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	Jobs               []Job      `gorm:"foreignKey:TaskID" json:"jobs,omitempty"`
	WorkflowID         *uuid.UUID `json:"workflow_id,omitempty"`
	Workflow           *Workflow  `gorm:"foreignKey:WorkflowID" json:"workflow,omitempty"`
	IdempotencyKey     *string    `gorm:"type:varchar(255);uniqueIndex:idx_tasks_owner_idempotency,priority:2;column:idempotency_key" json:"idempotency_key,omitempty"`
	RequestFingerprint string     `gorm:"type:varchar(64);column:request_fingerprint" json:"request_fingerprint,omitempty"`
}

// Workflow is a saved set of output options.
type Workflow struct {
	Base
	Name    string               `gorm:"not null" json:"name"`
	Options imageoptions.Options `gorm:"type:jsonb;not null" json:"options"`
	OwnerID uuid.UUID            `gorm:"not null" json:"owner_id"`
	Owner   User                 `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
}
