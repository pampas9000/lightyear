package models

import (
	"time"

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
	Status          string     `gorm:"type:varchar(20);default:'UPLOADING'" json:"status"`
	UploadID        string     `gorm:"type:varchar(255)" json:"upload_id,omitempty"`
	UploadExpiresAt *time.Time `json:"upload_expires_at,omitempty"`
	OwnerID         uuid.UUID  `gorm:"not null" json:"owner_id"`
	Owner           User       `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	InputToJobs     []Job      `gorm:"foreignKey:InputFileID" json:"input_to_jobs,omitempty"`
	OutputFromJobs  []Job      `gorm:"foreignKey:OutputFileID" json:"output_from_jobs,omitempty"`
}

type Job struct {
	Base
	Status       string    `gorm:"type:varchar(20);default:'PENDING'" json:"status"`
	InputPath    string    `json:"input_path"`
	OutputPath   string    `json:"output_path"`
	TargetFormat string          `json:"target_format"`
	Params       transcode.Params `gorm:"type:json" json:"params"`
	Progress     int             `gorm:"default:0" json:"progress"`
	ErrorMessage string    `json:"error_message,omitempty"`
	OwnerID      uuid.UUID `gorm:"not null" json:"owner_id"`
	Owner        User      `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	TaskID       *uuid.UUID `json:"task_id,omitempty"`
	Task         *Task     `gorm:"foreignKey:TaskID" json:"task,omitempty"`
	WorkflowID   *uuid.UUID `json:"workflow_id,omitempty"`
	Workflow     *Workflow `gorm:"foreignKey:WorkflowID" json:"workflow,omitempty"`
	InputFileID  *uuid.UUID `json:"input_file_id,omitempty"`
	InputFile    *File     `gorm:"foreignKey:InputFileID" json:"input_file,omitempty"`
	OutputFileID *uuid.UUID `json:"output_file_id,omitempty"`
	OutputFile   *File     `gorm:"foreignKey:OutputFileID" json:"output_file,omitempty"`
}

type Task struct {
	Base
	Status     string     `gorm:"type:varchar(20);default:'PENDING'" json:"status"`
	OwnerID    uuid.UUID  `gorm:"not null" json:"owner_id"`
	Owner      User       `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	Jobs       []Job      `gorm:"foreignKey:TaskID" json:"jobs,omitempty"`
	WorkflowID *uuid.UUID  `json:"workflow_id,omitempty"`
	Workflow   *Workflow  `gorm:"foreignKey:WorkflowID" json:"workflow,omitempty"`
}

type Workflow struct {
	Base
	Name         string    `gorm:"not null" json:"name"`
	TargetFormat string          `gorm:"not null" json:"target_format"`
	Params       transcode.Params `gorm:"type:json" json:"params"`
	OwnerID      uuid.UUID       `gorm:"not null" json:"owner_id"`
	Owner        User      `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	Jobs         []Job     `gorm:"foreignKey:WorkflowID" json:"jobs,omitempty"`
	Tasks        []Task    `gorm:"foreignKey:WorkflowID" json:"tasks,omitempty"`
}
