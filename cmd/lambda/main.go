// Command lambda is the AWS Lambda entry point for go-postgres-s3-backup. It
// wires environment configuration to the backup package and starts the Lambda
// runtime; all backup logic lives in the importable backup package.
package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/joho/godotenv"

	"github.com/nicobistolfi/go-postgres-s3-backup/backup"
)

// Build information, set via -ldflags at release time by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	log.Printf("go-postgres-s3-backup %s (commit %s, built %s)", version, commit, date)

	// Load .env for local development.
	_ = godotenv.Load()

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatalf("unable to load SDK config: %v", err)
	}

	bucket := os.Getenv("BACKUP_BUCKET")
	if bucket == "" {
		log.Fatalf("BACKUP_BUCKET environment variable not set")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatalf("DATABASE_URL environment variable not set")
	}
	db, err := backup.ParseDatabaseURL(dbURL)
	if err != nil {
		log.Fatalf("failed to parse DATABASE_URL: %v", err)
	}

	handler := backup.New(backup.Config{
		S3:            s3.NewFromConfig(cfg),
		Bucket:        bucket,
		Database:      db,
		RetentionDays: retentionDays(),
	})

	events := backup.NewEventHandler(handler, os.Getenv("API_KEY"))
	lambda.Start(events.Dispatch)
}

// retentionDays reads DAILY_BACKUP_RETENTION_DAYS, defaulting to 7 when unset
// or invalid.
func retentionDays() int {
	v := os.Getenv("DAILY_BACKUP_RETENTION_DAYS")
	if v == "" {
		return 7
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n
	}
	log.Printf("Warning: invalid DAILY_BACKUP_RETENTION_DAYS value %q, using default 7", v)
	return 7
}
