// Package backup creates PostgreSQL dumps with pg_dump and stores them in S3
// on a daily/monthly/yearly rotation. It deduplicates unchanged dumps by
// SHA-256, prunes daily backups past a retention window, and can be driven
// either on a schedule or on demand through an authenticated HTTP endpoint.
package backup

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Dumper produces a SQL dump of the given database. The default implementation
// is PgDump; tests inject their own.
type Dumper func(ctx context.Context, db DatabaseConfig) ([]byte, error)

// Config configures a Handler.
type Config struct {
	S3            S3API          // S3 client (required)
	Bucket        string         // destination bucket (required)
	Database      DatabaseConfig // database to dump (required)
	RetentionDays int            // daily backups to keep; <= 0 means 7
	Dump          Dumper         // dump implementation; nil means PgDump
}

// Handler runs backups against a bucket and database.
type Handler struct {
	s3            S3API
	bucket        string
	db            DatabaseConfig
	retentionDays int
	dump          Dumper
	now           func() time.Time
}

// New builds a Handler from cfg, applying defaults for RetentionDays (7) and
// Dump (PgDump).
func New(cfg Config) *Handler {
	dump := cfg.Dump
	if dump == nil {
		dump = PgDump
	}
	retention := cfg.RetentionDays
	if retention <= 0 {
		retention = 7
	}
	return &Handler{
		s3:            cfg.S3,
		bucket:        cfg.Bucket,
		db:            cfg.Database,
		retentionDays: retention,
		dump:          dump,
		now:           time.Now,
	}
}

// Result summarizes a single backup run.
type Result struct {
	Status     string `json:"status"`      // always "ok" on success
	Action     string `json:"action"`      // "created" or "skipped"
	Reason     string `json:"reason"`      // why the daily backup was created/skipped
	Key        string `json:"key"`         // today's daily backup S3 key
	Size       string `json:"size"`        // human-readable dump size (e.g. "12.34 MB")
	SizeBytes  int    `json:"size_bytes"`  // size of the dump in bytes
	DurationMs int64  `json:"duration_ms"` // wall-clock time of the run
}

// Run produces a dump and stores it. A normal run stores the daily backup only
// when the dump differs from the most recent daily backup. When force is true
// (a manual invocation) it stores today's backup even if it matches an older
// one, but still skips rewriting today's file when that file is already
// identical. Monthly and yearly backups are created when missing, and daily
// backups older than the retention window are pruned.
func (h *Handler) Run(ctx context.Context, force bool) (*Result, error) {
	start := h.now()
	log.Println("Starting database backup...")

	raw, err := h.dump(ctx, h.db)
	if err != nil {
		return nil, fmt.Errorf("failed to create backup: %w", err)
	}
	data := removeTimestampComments(raw)
	sum := checksum(data)
	log.Printf("Backup created, size: %d bytes", len(data))

	now := h.now()
	dailyKey := fmt.Sprintf("daily/%s-backup.sql", now.Format("2006-01-02"))
	result := &Result{
		Status:    "ok",
		Key:       dailyKey,
		Size:      HumanizeSize(len(data)),
		SizeBytes: len(data),
	}

	upload, reason := h.decideDailyUpload(ctx, dailyKey, sum, force)
	result.Reason = reason
	if !upload {
		log.Printf("Skipping daily backup upload: %s", reason)
		result.Action = "skipped"
		result.DurationMs = h.elapsed(start)
		return result, nil
	}

	if err := h.upload(ctx, dailyKey, data, sum); err != nil {
		return nil, fmt.Errorf("failed to upload daily backup: %w", err)
	}
	log.Printf("Daily backup uploaded: %s", dailyKey)
	result.Action = "created"

	if err := h.createPeriodicBackups(ctx, now, data, sum); err != nil {
		return nil, err
	}

	if err := h.cleanupOldDailyBackups(ctx); err != nil {
		log.Printf("Warning: failed to clean up old daily backups: %v", err)
	}

	log.Println("Backup process completed successfully")
	result.DurationMs = h.elapsed(start)
	return result, nil
}

// decideDailyUpload determines whether today's daily backup should be written
// and why. A normal run stores it only when the dump differs from the most
// recent daily backup; a forced run stores it unless today's file is already
// identical.
func (h *Handler) decideDailyUpload(ctx context.Context, dailyKey, sum string, force bool) (upload bool, reason string) {
	mostRecent, err := h.mostRecentBackup(ctx, "daily/")
	if err != nil {
		log.Printf("Warning: couldn't find most recent backup: %v", err)
	}

	contentChanged := mostRecent == "" || !h.objectMatches(ctx, mostRecent, sum)
	if contentChanged {
		return true, "content changed"
	}
	switch {
	case !force:
		return false, "unchanged"
	case h.objectMatches(ctx, dailyKey, sum):
		return false, "today's backup already identical"
	default:
		return true, "forced; matched an older backup"
	}
}

// createPeriodicBackups creates the monthly and yearly backups for now if they
// do not already exist.
func (h *Handler) createPeriodicBackups(ctx context.Context, now time.Time, data []byte, sum string) error {
	monthlyKey := fmt.Sprintf("monthly/%s-backup.sql", now.Format("2006-01"))
	if created, err := h.uploadIfMissing(ctx, monthlyKey, data, sum); err != nil {
		return err
	} else if created {
		log.Printf("Monthly backup created: %s", monthlyKey)
	}

	yearlyKey := fmt.Sprintf("yearly/%s-backup.sql", now.Format("2006"))
	if created, err := h.uploadIfMissing(ctx, yearlyKey, data, sum); err != nil {
		return err
	} else if created {
		log.Printf("Yearly backup created: %s", yearlyKey)
	}
	return nil
}

func (h *Handler) elapsed(start time.Time) int64 {
	return h.now().Sub(start).Milliseconds()
}
