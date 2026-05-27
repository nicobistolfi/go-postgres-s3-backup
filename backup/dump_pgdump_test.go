package backup

import (
	"context"
	"testing"
)

func TestPgDumpBinaryNotFound(t *testing.T) {
	// Point PATH at a directory with no pg_dump so the lookup fails
	// deterministically regardless of what's installed on the host.
	t.Setenv("PATH", "/nonexistent-dir-for-test")

	_, err := PgDump(context.Background(), DatabaseConfig{Host: "localhost", Database: "x"})
	if err == nil {
		t.Fatal("expected error when pg_dump is not found, got nil")
	}
}
