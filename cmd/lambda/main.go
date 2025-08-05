package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/joho/godotenv"
)

type BackupHandler struct {
	s3Client *s3.Client
	bucket   string
	dbConfig DatabaseConfig
}

type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

func NewBackupHandler() (*BackupHandler, error) {
	// Load .env file for local development
	_ = godotenv.Load()

	// Initialize AWS config
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	// Get environment variables
	bucket := os.Getenv("BACKUP_BUCKET")
	if bucket == "" {
		return nil, fmt.Errorf("BACKUP_BUCKET environment variable not set")
	}

	dbConnStr := os.Getenv("DATABASE_URL")
	if dbConnStr == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	// Parse database URL
	dbConfig, err := parseDatabaseURL(dbConnStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DATABASE_URL: %w", err)
	}

	return &BackupHandler{
		s3Client: s3.NewFromConfig(cfg),
		bucket:   bucket,
		dbConfig: dbConfig,
	}, nil
}

func parseDatabaseURL(dbURL string) (DatabaseConfig, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return DatabaseConfig{}, err
	}

	password, _ := u.User.Password()
	
	// Default port to 5432 if not specified
	port := u.Port()
	if port == "" {
		port = "5432"
	}

	// Remove leading slash from path to get database name
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		database = "postgres"
	}

	return DatabaseConfig{
		Host:     u.Hostname(),
		Port:     port,
		User:     u.User.Username(),
		Password: password,
		Database: database,
	}, nil
}

func (h *BackupHandler) HandleRequest(ctx context.Context) error {
	log.Println("Starting database backup...")

	// Create backup using pg_dump
	backupData, err := h.createBackup(ctx)
	if err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Get current date
	now := time.Now()
	year := now.Format("2006")
	month := now.Format("2006-01")
	day := now.Format("2006-01-02")

	// Calculate checksum of the new backup
	newChecksum := h.calculateChecksum(backupData)

	// Find the most recent daily backup to compare against
	mostRecentBackup, err := h.findMostRecentBackup(ctx, "daily/")
	if err != nil {
		log.Printf("Warning: couldn't find most recent backup: %v", err)
	}

	// Check if content has changed from the most recent backup
	contentChanged := true
	if mostRecentBackup != "" {
		existingChecksum, err := h.getObjectChecksum(ctx, mostRecentBackup)
		if err == nil && existingChecksum == newChecksum {
			contentChanged = false
			log.Printf("Backup content unchanged from %s, skipping all uploads", mostRecentBackup)
		}
	}

	// Upload daily backup only if content changed
	dailyKey := fmt.Sprintf("daily/%s-backup.sql", day)
	if contentChanged {
		if err := h.uploadToS3WithChecksum(ctx, dailyKey, backupData, newChecksum); err != nil {
			return fmt.Errorf("failed to upload daily backup: %w", err)
		}
		log.Printf("Daily backup uploaded: %s", dailyKey)
	} else {
		// Even though content hasn't changed, we might want to update the timestamp
		// by creating a new file with today's date pointing to the same content
		return nil // Skip all uploads if content hasn't changed
	}

	// Only create monthly/yearly backups if content changed
	if contentChanged {
		// Check and create monthly backup if needed
		monthlyKey := fmt.Sprintf("monthly/%s-backup.sql", month)
		if exists, err := h.objectExists(ctx, monthlyKey); err != nil {
			return fmt.Errorf("failed to check monthly backup: %w", err)
		} else if !exists {
			if err := h.uploadToS3WithChecksum(ctx, monthlyKey, backupData, newChecksum); err != nil {
				return fmt.Errorf("failed to upload monthly backup: %w", err)
			}
			log.Printf("Monthly backup created: %s", monthlyKey)
		}

		// Check and create yearly backup if needed
		yearlyKey := fmt.Sprintf("yearly/%s-backup.sql", year)
		if exists, err := h.objectExists(ctx, yearlyKey); err != nil {
			return fmt.Errorf("failed to check yearly backup: %w", err)
		} else if !exists {
			if err := h.uploadToS3WithChecksum(ctx, yearlyKey, backupData, newChecksum); err != nil {
				return fmt.Errorf("failed to upload yearly backup: %w", err)
			}
			log.Printf("Yearly backup created: %s", yearlyKey)
		}
	}

	// Clean up old daily backups (keep only last 7 days)
	if err := h.cleanupOldDailyBackups(ctx); err != nil {
		log.Printf("Warning: failed to clean up old daily backups: %v", err)
	}

	log.Println("Backup process completed successfully")
	return nil
}

