package backup

import (
	"net/url"
	"strings"
)

// DatabaseConfig holds the connection parameters used to invoke pg_dump.
type DatabaseConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// ParseDatabaseURL parses a PostgreSQL connection string (for example
// "postgresql://user:pass@host:5432/dbname") into a DatabaseConfig. The port
// defaults to 5432 and the database name defaults to "postgres" when absent.
func ParseDatabaseURL(dbURL string) (DatabaseConfig, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return DatabaseConfig{}, err
	}

	password, _ := u.User.Password()

	port := u.Port()
	if port == "" {
		port = "5432"
	}

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
