package backup

import (
	"context"
	"errors"
	"testing"
	"time"
)

const testDate = "2026-05-27"

var testNow = time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)

func runHandler(t *testing.T, f *fakeS3, dump Dumper, retention int) *Handler {
	t.Helper()
	h := New(Config{
		S3:            f,
		Bucket:        "test-bucket",
		Database:      DatabaseConfig{Host: "localhost"},
		RetentionDays: retention,
		Dump:          dump,
	})
	h.now = fixedClock(testNow)
	return h
}

func TestRunCreatesAllBackups(t *testing.T) {
	f := newFakeS3()
	h := runHandler(t, f, staticDump([]byte("CREATE TABLE foo;")), 7)

	res, err := h.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "created" || res.Reason != "content changed" {
		t.Errorf("action=%q reason=%q, want created/content changed", res.Action, res.Reason)
	}
	if res.Status != "ok" || res.Key != "daily/"+testDate+"-backup.sql" {
		t.Errorf("status=%q key=%q unexpected", res.Status, res.Key)
	}
	if res.SizeBytes == 0 || res.Size == "" {
		t.Errorf("size not populated: size=%q bytes=%d", res.Size, res.SizeBytes)
	}
	for _, key := range []string{
		"daily/" + testDate + "-backup.sql",
		"monthly/2026-05-backup.sql",
		"yearly/2026-backup.sql",
	} {
		if _, ok := f.objects[key]; !ok {
			t.Errorf("expected object %q to be created", key)
		}
	}
}

func TestRunScheduledSkipsUnchanged(t *testing.T) {
	body := []byte("unchanged")
	f := newFakeS3()
	// Seed today's daily with identical content as the most recent backup.
	f.seed("daily/"+testDate+"-backup.sql", body, testNow.Add(-time.Hour))
	h := runHandler(t, f, staticDump(body), 7)
	before := f.puts

	res, err := h.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "skipped" || res.Reason != "unchanged" {
		t.Errorf("action=%q reason=%q, want skipped/unchanged", res.Action, res.Reason)
	}
	if f.puts != before {
		t.Errorf("expected no uploads, got %d", f.puts-before)
	}
}

func TestRunForcedStoresWhenTodayMissing(t *testing.T) {
	body := []byte("same-as-old")
	f := newFakeS3()
	// An older day's backup matches, but today's key is absent.
	f.seed("daily/2026-05-20-backup.sql", body, testNow.Add(-72*time.Hour))
	h := runHandler(t, f, staticDump(body), 30)

	res, err := h.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "created" || res.Reason != "forced; matched an older backup" {
		t.Errorf("action=%q reason=%q, want created/forced", res.Action, res.Reason)
	}
	if _, ok := f.objects["daily/"+testDate+"-backup.sql"]; !ok {
		t.Error("expected today's daily backup to be created")
	}
}

func TestRunForcedSkipsWhenTodayIdentical(t *testing.T) {
	body := []byte("identical-today")
	f := newFakeS3()
	f.seed("daily/"+testDate+"-backup.sql", body, testNow)
	h := runHandler(t, f, staticDump(body), 7)
	before := f.puts

	res, err := h.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "skipped" || res.Reason != "today's backup already identical" {
		t.Errorf("action=%q reason=%q, want skipped/identical", res.Action, res.Reason)
	}
	if f.puts != before {
		t.Errorf("expected no uploads, got %d", f.puts-before)
	}
}

func TestRunForcedContentChanged(t *testing.T) {
	f := newFakeS3()
	// Today's existing backup differs from the new dump.
	f.seed("daily/"+testDate+"-backup.sql", []byte("old-content"), testNow.Add(-time.Hour))
	h := runHandler(t, f, staticDump([]byte("new-content")), 7)

	res, err := h.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "created" || res.Reason != "content changed" {
		t.Errorf("action=%q reason=%q, want created/content changed", res.Action, res.Reason)
	}
}

