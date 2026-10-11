package models

import (
	"time"

	"transcoder/server/internal/operation"
	"transcoder/server/internal/transcode"
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

type User struct {
	Base
	Username     string     `gorm:"uniqueIndex;not null" json:"username"`
	Email        string     `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string     `json:"-"`
	Jobs         []Job      `gorm:"foreignKey:OwnerID" json:"jobs,omitempty"`
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
	InputToJobs     []Job      `gorm:"foreignKey:InputFileID" json:"input_to_jobs,omitempty"`
	OutputFromJobs  []Job      `gorm:"foreignKey:OutputFileID" json:"output_from_jobs,omitempty"`
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

type Job struct {
	Base
	Status          JobStatus        `gorm:"type:job_status;default:'PENDING';index:idx_jobs_schedule,priority:1" json:"status"`
	Operation       operation.Kind   `gorm:"type:varchar(32);not null;default:'transcode'" json:"operation"`
	Executor        string           `gorm:"type:varchar(64);not null;default:'compute'" json:"executor"`
	OperationParams operation.JSON   `gorm:"type:jsonb" json:"operation_params,omitempty"`
	Generation      int              `gorm:"not null;default:0" json:"generation"`
	NextRunAt       *time.Time       `gorm:"index:idx_jobs_schedule,priority:2" json:"next_run_at,omitempty"`
	LeaseExpiresAt  *time.Time       `gorm:"index" json:"lease_expires_at,omitempty"`
	InputPath       string           `json:"input_path"`
	OutputPath      string           `json:"output_path"`
	TargetFormat    string           `json:"target_format"`
	Params          transcode.Params `gorm:"type:json" json:"params"`
	Progress        int              `gorm:"default:0" json:"progress"`
	ErrorMessage    string           `json:"error_message,omitempty"`
	ErrorCode       string           `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	RetryCount      int              `gorm:"default:0" json:"retry_count"`
	MaxRetries      int              `gorm:"default:3" json:"max_retries"`
	OwnerID         uuid.UUID        `gorm:"not null" json:"owner_id"`
	Owner           User             `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	TaskID          *uuid.UUID       `gorm:"index" json:"task_id,omitempty"`
	Task            *Task            `gorm:"foreignKey:TaskID" json:"task,omitempty"`
	WorkflowID      *uuid.UUID       `json:"workflow_id,omitempty"`
	Workflow        *Workflow        `gorm:"foreignKey:WorkflowID" json:"workflow,omitempty"`
	InputFileID     *uuid.UUID       `gorm:"index:idx_jobs_input_file_id" json:"input_file_id,omitempty"`
	InputFile       *File            `gorm:"foreignKey:InputFileID" json:"input_file,omitempty"`
	OutputFileID    *uuid.UUID       `json:"output_file_id,omitempty"`
	OutputFile      *File            `gorm:"foreignKey:OutputFileID" json:"output_file,omitempty"`
	AttemptID       *uuid.UUID       `gorm:"type:uuid" json:"attempt_id,omitempty"`
	Inputs          []JobInput       `gorm:"foreignKey:JobID" json:"inputs,omitempty"`
	Outputs         []JobOutput      `gorm:"foreignKey:JobID" json:"outputs,omitempty"`
}

type JobInput struct {
	Base
	JobID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_job_input_position,priority:1" json:"job_id"`
	FileID   uuid.UUID `gorm:"type:uuid;not null;index" json:"file_id"`
	File     File      `gorm:"foreignKey:FileID" json:"file,omitempty"`
	Role     string    `gorm:"type:varchar(20);not null" json:"role"`
	Position int       `gorm:"not null;uniqueIndex:idx_job_input_position,priority:2" json:"position"`
	Path     string    `gorm:"not null" json:"path"`
}

type JobOutput struct {
	Base
	JobID    uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_job_output_position,priority:1" json:"job_id"`
	FileID   uuid.UUID `gorm:"type:uuid;not null;uniqueIndex" json:"file_id"`
	File     File      `gorm:"foreignKey:FileID" json:"file,omitempty"`
	Role     string    `gorm:"type:varchar(20);not null;default:'result'" json:"role"`
	Position int       `gorm:"not null;uniqueIndex:idx_job_output_position,priority:2" json:"position"`
	// Path is the logical output key; only File.Path is published to readers.
	Path string `gorm:"not null" json:"-"`
}

type JobAttempt struct {
	Base
	JobID           uuid.UUID      `gorm:"type:uuid;not null;index" json:"job_id"`
	Generation      int            `gorm:"not null" json:"generation"`
	Executor        string         `gorm:"type:varchar(64);not null" json:"executor"`
	Status          string         `gorm:"type:varchar(20);not null;default:'QUEUED'" json:"status"`
	OutputManifest  operation.JSON `gorm:"type:jsonb;not null" json:"output_manifest"`
	Stream          string         `gorm:"not null" json:"-"`
	DispatchPayload operation.JSON `gorm:"type:jsonb;not null" json:"-"`
	FinishedAt      *time.Time     `json:"finished_at,omitempty"`
	ErrorCode       string         `json:"error_code,omitempty"`
}

// OutboxEvent is committed with its business mutation and relayed at least once.
type OutboxEvent struct {
	Base
	Kind        string         `gorm:"type:varchar(20);not null" json:"kind"`
	Destination string         `gorm:"not null" json:"destination"`
	Payload     operation.JSON `gorm:"type:jsonb;not null" json:"payload"`
	AvailableAt time.Time      `gorm:"not null;index:idx_outbox_pending,priority:2" json:"available_at"`
	PublishedAt *time.Time     `gorm:"index:idx_outbox_pending,priority:1" json:"published_at,omitempty"`
	RetryCount  int            `gorm:"not null;default:0" json:"retry_count"`
	LastError   string         `json:"last_error,omitempty"`
}

// ProviderInvocation survives worker attempts. Unknown submissions must be reconciled.
type ProviderInvocation struct {
	Base
	JobID          uuid.UUID      `gorm:"type:uuid;not null;uniqueIndex:idx_invocation_generation,priority:1" json:"job_id"`
	Generation     int            `gorm:"not null;uniqueIndex:idx_invocation_generation,priority:2" json:"generation"`
	Provider       string         `gorm:"type:varchar(64);not null" json:"provider"`
	IdempotencyKey string         `gorm:"not null;uniqueIndex" json:"idempotency_key"`
	Phase          string         `gorm:"type:varchar(20);not null;default:'PREPARED'" json:"phase"`
	RemoteTaskID   string         `json:"remote_task_id,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	NextPollAt     *time.Time     `gorm:"index" json:"next_poll_at,omitempty"`
	Usage          operation.JSON `gorm:"type:jsonb" json:"usage,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
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

type Workflow struct {
	Base
	Name         string           `gorm:"not null" json:"name"`
	TargetFormat string           `gorm:"not null" json:"target_format"`
	Params       transcode.Params `gorm:"type:json" json:"params"`
	OwnerID      uuid.UUID        `gorm:"not null" json:"owner_id"`
	Owner        User             `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	Jobs         []Job            `gorm:"foreignKey:WorkflowID" json:"jobs,omitempty"`
	Tasks        []Task           `gorm:"foreignKey:WorkflowID" json:"tasks,omitempty"`
}
