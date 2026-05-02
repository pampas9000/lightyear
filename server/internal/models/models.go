package models

import (
	"time"

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
	UserID            uuid.UUID `gorm:"not null;uniqueIndex:idx_provider_account"`
	User              User      `gorm:"foreignKey:UserID"`
	Provider          string    `gorm:"not null;uniqueIndex:idx_provider_account"`
	ProviderAccountID string    `gorm:"not null;uniqueIndex:idx_provider_account"`
	Email             string
}

type File struct {
	Base
	Name           string `gorm:"not null"`
	Path           string `gorm:"not null"`
	Size           int64
	MimeType       string
	OwnerID        uuid.UUID `gorm:"not null"`
	Owner          User      `gorm:"foreignKey:OwnerID"`
	InputToJobs    []Job     `gorm:"foreignKey:InputFileID"`
	OutputFromJobs []Job     `gorm:"foreignKey:OutputFileID"`
}

type Job struct {
	Base
	Status       string `gorm:"type:varchar(20);default:'PENDING'"`
	InputPath    string
	OutputPath   string
	TargetFormat string
	Params       string `gorm:"type:json"`
	Progress     int    `gorm:"default:0"`
	ErrorMessage string
	OwnerID      uuid.UUID `gorm:"not null"`
	Owner        User      `gorm:"foreignKey:OwnerID"`
	TaskID       *uuid.UUID
	Task         *Task `gorm:"foreignKey:TaskID"`
	WorkflowID   *uuid.UUID
	Workflow     *Workflow `gorm:"foreignKey:WorkflowID"`
	InputFileID  *uuid.UUID
	InputFile    *File `gorm:"foreignKey:InputFileID"`
	OutputFileID *uuid.UUID
	OutputFile   *File `gorm:"foreignKey:OutputFileID"`
}

type Task struct {
	Base
	Status     string    `gorm:"type:varchar(20);default:'PENDING'"`
	OwnerID    uuid.UUID `gorm:"not null"`
	Owner      User      `gorm:"foreignKey:OwnerID"`
	Jobs       []Job     `gorm:"foreignKey:TaskID"`
	WorkflowID *uuid.UUID
	Workflow   *Workflow `gorm:"foreignKey:WorkflowID"`
}

type Workflow struct {
	Base
	Name         string    `gorm:"not null"`
	TargetFormat string    `gorm:"not null"`
	Params       string    `gorm:"type:json"`
	OwnerID      uuid.UUID `gorm:"not null"`
	Owner        User      `gorm:"foreignKey:OwnerID"`
	Jobs         []Job     `gorm:"foreignKey:WorkflowID"`
	Tasks        []Task    `gorm:"foreignKey:WorkflowID"`
}
