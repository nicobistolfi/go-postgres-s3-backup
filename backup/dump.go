package backup

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
)

// PgDump produces a SQL dump of the given database by invoking the pg_dump
// binary. It is the default Dumper used by New. On AWS Lambda the binary ships
// in a layer mounted at /opt/opt/bin; elsewhere it is resolved from PATH.
func PgDump(ctx context.Context, db DatabaseConfig) ([]byte, error) {
	// The PostgreSQL layer mounts its tools under /opt/opt on Lambda.
	_ = os.Setenv("PATH", "/opt/opt/bin:"+os.Getenv("PATH"))
	_ = os.Setenv("LD_LIBRARY_PATH", "/opt/opt/lib:"+os.Getenv("LD_LIBRARY_PATH"))
	_ = os.Setenv("PGPASSWORD", db.Password)

	pgDumpPath := "/opt/opt/bin/pg_dump"
	if _, err := os.Stat(pgDumpPath); os.IsNotExist(err) {
		var lookupErr error
		pgDumpPath, lookupErr = exec.LookPath("pg_dump")
		if lookupErr != nil {
			return nil, fmt.Errorf("pg_dump binary not found in /opt/opt/bin or PATH: %w", lookupErr)
		}
	}

	cmd := exec.CommandContext(ctx, pgDumpPath,
		"-h", db.Host,
		"-p", db.Port,
		"-U", db.User,
		"-d", db.Database,
		"--verbose",
		"--no-owner",
		"--no-privileges",
		"--clean",
		"--if-exists",
		"--exclude-schema=supabase_migrations",
		"--no-comments",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Println("Executing pg_dump...")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pg_dump failed: %w\nstderr: %s", err, stderr.String())
	}
	if stderr.Len() > 0 {
		log.Printf("pg_dump stderr: %s", stderr.String())
	}

	return stdout.Bytes(), nil
}

// removeTimestampComments strips the "-- Started on" / "-- Completed on" lines
// pg_dump emits, which otherwise make byte-identical dumps appear to differ.
func removeTimestampComments(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	filtered := lines[:0]
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("-- Started on ")) ||
			bytes.HasPrefix(line, []byte("-- Completed on ")) {
			continue
		}
		filtered = append(filtered, line)
	}
	return bytes.Join(filtered, []byte("\n"))
}
