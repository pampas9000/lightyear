package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"transcoder/server/internal/api/middleware"
	"transcoder/server/internal/api/response"
	v1 "transcoder/server/internal/api/v1"
	"transcoder/server/internal/models"
	"transcoder/server/internal/services/apikey"
	"transcoder/server/internal/services/task"
	"transcoder/server/internal/services/upload"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolError is the content of a failed tool call: the code, message and
// details /v1 would answer with.
type toolError struct {
	Code    response.ResponseCode `json:"code"`
	Message string                `json:"message"`
	Details any                   `json:"details,omitempty"`
}

func fromV1(e *v1.Error) *toolError {
	return &toolError{Code: e.Code, Message: e.Message, Details: e.Details}
}

// internal logs err and reports a generic internal error, as /v1 does:
// the error stays in the log, the result carries only details.request_id.
func internal(ctx context.Context, action string, err error) *toolError {
	rid := requestID(ctx)
	slog.ErrorContext(ctx, "mcp tool failed", "request_id", rid, "action", action, "error", err)
	te := &toolError{Code: response.CodeInternal, Message: "Failed to " + action + "."}
	if rid != "" {
		te.Details = map[string]string{"request_id": rid}
	}
	return te
}

func taskError(ctx context.Context, err error, action string) *toolError {
	if e := v1.TaskError(err); e != nil {
		return fromV1(e)
	}
	return internal(ctx, action, err)
}

func uploadError(ctx context.Context, err error, action string) *toolError {
	if e := v1.UploadError(err); e != nil {
		return fromV1(e)
	}
	return internal(ctx, action, err)
}

// errorResult carries a tool error as JSON text. There is no
// structuredContent: clients check it against the tool's outputSchema, which
// describes the successful result.
func errorResult(e *toolError) *sdk.CallToolResult {
	b, err := json.Marshal(e)
	if err != nil {
		b = []byte(`{"code":"error.internal","message":"Failed to encode the error."}`)
	}
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}
}

type toolFunc[In, Out any] func(ctx context.Context, key *models.APIKey, in In) (Out, *toolError)

// addTool registers a tool whose arguments are checked against inputSchema,
// whose caller must hold scope, and whose result is the JSON of Out as both
// structuredContent and text. The SDK's typed AddTool is not used because it
// cannot return structured error details.
func addTool[In, Out any](s *Server, t *sdk.Tool, scope string, h toolFunc[In, Out]) error {
	in, ok := t.InputSchema.(*jsonschema.Schema)
	if !ok {
		return fmt.Errorf("tool %s: input schema must be a *jsonschema.Schema", t.Name)
	}
	inResolved, err := in.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return fmt.Errorf("tool %s: input schema: %w", t.Name, err)
	}
	out, err := outputSchema[Out]()
	if err != nil {
		return fmt.Errorf("tool %s: output schema: %w", t.Name, err)
	}
	outResolved, err := out.Resolve(nil)
	if err != nil {
		return fmt.Errorf("tool %s: output schema: %w", t.Name, err)
	}
	t.OutputSchema = out

	s.mcp.AddTool(t, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		key := caller(req)
		if key == nil {
			return errorResult(&toolError{Code: response.CodeUnauthorized, Message: "Missing API key."}), nil
		}
		if !apikey.HasScope(key, scope) {
			return errorResult(&toolError{Code: response.CodeInsufficientScope, Message: "API key lacks the required scope.",
				Details: map[string]string{"required_scope": scope}}), nil
		}
		input, terr := decodeArgs[In](req.Params.Arguments, inResolved)
		if terr != nil {
			return errorResult(terr), nil
		}
		result, terr := h(ctx, key, input)
		if terr != nil {
			return errorResult(terr), nil
		}
		b, err := json.Marshal(result)
		if err != nil {
			return errorResult(internal(ctx, "encode the result", err)), nil
		}
		var check any
		if err := json.Unmarshal(b, &check); err != nil {
			return errorResult(internal(ctx, "encode the result", err)), nil
		}
		if err := outResolved.Validate(check); err != nil {
			return errorResult(internal(ctx, "encode the result", fmt.Errorf("%s output does not match its schema: %w", t.Name, err))), nil
		}
		return &sdk.CallToolResult{
			Content:           []sdk.Content{&sdk.TextContent{Text: string(b)}},
			StructuredContent: json.RawMessage(b),
		}, nil
	})
	return nil
}

