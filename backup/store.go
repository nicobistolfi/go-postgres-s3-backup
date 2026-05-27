package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3API is the subset of the AWS S3 client used by Handler. It is satisfied by
// *s3.Client and allows the storage layer to be mocked in tests.
type S3API interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// checksum returns the hex-encoded SHA-256 of data.
func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mostRecentBackup returns the key of the most recently modified object under
// prefix, or "" when none exist.
func (h *Handler) mostRecentBackup(ctx context.Context, prefix string) (string, error) {
	resp, err := h.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(h.bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		return "", err
	}

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

// objectChecksum returns the SHA-256 of the object at key, preferring the value
// stored in object metadata and falling back to downloading and hashing the
// body for objects written before checksums were recorded.
func (h *Handler) objectChecksum(ctx context.Context, key string) (string, error) {
	resp, err := h.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}

	if sum, ok := resp.Metadata["sha256"]; ok {
		return sum, nil
	}

	getResp, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = getResp.Body.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, getResp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// objectMatches reports whether the object at key exists and its checksum
// equals the given checksum.
func (h *Handler) objectMatches(ctx context.Context, key, sum string) bool {
	existing, err := h.objectChecksum(ctx, key)
	return err == nil && existing == sum
}

// upload writes data to key, recording its checksum in object metadata.
func (h *Handler) upload(ctx context.Context, key string, data []byte, sum string) error {
	_, err := h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/sql"),
		Metadata:    map[string]string{"sha256": sum},
	})
	return err
}

// objectExists reports whether key exists in the bucket.
func (h *Handler) objectExists(ctx context.Context, key string) (bool, error) {
	_, err := h.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(h.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// uploadIfMissing writes data to key only when it does not already exist,
// returning whether it created the object.
func (h *Handler) uploadIfMissing(ctx context.Context, key string, data []byte, sum string) (bool, error) {
	exists, err := h.objectExists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("failed to check %s: %w", key, err)
	}
	if exists {
		return false, nil
	}
	if err := h.upload(ctx, key, data, sum); err != nil {
		return false, fmt.Errorf("failed to upload %s: %w", key, err)
	}
	return true, nil
}

// cleanupOldDailyBackups deletes daily backups older than the retention window.
// Keys are expected in the form "daily/YYYY-MM-DD-backup.sql"; unparseable keys
// are left untouched.
func (h *Handler) cleanupOldDailyBackups(ctx context.Context) error {
	resp, err := h.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(h.bucket),
		Prefix: aws.String("daily/"),
	})
	if err != nil {
		return fmt.Errorf("failed to list daily backups: %w", err)
	}

	cutoff := h.now().AddDate(0, 0, -h.retentionDays)
	for _, obj := range resp.Contents {
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
		if !backupDate.Before(cutoff) {
			continue
		}
		if _, err := h.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(h.bucket),
			Key:    obj.Key,
		}); err != nil {
			log.Printf("Warning: failed to delete old backup %s: %v", *obj.Key, err)
		} else {
			log.Printf("Deleted old daily backup: %s", *obj.Key)
		}
	}
	return nil
}
