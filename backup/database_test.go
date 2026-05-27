package backup

import "testing"

func TestParseDatabaseURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want DatabaseConfig
	}{
		{
			name: "full",
			url:  "postgresql://alice:secret@db.example.com:6543/shop",
			want: DatabaseConfig{Host: "db.example.com", Port: "6543", User: "alice", Password: "secret", Database: "shop"},
		},
		{
			name: "default port and database",
			url:  "postgresql://bob@localhost/",
			want: DatabaseConfig{Host: "localhost", Port: "5432", User: "bob", Password: "", Database: "postgres"},
		},
		{
			name: "no user info",
			url:  "postgres://example.org:5432/mydb",
			want: DatabaseConfig{Host: "example.org", Port: "5432", User: "", Password: "", Database: "mydb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDatabaseURL(tt.url)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseDatabaseURL(%q) = %+v, want %+v", tt.url, got, tt.want)
			}
		})
	}
}

func TestParseDatabaseURLError(t *testing.T) {
	if _, err := ParseDatabaseURL("postgres://%zz"); err == nil {
		t.Fatal("expected error for malformed URL, got nil")
	}
}