// decodeArgs validates the arguments against the input schema, applies its
// defaults and decodes them into In.
func decodeArgs[In any](raw json.RawMessage, schema *jsonschema.Resolved) (In, *toolError) {
	var zero In
	invalid := func(err error) (In, *toolError) {
		return zero, &toolError{Code: response.CodeBodyInvalid, Message: "Invalid arguments.",
			Details: map[string]string{"reason": err.Error()}}
	}
	args := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &args); err != nil {
			return invalid(err)
		}
	}
	if err := schema.ApplyDefaults(&args); err != nil {
		return invalid(err)
	}
	if err := schema.Validate(args); err != nil {
		return invalid(err)
	}
	b, err := json.Marshal(args)
	if err != nil {
		return invalid(err)
	}
	var in In
	if err := json.Unmarshal(b, &in); err != nil {
		return invalid(err)
	}
	return in, nil
}

func (s *Server) addTools() error {
	submitSchema, err := submitTaskInputSchema()
	if err != nil {
		return err
	}
	if err := addTool(s, &sdk.Tool{
		Name:  "create_upload",
		Title: "Create an upload",
		Description: "Register a local image file and get a presigned URL to send its bytes to. " +
			"The bytes do not go through this tool: send them yourself with an HTTP client, without an Authorization header. " +
			"method put: PUT the whole file to url. method multipart (files over 10 MB): PUT byte range [i*part_size, (i+1)*part_size) to part_urls[i]. " +
			"method none: a file with the same sha256 is already stored; nothing to send. " +
			"Then pass file.id to submit_task, which finishes the upload. URLs expire at expires_at (1 hour). Requires tasks:write.",
		InputSchema: createUploadInputSchema(),
		Annotations: &sdk.ToolAnnotations{OpenWorldHint: ptr(false), DestructiveHint: ptr(false)},
	}, apikey.ScopeTasksWrite, s.createUpload); err != nil {
		return err
	}
	if err := addTool(s, &sdk.Tool{
		Name:  "submit_task",
		Title: "Submit a conversion task",
		Description: "Convert or compress source images asynchronously. Returns the task at once (status PENDING, or COMPLETED when identical outputs already exist); " +
			"poll it with get_task. Sources are file ids from create_upload or public image URLs. " +
			"Give options (format required, e.g. {\"format\": \"avif\"}) with an optional target such as {\"max_bytes\": 102400}, or outputs for several formats at once. " +
			"Counts against the monthly processing quota: 1 unit per output, more for a target search. Requires tasks:write.",
		InputSchema: submitSchema,
		Annotations: &sdk.ToolAnnotations{OpenWorldHint: ptr(true), DestructiveHint: ptr(false)},
	}, apikey.ScopeTasksWrite, s.submitTask); err != nil {
		return err
	}
	return addTool(s, &sdk.Tool{
		Name:  "get_task",
		Title: "Get a task",
		Description: "Read a task and its jobs, optionally waiting up to wait_seconds for it to finish. " +
			"Completed jobs carry output_file.download_url (valid until download_expires_at, 1 hour; call again for a fresh one) " +
			"and compression (source_bytes, output_bytes, ratio, saved_ratio). Failed jobs carry error_code and error_message. " +
			"Image content is never returned. Requires tasks:read.",
		InputSchema: getTaskInputSchema(),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(false)},
	}, apikey.ScopeTasksRead, s.getTask)
}

