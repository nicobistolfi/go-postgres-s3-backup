package backup

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestHandler(s3 S3API, retention int) *Handler {
	h := New(Config{
		S3:            s3,
		Bucket:        "test-bucket",
		Database:      DatabaseConfig{Host: "localhost"},
		RetentionDays: retention,
		Dump:          staticDump([]byte("dump")),
	})
	h.now = fixedClock(time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC))
	return h
}

func TestObjectChecksumFromMetadata(t *testing.T) {
	f := newFakeS3()
	body := []byte("hello")
	f.seed("daily/x.sql", body, time.Now())
	h := newTestHandler(f, 7)

	got, err := h.objectChecksum(context.Background(), "daily/x.sql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != checksum(body) {
		t.Errorf("objectChecksum = %q, want %q", got, checksum(body))
	}
}

func TestObjectChecksumDownloadFallback(t *testing.T) {
	f := newFakeS3()
	body := []byte("no-metadata-body")
	// Store without a sha256 metadata entry to force the download path.
	f.objects["daily/y.sql"] = &fakeObject{body: body, metadata: map[string]string{}}
	h := newTestHandler(f, 7)

	got, err := h.objectChecksum(context.Background(), "daily/y.sql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != checksum(body) {
		t.Errorf("objectChecksum (download) = %q, want %q", got, checksum(body))
	}
}

func TestObjectExists(t *testing.T) {
	f := newFakeS3()
	f.seed("daily/present.sql", []byte("x"), time.Now())
	h := newTestHandler(f, 7)
	ctx := context.Background()

	if ok, err := h.objectExists(ctx, "daily/present.sql"); err != nil || !ok {
		t.Errorf("present object: ok=%v err=%v, want ok=true err=nil", ok, err)
	}
	if ok, err := h.objectExists(ctx, "daily/absent.sql"); err != nil || ok {
		t.Errorf("absent object: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestObjectChecksumDownloadError(t *testing.T) {
	f := newFakeS3()
	// Object exists (Head succeeds) but has no sha256 metadata, and the
	// download to compute it fails.
	f.objects["daily/z.sql"] = &fakeObject{body: []byte("x"), metadata: map[string]string{}}
	f.getErr = errors.New("download failed")
	h := newTestHandler(f, 7)

	if _, err := h.objectChecksum(context.Background(), "daily/z.sql"); err == nil {
		t.Fatal("expected error from objectChecksum download, got nil")
	}
}

func TestObjectExistsError(t *testing.T) {
	f := newFakeS3()
	f.headErr = errors.New("AccessDenied: nope")
	h := newTestHandler(f, 7)

	if _, err := h.objectExists(context.Background(), "daily/x.sql"); err == nil {
		t.Fatal("expected error from objectExists, got nil")
	}
}

func TestMostRecentBackup(t *testing.T) {
	f := newFakeS3()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	f.seed("daily/old.sql", []byte("a"), base)
	f.seed("daily/new.sql", []byte("b"), base.Add(48*time.Hour))
	f.seed("daily/mid.sql", []byte("c"), base.Add(24*time.Hour))
	h := newTestHandler(f, 7)

	got, err := h.mostRecentBackup(context.Background(), "daily/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "daily/new.sql" {
		t.Errorf("mostRecentBackup = %q, want daily/new.sql", got)
	}
}

func TestMostRecentBackupEmpty(t *testing.T) {
	h := newTestHandler(newFakeS3(), 7)
	got, err := h.mostRecentBackup(context.Background(), "daily/")
	if err != nil || got != "" {
		t.Errorf("empty bucket: got=%q err=%v, want empty/nil", got, err)
	}
}
