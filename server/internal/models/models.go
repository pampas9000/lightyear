package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/pkg/util"

	"github.com/google/uuid"
	"github.com/lib/pq"
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
	Username     string `gorm:"uniqueIndex;not null" json:"username"`
	Email        string `gorm:"uniqueIndex;not null" json:"email"`
	PasswordHash string `json:"-"`
	// SourceRetentionDays and OutputRetentionDays override the global
	// retention of the user's source files and outputs (0 keeps them
	// forever). NULL uses the global default. Reserved: no UI sets them yet.
	SourceRetentionDays *int `json:"-"`
	OutputRetentionDays *int `json:"-"`
	// StorageQuotaBytes and MonthlyUnitQuota override the global quotas
	// (QUOTA_STORAGE_BYTES, QUOTA_MONTHLY_UNITS); 0 is unlimited and NULL
	// uses the global value. Reserved: no UI sets them yet.
	StorageQuotaBytes *int64     `json:"-"`
	MonthlyUnitQuota  *int64     `json:"-"`
	Tasks             []Task     `gorm:"foreignKey:OwnerID" json:"tasks,omitempty"`
	Workflows         []Workflow `gorm:"foreignKey:OwnerID" json:"workflows,omitempty"`
	Files             []File     `gorm:"foreignKey:OwnerID" json:"files,omitempty"`
}

type UserOauthAccount struct {
	Base
	UserID            uuid.UUID `gorm:"not null;uniqueIndex:idx_provider_account" json:"user_id"`
	User              User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
	Provider          string    `gorm:"not null;uniqueIndex:idx_provider_account" json:"provider"`
	ProviderAccountID string    `gorm:"not null;uniqueIndex:idx_provider_account" json:"provider_account_id"`
	Email             string    `json:"email"`
}

// File statuses. A file's object exists in storage only while it is
// UPLOADING, FETCHING or UPLOADED. FETCHING is a source given as a URL that
// no worker has fetched yet: the first attempt of a job reading it fetches
// SourceURL into Path, and the orchestrator marks it UPLOADED once the worker
// reports the fetch. EXPIRED (retention ran out) and DELETED (the owner
// deleted it, or the upload was aborted or abandoned) are set before the
// object is removed; PurgedAt records the removal. services/lifecycle makes
// every such change.
const (
	FileUploading = "UPLOADING"
	FileFetching  = "FETCHING"
	FileUploaded  = "UPLOADED"
	FileExpired   = "EXPIRED"
	FileDeleted   = "DELETED"
)