type createUploadInput struct {
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

// rateLimit takes a token from the caller's /v1 write bucket.
func (s *Server) rateLimit(ctx context.Context, key *models.APIKey, tool string) *toolError {
	seconds := middleware.Take(ctx, s.limiter, key.OwnerID.String())
	if seconds == 0 {
		return nil
	}
	slog.WarnContext(ctx, "rate limited", "user_id", key.OwnerID, "api_key_prefix", key.Prefix,
		"mcp_tool", tool, "retry_after", seconds)
	return &toolError{Code: response.CodeRateLimited, Message: fmt.Sprintf("Too many requests. Retry after %d seconds.", seconds),
		Details: middleware.RateLimitDetail{RetryAfter: seconds}}
}

func (s *Server) createUpload(ctx context.Context, key *models.APIKey, in createUploadInput) (uploadOutput, *toolError) {
	if e := s.rateLimit(ctx, key, "create_upload"); e != nil {
		return uploadOutput{}, e
	}
	res, err := s.uploads.Init(ctx, key.OwnerID, upload.InitInput{
		Filename: in.Filename, ContentType: in.ContentType, Size: in.Size, Hash: in.SHA256,
	})
	if err != nil {
		return uploadOutput{}, uploadError(ctx, err, "create upload")
	}
	return v1.NewUpload(res), nil
}

type submitItem struct {
	FileID *uuid.UUID `json:"file_id,omitempty"`
	URL    string     `json:"url,omitempty"`
}

type submitTaskInput struct {
	Items          []submitItem    `json:"items"`
	Options        json.RawMessage `json:"options,omitempty"`
	Target         json.RawMessage `json:"target,omitempty"`
	Outputs        json.RawMessage `json:"outputs,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

func (s *Server) submitTask(ctx context.Context, key *models.APIKey, in submitTaskInput) (taskOutput, *toolError) {
	if e := s.rateLimit(ctx, key, "submit_task"); e != nil {
		return taskOutput{}, e
	}
	items := make([]task.TaskItemInput, len(in.Items))
	for i, it := range in.Items {
		if it.FileID != nil {
			items[i].FileID = *it.FileID
			if e := s.finishUpload(ctx, key.OwnerID, *it.FileID); e != nil {
				return taskOutput{}, e
			}
		}
		items[i].URL = it.URL
	}
	t, _, err := s.tasks.Create(ctx, task.CreateTaskInput{
		OwnerID:        key.OwnerID,
		Items:          items,
		Options:        in.Options,
		Target:         in.Target,
		Outputs:        in.Outputs,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
	})
	if err != nil {
		return taskOutput{}, taskError(ctx, err, "create task")
	}
	return s.present(ctx, t)
}

// finishUpload completes an upload whose bytes the client has sent: MCP has
// no separate complete step. Files in any other state are left to the task
// service to accept or refuse.
func (s *Server) finishUpload(ctx context.Context, owner, id uuid.UUID) *toolError {
	f, err := s.uploads.FileByID(ctx, owner, id)
	if err != nil || f.Status != models.FileUploading {
		return nil
	}
	if _, err := s.uploads.Finish(ctx, f); err != nil {
		if errors.Is(err, upload.ErrObjectMissing) {
			return &toolError{Code: response.CodeParamInvalid,
				Message: fmt.Sprintf("File %s has not been uploaded yet (%v). Send its bytes to the URL from create_upload first.", id, err),
				Details: response.ValidationDetail{Field: "items", Reason: "upload not finished"}}
		}
		return uploadError(ctx, err, "finish upload")
	}
	return nil
}

type getTaskInput struct {
	TaskID      uuid.UUID `json:"task_id"`
	WaitSeconds int       `json:"wait_seconds"`
}

func (s *Server) getTask(ctx context.Context, key *models.APIKey, in getTaskInput) (taskOutput, *toolError) {
	deadline := s.now().Add(time.Duration(in.WaitSeconds) * time.Second)
	for {
		t, err := s.tasks.GetTask(ctx, key.OwnerID, in.TaskID)
		if err != nil {
			return taskOutput{}, taskError(ctx, err, "fetch task")
		}
		if v1.Terminal(t.Status) || !s.now().Add(s.poll).Before(deadline) {
			return s.present(ctx, t)
		}
		select {
		case <-ctx.Done():
			return taskOutput{}, internal(ctx, "fetch task", ctx.Err())
		case <-time.After(s.poll):
		}
	}
}

// present is the /v1 representation of t with download URLs for the jobs
// that have finished.
func (s *Server) present(ctx context.Context, t *models.Task) (taskOutput, *toolError) {
	out := v1.NewTask(t)
	if err := v1.AttachDownloads(ctx, &out, t, s.sign, v1.DownloadURLTTL, s.now()); err != nil {
		return taskOutput{}, internal(ctx, "sign download URLs", err)
	}
	return out, nil
}
