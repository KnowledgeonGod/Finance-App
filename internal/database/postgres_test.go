package database

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"finance-tracker/internal/config"
)

func testConfig() config.Config {
	return config.Config{
		DatabaseURL:        "postgres://localhost/finance?sslmode=verify-full",
		Environment:        "production",
		DBMaxConns:         24,
		DBMinConns:         4,
		DBConnectTimeout:   3 * time.Second,
		DBStatementTimeout: 8 * time.Second,
	}
}

func TestNewPoolConfigAppliesProductionSettings(t *testing.T) {
	poolConfig, err := newPoolConfig(testConfig())
	if err != nil {
		t.Fatalf("newPoolConfig: %v", err)
	}
	if poolConfig.MaxConns != 24 || poolConfig.MinConns != 4 {
		t.Fatalf("pool connections = %d/%d, want 24/4", poolConfig.MinConns, poolConfig.MaxConns)
	}
	if got := poolConfig.ConnConfig.RuntimeParams["statement_timeout"]; got != "8s" {
		t.Errorf("statement_timeout = %q, want 8s", got)
	}
	if got := poolConfig.ConnConfig.RuntimeParams["application_name"]; got != "finance-tracker-api" {
		t.Errorf("application_name = %q", got)
	}
	if poolConfig.ConnConfig.TLSConfig == nil || poolConfig.ConnConfig.TLSConfig.InsecureSkipVerify {
		t.Fatal("production config must verify the PostgreSQL server certificate")
	}
}

func TestNewPoolConfigRejectsInvalidOrUnverifiedProductionConnections(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "invalid URL", url: "not a database URL", want: "DATABASE_URL is invalid"},
		{name: "TLS disabled", url: "postgres://localhost/finance?sslmode=disable", want: "TLS certificate verification"},
		{name: "TLS not verified", url: "postgres://localhost/finance?sslmode=require", want: "TLS certificate verification"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.DatabaseURL = test.url
			_, err := newPoolConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("newPoolConfig error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNewPoolConfigAllowsDevelopmentWithoutTLS(t *testing.T) {
	cfg := testConfig()
	cfg.Environment = "development"
	cfg.DatabaseURL = "postgres://localhost/finance?sslmode=disable"

	poolConfig, err := newPoolConfig(cfg)
	if err != nil {
		t.Fatalf("newPoolConfig: %v", err)
	}
	if poolConfig.ConnConfig.TLSConfig != nil {
		t.Fatal("development sslmode=disable should not configure TLS")
	}
}

func TestConnectReturnsAnErrorWhenStartupContextIsCanceled(t *testing.T) {
	cfg := testConfig()
	cfg.Environment = "development"
	cfg.DatabaseURL = "postgres://127.0.0.1:1/finance?sslmode=disable"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pool, err := Connect(ctx, cfg)
	if pool != nil {
		pool.Close()
		t.Fatal("Connect returned a pool for an already-canceled startup context")
	}
	if err == nil || !strings.Contains(err.Error(), "connect to PostgreSQL") {
		t.Fatalf("Connect error = %v, want canceled connection error", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect error = %v, want context.Canceled", err)
	}
}