type File struct {
	Base
	Name            string     `gorm:"not null" json:"name"`
	Path            string     `gorm:"not null" json:"path"`
	Size            int64      `json:"size"`
	MimeType        string     `json:"mime_type"`
	Status          string     `gorm:"type:varchar(20);default:'UPLOADING';index:idx_files_owner_status_uploaded,priority:2;index:idx_files_status_expires,priority:1" json:"status"`
	UploadID        string     `gorm:"type:text" json:"upload_id,omitempty"`
	UploadExpiresAt *time.Time `json:"upload_expires_at,omitempty"`
	UploadedAt      *time.Time `gorm:"index:idx_files_owner_status_uploaded,priority:3" json:"uploaded_at,omitempty"`
	Hash            string     `gorm:"type:varchar(64);index:idx_files_hash" json:"hash,omitempty"`
	// SourceURL is where a source given as a URL is fetched from; empty for
	// uploads and outputs. It is kept after the fetch as the file's origin.
	SourceURL string    `gorm:"type:text" json:"source_url,omitempty"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	OwnerID   uuid.UUID `gorm:"not null;index:idx_files_owner_status_uploaded,priority:1" json:"owner_id"`
	Owner     User      `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	// ExpiresAt is when retention runs out for an UPLOADED file. The
	// lifecycle sweep sets it (uploaded_at + retention); NULL means not yet
	// scheduled or kept forever.
	ExpiresAt *time.Time `gorm:"index:idx_files_status_expires,priority:2" json:"expires_at,omitempty"`
	// PurgedAt is when the object of an EXPIRED or DELETED file was removed
	// from storage. NULL on such a file means the removal is still pending.
	PurgedAt *time.Time `json:"-"`
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
//
// NextRunAt is set whenever a job becomes PENDING and cleared when it is
// claimed; dispatch only picks PENDING jobs with next_run_at <= now. The
// dispatch indexes are partial and include the primary key, so
// schema.Migrate creates them instead of struct tags.
type Job struct {
	Base
	OwnerID         uuid.UUID             `gorm:"type:uuid;not null;index" json:"owner_id"`
	TaskID          uuid.UUID             `gorm:"type:uuid;not null;index" json:"task_id"`
	WorkflowID      *uuid.UUID            `gorm:"type:uuid" json:"workflow_id,omitempty"`
	OutputIndex     int                   `gorm:"not null;default:0" json:"output_index"`
	OutputName      string                `gorm:"type:varchar(64);not null;default:''" json:"output_name,omitempty"`
	Operation       string                `gorm:"type:varchar(32);not null" json:"operation"`
	Options         imageoptions.Options  `gorm:"type:jsonb;not null" json:"options"`
	Target          *compression.Target   `gorm:"type:jsonb" json:"target,omitempty"`
	ResolvedOptions *imageoptions.Options `gorm:"type:jsonb" json:"resolved_options,omitempty"`
	TargetMet       *bool                 `json:"target_met,omitempty"`
	Achieved        *compression.Achieved `gorm:"type:jsonb" json:"achieved,omitempty"`
	Compression     *compression.Stats    `gorm:"-" json:"compression,omitempty"`
	SourceFileID    uuid.UUID             `gorm:"type:uuid;not null;index" json:"source_file_id"`
	SourceFile      *File                 `gorm:"foreignKey:SourceFileID" json:"source_file,omitempty"`
	OutputFileID    *uuid.UUID            `gorm:"type:uuid;index" json:"output_file_id,omitempty"`
	OutputFile      *File                 `gorm:"foreignKey:OutputFileID" json:"output_file,omitempty"`
	// ArtifactKey identifies (source, canonical options[, target]) for reusing outputs.
	ArtifactKey      string     `gorm:"type:varchar(64);index" json:"-"`
	Status           JobStatus  `gorm:"type:job_status;not null;default:'PENDING'" json:"status"`
	NextRunAt        *time.Time `json:"next_run_at,omitempty"`
	Progress         int        `gorm:"not null;default:0" json:"progress"`
	Attempts         int        `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts      int        `gorm:"not null;default:3" json:"max_attempts"`
	CurrentAttemptID *uuid.UUID `gorm:"type:uuid" json:"current_attempt_id,omitempty"`
	LeaseExpiresAt   *time.Time `gorm:"index" json:"lease_expires_at,omitempty"`
	ErrorCode        string     `gorm:"type:varchar(64)" json:"error_code,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
}

// PopulateDerived fills API response fields derived from job relations.
func (j *Job) PopulateDerived() {
	if j == nil {
		return
	}
	if j.Status == JobCompleted && j.ResolvedOptions == nil {
		resolved := j.Options
		j.ResolvedOptions = &resolved
	}
	if j.Status == JobCompleted && j.SourceFile != nil && j.OutputFile != nil {
		j.Compression = compression.BuildStats(j.SourceFile.Size, j.OutputFile.Size)
	}
}

// PopulateJobsDerived fills API response fields on each job in jobs.
func PopulateJobsDerived(jobs []Job) {
	for i := range jobs {
		jobs[i].PopulateDerived()
	}
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

// Attempt is one execution of a job. Payload is exactly what was dispatched;
// every send of the attempt, first or repeated, goes through OutboxMessage.
type Attempt struct {
	Base
	JobID        uuid.UUID     `gorm:"type:uuid;not null;index" json:"job_id"`
	Status       AttemptStatus `gorm:"type:varchar(20);not null" json:"status"`
	Queue        string        `gorm:"not null" json:"-"`
	Payload      JSON          `gorm:"type:jsonb;not null" json:"-"`
	OutputKey    string        `gorm:"not null" json:"-"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	EncodeRounds *int          `json:"encode_rounds,omitempty"`
	ComputeMS    *int64        `json:"compute_ms,omitempty"`
	ErrorCode    string        `gorm:"type:varchar(64)" json:"error_code,omitempty"`
}

// OutboxMessage is a queue message written in the same transaction as the
// state change that needs it and sent after commit, by the writer right away
// or by the orchestrator's relay once AvailableAt passes. Sending is at least
// once: a message may reach the queue more than once.
//
// AttemptID is set on job dispatches, so an attempt's sends can be listed and
// its unsent ones dropped when it is cancelled. The partial index on unsent
// messages is created by schema.Migrate.
type OutboxMessage struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CreatedAt   time.Time  `gorm:"not null"`
	Queue       string     `gorm:"type:varchar(64);not null"`
	Payload     JSON       `gorm:"type:jsonb;not null"`
	AttemptID   *uuid.UUID `gorm:"type:uuid;index"`
	AvailableAt time.Time  `gorm:"not null"`
	Tries       int        `gorm:"not null;default:0"`
	LastError   string     `gorm:"type:varchar(255);not null;default:''"`
	SentAt      *time.Time
	// StreamID is the entry ID XADD returned for the send; Recover uses it
	// to tell a dispatch still waiting in the stream from a lost one.
	StreamID string `gorm:"type:varchar(64);not null;default:''"`
}

func (m *OutboxMessage) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = util.NewUuidV7()
	}
	return nil
}

func (OutboxMessage) TableName() string { return "outbox_messages" }

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
	Name    string                      `gorm:"not null" json:"name"`
	Outputs compression.WorkflowOutputs `gorm:"type:jsonb;not null" json:"outputs"`
	OwnerID uuid.UUID                   `gorm:"not null" json:"owner_id"`
	Owner   User                        `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
}

// APIKey authenticates /v1 requests. Only the SHA-256 of the key is stored;
// the plaintext is shown once, when the key is created.
type APIKey struct {
	Base
	OwnerID    uuid.UUID      `gorm:"type:uuid;not null;index" json:"-"`
	Name       string         `gorm:"type:varchar(64);not null" json:"name"`
	Prefix     string         `gorm:"type:varchar(16);not null" json:"prefix"`
	KeyHash    string         `gorm:"type:varchar(64);not null;uniqueIndex" json:"-"`
	Scopes     pq.StringArray `gorm:"type:text[];not null" json:"scopes"`
	LastUsedAt *time.Time     `json:"last_used_at"`
	RevokedAt  *time.Time     `json:"revoked_at"`
}

func (APIKey) TableName() string { return "api_keys" }

// Usage record kinds.
const (
	// UsageProcess is a completed attempt; Units are its encode rounds.
	UsageProcess = "process"
	// UsageFailed is an attempt the worker reported failed; it costs no
	// units and keeps ComputeMS for cost tracking.
	UsageFailed = "failed"
	// UsageReuse is a job completed with an existing output; no units.
	UsageReuse = "reuse"
	// UsageFetch is a URL source the worker downloaded; InputBytes is its size.
	UsageFetch = "fetch"
)

// UsageRecord is one metered event, the ledger behind quotas and
// GET /v1/usage. It keeps job and task ids without foreign keys, so deleting
// a task does not remove its usage. Attempt events are unique per
// (attempt_id, kind), so a replayed result is not counted twice.
type UsageRecord struct {
	ID           uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	CreatedAt    time.Time  `gorm:"not null;index:idx_usage_records_owner_created,priority:2"`
	OwnerID      uuid.UUID  `gorm:"type:uuid;not null;index:idx_usage_records_owner_created,priority:1"`
	Kind         string     `gorm:"type:varchar(16);not null;uniqueIndex:idx_usage_records_attempt_kind,priority:2"`
	AttemptID    *uuid.UUID `gorm:"type:uuid;uniqueIndex:idx_usage_records_attempt_kind,priority:1"`
	JobID        uuid.UUID  `gorm:"type:uuid;not null"`
	TaskID       uuid.UUID  `gorm:"type:uuid;not null"`
	Units        int64      `gorm:"not null;default:0"`
	EncodeRounds int        `gorm:"not null;default:0"`
	ComputeMS    int64      `gorm:"not null;default:0"`
	// InputBytes is the downloaded size of a fetch; OutputBytes and
	// OutputPixels describe the output of a process or reuse.
	InputBytes   int64 `gorm:"not null;default:0"`
	OutputBytes  int64 `gorm:"not null;default:0"`
	OutputPixels int64 `gorm:"not null;default:0"`
}

func (UsageRecord) TableName() string { return "usage_records" }
