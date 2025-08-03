package main

import (
	"bytes"
	"context"
	"fmt"
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

	// Upload daily backup
	dailyKey := fmt.Sprintf("daily/%s-backup.sql", day)
	if err := h.uploadToS3(ctx, dailyKey, backupData); err != nil {
		return fmt.Errorf("failed to upload daily backup: %w", err)
	}
	log.Printf("Daily backup uploaded: %s", dailyKey)

	// Check and create monthly backup if needed
	monthlyKey := fmt.Sprintf("monthly/%s-backup.sql", month)
	if exists, err := h.objectExists(ctx, monthlyKey); err != nil {
		return fmt.Errorf("failed to check monthly backup: %w", err)
	} else if !exists {
		if err := h.uploadToS3(ctx, monthlyKey, backupData); err != nil {
			return fmt.Errorf("failed to upload monthly backup: %w", err)
		}
		log.Printf("Monthly backup created: %s", monthlyKey)
	}

	// Check and create yearly backup if needed
	yearlyKey := fmt.Sprintf("yearly/%s-backup.sql", year)
	if exists, err := h.objectExists(ctx, yearlyKey); err != nil {
		return fmt.Errorf("failed to check yearly backup: %w", err)
	} else if !exists {
		if err := h.uploadToS3(ctx, yearlyKey, backupData); err != nil {
			return fmt.Errorf("failed to upload yearly backup: %w", err)
		}
		log.Printf("Yearly backup created: %s", yearlyKey)
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
	log.Printf("Backup created successfully, size: %d bytes", len(backupData))
	
	return backupData, nil
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