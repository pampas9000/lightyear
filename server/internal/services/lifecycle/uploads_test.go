package lifecycle

import (
	"errors"
	"testing"
	"time"

	"transcoder/server/internal/models"

	"github.com/google/uuid"
)

// upload creates an UPLOADING file whose upload window ends at expires.
func (e *env) upload(uploadID string, expires time.Time) models.File {
	e.t.Helper()
	f := models.File{OwnerID: e.owner, Name: "u.png", Path: "uploads/" + e.owner.String() + "/" + uuid.NewString() + ".png",
		Status: models.FileUploading, UploadID: uploadID, UploadExpiresAt: &expires, Size: 1000}
	if err := e.db.Create(&f).Error; err != nil {
		e.t.Fatal(err)
	}
	return f
}

// draft makes the file one of the owner's drafts, created at the given time.
func (e *env) draft(fileID uuid.UUID, created time.Time) {
	e.t.Helper()
	d := models.DraftFile{FileID: fileID, OwnerID: e.owner}
	if err := e.db.Create(&d).Error; err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.Model(&d).Update("created_at", created).Error; err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) hasDraft(fileID uuid.UUID) bool {
	e.t.Helper()
	var n int64
	e.db.Model(&models.DraftFile{}).Where("file_id = ?", fileID).Count(&n)
	return n > 0
}

func (e *env) sweepAt(now time.Time) SweepResult {
	e.t.Helper()
	res, err := e.svc.Sweep(e.t.Context(), now)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) sweepUploads(now time.Time) UploadSweepResult {
	e.t.Helper()
	res, err := e.svc.SweepUploads(e.t.Context(), now)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func TestDeletedUploadWaitsForItsURLs(t *testing.T) {
	e := setup(t)
	up := e.upload("", time.Now().Add(24*time.Hour))
	e.draft(up.ID, time.Now())
	before := time.Now()
	f, err := e.svc.DeleteFile(t.Context(), e.owner, up.ID)
	if err != nil || f.Status != models.FileDeleted {
		t.Fatalf("delete: %v %+v", err, f)
	}
	got, ok := e.file(up.ID)
	if !ok || got.Status != models.FileDeleted || got.UploadExpiresAt == nil ||
		got.UploadExpiresAt.After(before.Add(UploadURLExpiry+time.Minute)) || got.UploadExpiresAt.Before(before) {
		t.Fatalf("upload window not brought forward: %+v", got)
	}
	if e.hasDraft(up.ID) {
		t.Fatal("draft kept")
	}
	// A PUT with a URL signed before the deletion can still land, so the
	// object stays until the URLs run out.
	if e.store.has(up.Path) {
		t.Fatal("object removed while its URL is valid")
	}
	if res := e.sweepAt(time.Now()); res.Purged != 0 || e.store.has(up.Path) {
		t.Fatalf("sweep purged early: %+v", res)
	}
	if res := e.sweepAt(time.Now().Add(UploadURLExpiry + time.Minute)); res.Purged != 1 || !e.store.has(up.Path) {
		t.Fatalf("sweep after the window: %+v", res)
	}
	if _, ok := e.file(up.ID); ok {
		t.Fatal("row of an unreferenced deleted upload kept")
	}
}

func TestDeletedMultipartUploadIsAbortedNowAndAgainLater(t *testing.T) {
	e := setup(t)
	up := e.upload("mp-1", time.Now().Add(24*time.Hour))
	if _, err := e.svc.DeleteFile(t.Context(), e.owner, up.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.store.abortCount("mp-1"); n != 1 {
		t.Fatalf("aborted %d times on delete", n)
	}
	// A part in flight can complete after the abort; aborting again is the
	// documented remedy, and NoSuchUpload then counts as done.
	if res := e.sweepAt(time.Now().Add(UploadURLExpiry + time.Minute)); res.Purged != 1 {
		t.Fatalf("sweep: %+v", res)
	}
	if n := e.store.abortCount("mp-1"); n != 2 || !e.store.has(up.Path) {
		t.Fatalf("aborted %d times; object removed %v", n, e.store.has(up.Path))
	}
	if _, ok := e.file(up.ID); ok {
		t.Fatal("row kept")
	}
}

func TestFailedAbortIsRetried(t *testing.T) {
	e := setup(t)
	up := e.upload("mp-2", time.Now().Add(-time.Minute))
	e.store.fail = true
	if _, err := e.svc.DeleteFile(t.Context(), e.owner, up.ID); err != nil {
		t.Fatalf("delete must succeed while storage is down: %v", err)
	}
	if res := e.sweepAt(time.Now()); res.Failed != 1 {
		t.Fatalf("sweep with storage down: %+v", res)
	}
	e.store.fail = false
	if res := e.sweepAt(time.Now()); res.Purged != 1 || e.store.abortCount("mp-2") != 1 {
		t.Fatalf("retry: %+v", res)
	}
}

func TestSweepUploads(t *testing.T) {
	e := setup(t)
	now := time.Now()
	// Finished in storage but never completed: recorded as UPLOADED.
	landed := e.upload("", now.Add(-time.Minute))
	e.draft(landed.ID, now)
	e.store.put(landed.Path, 4321)
	// Never finished: deleted.
	lost := e.upload("mp-3", now.Add(-2*time.Minute))
	e.draft(lost.ID, now)
	// Still within its window: untouched.
	pending := e.upload("", now.Add(time.Hour))

	res := e.sweepUploads(now)
	if res.Recovered != 1 || res.Abandoned != 1 || res.Purged != 1 {
		t.Fatalf("sweep: %+v", res)
	}
	got, _ := e.file(landed.ID)
	if got.Status != models.FileUploaded || got.Size != 4321 || got.UploadedAt == nil || got.ExpiresAt == nil {
		t.Fatalf("landed upload: %+v", got)
	}
	if !e.hasDraft(landed.ID) {
		t.Fatal("recovered upload lost its draft")
	}
	if _, ok := e.file(lost.ID); ok || e.hasDraft(lost.ID) || e.store.abortCount("mp-3") != 1 {
		t.Fatal("abandoned upload not removed")
	}
	if got, _ := e.file(pending.ID); got.Status != models.FileUploading {
		t.Fatalf("pending upload: %s", got.Status)
	}
}

func TestSweepUploadsKeepsUploadsStorageCannotCheck(t *testing.T) {
	e := setup(t)
	now := time.Now()
	a, b := e.upload("", now.Add(-2*time.Minute)), e.upload("", now.Add(-time.Minute))
	e.svc.BatchSize = 1
	e.store.fail = true
	if res := e.sweepUploads(now); res.Failed != 1 {
		t.Fatalf("sweep with storage down: %+v", res)
	}
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		if got, _ := e.file(id); got.Status != models.FileUploading {
			t.Fatalf("upload changed while storage was down: %s", got.Status)
		}
	}
	e.store.fail = false
	if res := e.sweepUploads(now); res.Abandoned != 1 {
		t.Fatalf("one per batch: %+v", res)
	}
	if res := e.sweepUploads(now); res.Abandoned != 1 {
		t.Fatalf("next batch: %+v", res)
	}
}

func TestDeleteDraft(t *testing.T) {
	e := setup(t)
	// An unsubmitted upload goes with its draft.
	lone := e.source("lone.png")
	e.draft(lone.ID, time.Now())
	deleted, err := e.svc.DeleteDraft(t.Context(), e.owner, lone.ID)
	if err != nil || !deleted {
		t.Fatalf("delete draft: %v %v", deleted, err)
	}
	if _, ok := e.file(lone.ID); ok || !e.store.has(lone.Path) || e.hasDraft(lone.ID) {
		t.Fatal("file of a deleted draft kept")
	}
	if _, err := e.svc.DeleteDraft(t.Context(), e.owner, lone.ID); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	// A file a task uses only stops being a draft.
	// (Submitting removes the draft; a second upload of the same file
	// makes it a draft again.)
	used := e.source("used.png")
	e.submit(used.ID, 70)
	e.draft(used.ID, time.Now())
	deleted, err = e.svc.DeleteDraft(t.Context(), e.owner, used.ID)
	if err != nil || deleted {
		t.Fatalf("delete used draft: %v %v", deleted, err)
	}
	if got, _ := e.file(used.ID); got.Status != models.FileUploaded || e.hasDraft(used.ID) {
		t.Fatalf("used file: %s, draft kept %v", got.Status, e.hasDraft(used.ID))
	}

	// A draft whose file is gone is removed.
	orphan := uuid.New()
	e.draft(orphan, time.Now())
	if deleted, err := e.svc.DeleteDraft(t.Context(), e.owner, orphan); err != nil || deleted || e.hasDraft(orphan) {
		t.Fatalf("orphan draft: %v %v", deleted, err)
	}

	// Another owner's draft is not found.
	other := e.source("other.png")
	e.draft(other.ID, time.Now())
	if _, err := e.svc.DeleteDraft(t.Context(), uuid.New(), other.ID); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("other owner: %v", err)
	}
}

