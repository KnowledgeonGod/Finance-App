package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"finance-tracker/internal/config"
)

func clearAPIConfig(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "PUBLIC_BASE_URL", "RATE_LIMIT_REDIS_URL",
		"BREVO_API_KEY", "MAIL_FROM",
		"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_CONNECT_TIMEOUT", "DB_STATEMENT_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadAPIConfigValidatesRequiredSettings(t *testing.T) {
	clearAPIConfig(t)
	if _, err := loadAPIConfig(); err == nil {
		t.Fatal("loadAPIConfig accepted a missing database URL")
	}

	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://localhost/finance?sslmode=verify-full")
	if _, err := loadAPIConfig(); err == nil {
		t.Fatal("loadAPIConfig accepted production without shared Redis")
	}
}

func TestNewHTTPServerAppliesTimeoutsAndHandler(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	cfg := config.Config{
		HTTPAddr:         ":8088",
		HTTPReadTimeout:  9 * time.Second,
		HTTPWriteTimeout: 12 * time.Second,
		HTTPIdleTimeout:  45 * time.Second,
	}
	server := newHTTPServer(cfg, handler)

	if server.Addr != cfg.HTTPAddr || server.Handler == nil {
		t.Fatalf("server address/handler = %q/%v", server.Addr, server.Handler)
	}
	if server.ReadTimeout != cfg.HTTPReadTimeout ||
		server.WriteTimeout != cfg.HTTPWriteTimeout ||
		server.IdleTimeout != cfg.HTTPIdleTimeout {
		t.Fatalf("server timeouts do not match configuration: %+v", server)
	}
	if server.ReadHeaderTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatal("server is missing bounded header settings")
	}
}

func TestSessionCleanupWorkerRunsImmediatelyAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	done := make(chan struct{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clean := func(ctx context.Context, batchSize int) (int64, error) {
		calls++
		if batchSize != 1000 {
			t.Errorf("cleanup batch size = %d, want 1000", batchSize)
		}
		cancel()
		return 1000, nil
	}

	go cleanupExpiredSessions(ctx, clean, logger, done)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop after cancellation")
	}
	if calls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", calls)
	}
}

func TestSessionCleanupWorkerStopsAfterCleanupError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	done := make(chan struct{})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clean := func(context.Context, int) (int64, error) {
		calls++
		cancel()
		return 0, errors.New("simulated cleanup failure")
	}

	go cleanupExpiredSessions(ctx, clean, logger, done)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop after cancellation")
	}
	if calls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", calls)
	}
}