func (h *BackupHandler) createBackup(ctx context.Context) ([]byte, error) {
	// Debug: Log current environment
	log.Printf("Current PATH: %s", os.Getenv("PATH"))
	log.Printf("Current LD_LIBRARY_PATH: %s", os.Getenv("LD_LIBRARY_PATH"))
	
	// Set PATH to include layer binaries (note: layer creates /opt/opt/bin structure)
	os.Setenv("PATH", "/opt/opt/bin:"+os.Getenv("PATH"))
	
	// Set library path for shared libraries
	os.Setenv("LD_LIBRARY_PATH", "/opt/opt/lib:"+os.Getenv("LD_LIBRARY_PATH"))
	
	// Debug: Log updated environment
	log.Printf("Updated PATH: %s", os.Getenv("PATH"))
	log.Printf("Updated LD_LIBRARY_PATH: %s", os.Getenv("LD_LIBRARY_PATH"))
	
	// Debug: Check what's in /opt
	if entries, err := os.ReadDir("/opt"); err == nil {
		log.Printf("Contents of /opt: %v", entries)
		for _, entry := range entries {
			if entry.IsDir() {
				if subEntries, subErr := os.ReadDir("/opt/" + entry.Name()); subErr == nil {
					log.Printf("Contents of /opt/%s: %v", entry.Name(), subEntries)
				}
			}
		}
	} else {
		log.Printf("Error reading /opt directory: %v", err)
	}
	
	// Set PostgreSQL password via environment
	os.Setenv("PGPASSWORD", h.dbConfig.Password)
	
	// Check if pg_dump binary exists
	pgDumpPath := "/opt/opt/bin/pg_dump"
	if _, err := os.Stat(pgDumpPath); os.IsNotExist(err) {
		// Try to find it in PATH
		var lookupErr error
		pgDumpPath, lookupErr = exec.LookPath("pg_dump")
		if lookupErr != nil {
			return nil, fmt.Errorf("pg_dump binary not found in /opt/opt/bin or PATH: %w", lookupErr)
		}
	}
	
	log.Printf("Using pg_dump at: %s", pgDumpPath)
	
	// Build pg_dump command with full path
	// Note: PostgreSQL 14 supports SCRAM auth and modern options
	cmd := exec.CommandContext(ctx, pgDumpPath,
		"-h", h.dbConfig.Host,
		"-p", h.dbConfig.Port,
		"-U", h.dbConfig.User,
		"-d", h.dbConfig.Database,
		"--verbose",
		"--no-owner",
		"--no-privileges",
		"--clean",
		"--if-exists",
		"--exclude-schema=supabase_migrations",
		"--no-comments",
	)
	
	// Capture output
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	// Run pg_dump
	log.Println("Executing pg_dump...")
	err := cmd.Run()
	
	// Log stderr (pg_dump writes progress info to stderr)
	if stderr.Len() > 0 {
		log.Printf("pg_dump stderr: %s", stderr.String())
	}
	
	if err != nil {
		return nil, fmt.Errorf("pg_dump failed: %w\nstderr: %s", err, stderr.String())
	}
	
	backupData := stdout.Bytes()
	
	// Remove timestamp comments that cause unnecessary duplicates
	backupData = h.removeTimestampComments(backupData)
	
	log.Printf("Backup created successfully, size: %d bytes", len(backupData))
	
	return backupData, nil
}

func (h *BackupHandler) removeTimestampComments(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	var filtered [][]byte
	
	for _, line := range lines {
		// Skip lines that start with "-- Started on" or "-- Completed on"
		if bytes.HasPrefix(line, []byte("-- Started on ")) ||
			bytes.HasPrefix(line, []byte("-- Completed on ")) {
			continue
		}
		filtered = append(filtered, line)
	}
	
	return bytes.Join(filtered, []byte("\n"))
}