func TestStaleDraftsAreRemoved(t *testing.T) {
	e := setup(t)
	old := time.Now().Add(-DraftRetention - time.Hour)
	lone := e.source("lone.png")
	e.draft(lone.ID, old)
	used := e.source("used.png")
	e.submit(used.ID, 70)
	e.draft(used.ID, old)
	orphan := uuid.New()
	e.draft(orphan, old)
	// Drafts of unfinished uploads wait for the upload sweep, and must not
	// fill the batch.
	up := e.upload("", time.Now().Add(time.Hour))
	e.draft(up.ID, old)
	fresh := e.source("fresh.png")
	e.draft(fresh.ID, time.Now())
	e.svc.BatchSize = 3

	res := e.sweepUploads(time.Now())
	if res.Drafts != 3 || res.Purged != 1 {
		t.Fatalf("sweep: %+v", res)
	}
	if _, ok := e.file(lone.ID); ok || !e.store.has(lone.Path) {
		t.Fatal("stale unsubmitted file kept")
	}
	if got, _ := e.file(used.ID); got.Status != models.FileUploaded || e.hasDraft(used.ID) {
		t.Fatal("used file deleted or its draft kept")
	}
	if e.hasDraft(orphan) {
		t.Fatal("orphan draft kept")
	}
	if !e.hasDraft(up.ID) || !e.hasDraft(fresh.ID) {
		t.Fatal("draft removed too early")
	}
}
