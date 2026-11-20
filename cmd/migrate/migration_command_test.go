package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestRunRequiresDatabaseConfiguration(t *testing.T) {
	for _, key := range []string{
		"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "RATE_LIMIT_REDIS_URL",
		"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_CONNECT_TIMEOUT", "DB_STATEMENT_TIMEOUT",
	} {
		t.Setenv(key, "")
	}

	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("run started migrations without DATABASE_URL")
	}
}