func TestRunDoesNotRecreatePeriodicBackups(t *testing.T) {
	f := newFakeS3()
	// Pre-existing monthly/yearly with distinct bodies must be preserved.
	f.seed("monthly/2026-05-backup.sql", []byte("OLD-MONTHLY"), testNow.Add(-time.Hour))
	f.seed("yearly/2026-backup.sql", []byte("OLD-YEARLY"), testNow.Add(-time.Hour))
	h := runHandler(t, f, staticDump([]byte("fresh-daily")), 7)

	if _, err := h.Run(context.Background(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(f.objects["monthly/2026-05-backup.sql"].body); got != "OLD-MONTHLY" {
		t.Errorf("monthly backup overwritten: %q", got)
	}
	if got := string(f.objects["yearly/2026-backup.sql"].body); got != "OLD-YEARLY" {
		t.Errorf("yearly backup overwritten: %q", got)
	}
}

func TestRunCleansUpOldDailyBackups(t *testing.T) {
	f := newFakeS3()
	f.seed("daily/2026-05-01-backup.sql", []byte("old"), testNow.Add(-100*time.Hour))   // before cutoff -> deleted
	f.seed("daily/2026-05-26-backup.sql", []byte("recent"), testNow.Add(-24*time.Hour)) // within window -> kept
	f.seed("daily/not-a-date-backup.sql", []byte("weird"), testNow.Add(-24*time.Hour))  // unparseable -> kept
	h := runHandler(t, f, staticDump([]byte("fresh")), 7)

	if _, err := h.Run(context.Background(), false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := f.objects["daily/2026-05-01-backup.sql"]; ok {
		t.Error("expected old daily backup to be deleted")
	}
	if _, ok := f.objects["daily/2026-05-26-backup.sql"]; !ok {
		t.Error("expected recent daily backup to be kept")
	}
	if _, ok := f.objects["daily/not-a-date-backup.sql"]; !ok {
		t.Error("expected unparseable key to be left untouched")
	}
}

func TestRunDumpError(t *testing.T) {
	h := runHandler(t, newFakeS3(), failingDump(errors.New("pg_dump exploded")), 7)
	if _, err := h.Run(context.Background(), false); err == nil {
		t.Fatal("expected error when dump fails, got nil")
	}
}

func TestRunUploadError(t *testing.T) {
	f := newFakeS3()
	f.putErr = errors.New("S3 down")
	h := runHandler(t, f, staticDump([]byte("data")), 7)
	if _, err := h.Run(context.Background(), false); err == nil {
		t.Fatal("expected error when upload fails, got nil")
	}
}

func TestRunListErrorStillUploads(t *testing.T) {
	f := newFakeS3()
	f.listErr = errors.New("list failed")
	h := runHandler(t, f, staticDump([]byte("data")), 7)

	// A failing list is treated as "no prior backup", so the run proceeds.
	res, err := h.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Action != "created" {
		t.Errorf("action=%q, want created", res.Action)
	}
}

func TestRunPeriodicCheckError(t *testing.T) {
	f := newFakeS3()
	// Empty bucket so the daily upload succeeds, but HeadObject fails when the
	// monthly backup existence is checked.
	f.headErr = errors.New("AccessDenied")
	h := runHandler(t, f, staticDump([]byte("data")), 7)

	if _, err := h.Run(context.Background(), false); err == nil {
		t.Fatal("expected error from periodic backup check, got nil")
	}
}

func TestRunCleanupDeleteErrorIsNonFatal(t *testing.T) {
	f := newFakeS3()
	f.seed("daily/2026-05-01-backup.sql", []byte("old"), testNow.Add(-100*time.Hour))
	f.deleteErr = errors.New("delete denied")
	h := runHandler(t, f, staticDump([]byte("fresh")), 7)

	// A delete failure during cleanup is logged but must not fail the run.
	res, err := h.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("cleanup delete error should be non-fatal, got: %v", err)
	}
	if res.Action != "created" {
		t.Errorf("action = %q, want created", res.Action)
	}
	if _, ok := f.objects["daily/2026-05-01-backup.sql"]; !ok {
		t.Error("object should remain after failed delete")
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	h := New(Config{S3: newFakeS3(), Bucket: "b"})
	if h.retentionDays != 7 {
		t.Errorf("retentionDays = %d, want default 7", h.retentionDays)
	}
	if h.dump == nil {
		t.Error("dump should default to PgDump, got nil")
	}
}