func (h *BackupHandler) findMostRecentBackup(ctx context.Context, prefix string) (string, error) {
	resp, err := h.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(h.bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return "", err
	}
	
	if len(resp.Contents) == 0 {
		return "", nil
	}
	
	// Find the most recent backup by LastModified time
	var mostRecent types.Object
	var found bool
	for _, obj := range resp.Contents {
		if !found || obj.LastModified.After(*mostRecent.LastModified) {
			mostRecent = obj
			found = true
		}
	}
	
	if found {
		return *mostRecent.Key, nil
	}
	
	return "", nil
}

func (h *BackupHandler) calculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (h *BackupHandler) getObjectChecksum(ctx context.Context, key string) (string, error) {
	// Try to get checksum from object metadata
	resp, err := h.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	
	// Check if we stored the checksum in metadata
	if resp.Metadata != nil {
		if checksum, ok := resp.Metadata["sha256"]; ok {
			return checksum, nil
		}
	}
	
	// If no checksum in metadata, we need to download and calculate
	// This is for backwards compatibility with existing backups
	getResp, err := h.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	defer getResp.Body.Close()
	
	hash := sha256.New()
	if _, err := io.Copy(hash, getResp.Body); err != nil {
		return "", err
	}
	
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (h *BackupHandler) uploadIfChanged(ctx context.Context, key string, data []byte, newChecksum string) (bool, error) {
	// Try to get existing checksum
	existingChecksum, err := h.getObjectChecksum(ctx, key)
	if err != nil {
		// If object doesn't exist, upload it
		if strings.Contains(err.Error(), "NotFound") {
			return true, h.uploadToS3WithChecksum(ctx, key, data, newChecksum)
		}
		// For other errors, still try to upload
		log.Printf("Warning: couldn't get checksum for %s: %v", key, err)
	}
	
	// Compare checksums
	if existingChecksum == newChecksum {
		return false, nil // No upload needed
	}
	
	// Upload with checksum
	return true, h.uploadToS3WithChecksum(ctx, key, data, newChecksum)
}

func (h *BackupHandler) uploadToS3WithChecksum(ctx context.Context, key string, data []byte, checksum string) error {
	_, err := h.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/sql"),
		Metadata: map[string]string{
			"sha256": checksum,
		},
	})
	return err
}

func (h *BackupHandler) uploadToS3(ctx context.Context, key string, data []byte) error {
	_, err := h.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/sql"),
	})
	return err
}

func (h *BackupHandler) objectExists(ctx context.Context, key string) (bool, error) {
	_, err := h.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Check if it's a "not found" error
		if strings.Contains(err.Error(), "NotFound") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (h *BackupHandler) cleanupOldDailyBackups(ctx context.Context) error {
	// List all daily backups
	resp, err := h.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(h.bucket),
		Prefix: aws.String("daily/"),
	})
	if err != nil {
		return fmt.Errorf("failed to list daily backups: %w", err)
	}

	// Calculate cutoff date (7 days ago)
	cutoff := time.Now().AddDate(0, 0, -7)

	// Delete old backups
	for _, obj := range resp.Contents {
		// Extract date from key (format: daily/YYYY-MM-DD-backup.sql)
		parts := strings.Split(*obj.Key, "/")
		if len(parts) != 2 {
			continue
		}
		
		datePart := strings.TrimSuffix(parts[1], "-backup.sql")
		backupDate, err := time.Parse("2006-01-02", datePart)
		if err != nil {
			log.Printf("Warning: failed to parse date from key %s: %v", *obj.Key, err)
			continue
		}

		if backupDate.Before(cutoff) {
			_, err := h.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(h.bucket),
				Key:    obj.Key,
			})
			if err != nil {
				log.Printf("Warning: failed to delete old backup %s: %v", *obj.Key, err)
			} else {
				log.Printf("Deleted old daily backup: %s", *obj.Key)
			}
		}
	}

	return nil
}

func main() {
	handler, err := NewBackupHandler()
	if err != nil {
		log.Fatalf("Failed to initialize handler: %v", err)
	}

	lambda.Start(func(ctx context.Context) error {
		return handler.HandleRequest(ctx)
	})
}