package v1

import (
	"context"
	"time"

	"transcoder/server/internal/compression"
	"transcoder/server/internal/imageoptions"
	"transcoder/server/internal/models"

	"github.com/google/uuid"
)

// The /v1 representations are separate from the models so the public
// contract does not change when internal columns do.

type File struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	Size     int64     `json:"size"`
	MimeType string    `json:"mime_type"`
	Width    int       `json:"width,omitempty"`
	Height   int       `json:"height,omitempty"`
	SHA256   string    `json:"sha256,omitempty"`
	// SourceURL is set on a source given as a URL. Such a file is FETCHING
	// until a worker has fetched it.
	SourceURL  string     `json:"source_url,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UploadedAt *time.Time `json:"uploaded_at,omitempty"`
	// ExpiresAt is when retention removes the file (UPLOADED files only).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// OutputFile is a job's product; DownloadURL is set on GET /v1/tasks/:id
// once the job has completed.
type OutputFile struct {
	File
	DownloadURL       string     `json:"download_url,omitempty"`
	DownloadExpiresAt *time.Time `json:"download_expires_at,omitempty"`
}

type Job struct {
	ID              uuid.UUID             `json:"id"`
	Status          models.JobStatus      `json:"status"`
	Progress        int                   `json:"progress"`
	OutputIndex     int                   `json:"output_index"`
	OutputName      string                `json:"output_name,omitempty"`
	Options         imageoptions.Options  `json:"options"`
	Target          *compression.Target   `json:"target,omitempty"`
	ResolvedOptions *imageoptions.Options `json:"resolved_options,omitempty"`
	TargetMet       *bool                 `json:"target_met,omitempty"`
	Achieved        *compression.Achieved `json:"achieved,omitempty"`
	Compression     *compression.Stats    `json:"compression,omitempty"`
	Attempts        int                   `json:"attempts"`
	MaxAttempts     int                   `json:"max_attempts"`
	ErrorCode       string                `json:"error_code,omitempty"`
	ErrorMessage    string                `json:"error_message,omitempty"`
	SourceFile      *File                 `json:"source_file,omitempty"`
	OutputFile      *OutputFile           `json:"output_file,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type Task struct {
	ID             uuid.UUID         `json:"id"`
	Status         models.TaskStatus `json:"status"`
	WorkflowID     *uuid.UUID        `json:"workflow_id,omitempty"`
	IdempotencyKey *string           `json:"idempotency_key,omitempty"`
	Jobs           []Job             `json:"jobs"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func newFile(f *models.File) *File {
	if f == nil {
		return nil
	}
	return &File{
		ID: f.ID, Name: f.Name, Status: f.Status, Size: f.Size, MimeType: f.MimeType,
		Width: f.Width, Height: f.Height, SHA256: f.Hash, SourceURL: f.SourceURL, CreatedAt: f.CreatedAt, UploadedAt: f.UploadedAt,
		ExpiresAt: f.ExpiresAt,
	}
}

func newJob(j *models.Job) Job {
	j.PopulateDerived()
	out := Job{
		ID: j.ID, Status: j.Status, Progress: j.Progress, OutputIndex: j.OutputIndex, OutputName: j.OutputName,
		Options: j.Options, Target: j.Target, ResolvedOptions: j.ResolvedOptions, TargetMet: j.TargetMet,
		Achieved: j.Achieved, Compression: j.Compression, Attempts: j.Attempts, MaxAttempts: j.MaxAttempts,
		ErrorCode: j.ErrorCode, ErrorMessage: j.ErrorMessage, SourceFile: newFile(j.SourceFile),
		CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
	}
	if f := newFile(j.OutputFile); f != nil {
		out.OutputFile = &OutputFile{File: *f}
	}
	return out
}

func newTask(t *models.Task) Task {
	out := Task{
		ID: t.ID, Status: t.Status, WorkflowID: t.WorkflowID, IdempotencyKey: t.IdempotencyKey,
		Jobs: make([]Job, len(t.Jobs)), CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	for i := range t.Jobs {
		out.Jobs[i] = newJob(&t.Jobs[i])
	}
	return out
}

// downloadable reports whether a job's output can be signed for download.
func downloadable(j *Job) bool {
	return j.Status == models.JobCompleted && j.OutputFile != nil && j.OutputFile.Status == models.FileUploaded
}

// Signer presigns a download of the object at path, saved as filename.
type Signer func(ctx context.Context, path, filename string, ttl time.Duration) (string, error)

// attachDownloads signs a download URL for every completed job's output.
func attachDownloads(ctx context.Context, t *Task, src *models.Task, sign Signer, ttl time.Duration, now time.Time) error {
	paths := make(map[uuid.UUID]string, len(src.Jobs))
	for _, j := range src.Jobs {
		if j.OutputFile != nil {
			paths[j.OutputFile.ID] = j.OutputFile.Path
		}
	}
	expires := now.Add(ttl)
	for i := range t.Jobs {
		j := &t.Jobs[i]
		if !downloadable(j) {
			continue
		}
		url, err := sign(ctx, paths[j.OutputFile.ID], j.OutputFile.Name, ttl)
		if err != nil {
			return err
		}
		j.OutputFile.DownloadURL = url
		j.OutputFile.DownloadExpiresAt = &expires
	}
	return nil
}
