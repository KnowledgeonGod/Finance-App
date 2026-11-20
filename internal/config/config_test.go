package config

import (
	"os"
	"path/filepath"
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "PUBLIC_BASE_URL", "RATE_LIMIT_REDIS_URL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
		"DB_MAX_CONNS", "DB_MIN_CONNS", "DB_CONNECT_TIMEOUT", "DB_STATEMENT_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	clearConfigEnv(t)
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a missing DATABASE_URL")
	}
}

func TestLoadReadsDotEnvWithoutOverwritingProcessEnvironment(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	t.Setenv("APP_ENV", "development")
	if err := os.Unsetenv("DATABASE_URL"); err != nil {
		t.Fatalf("unset database URL: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, ".env"), []byte("DATABASE_URL=postgres://dotenv/finance\n"), 0600); err != nil {
		t.Fatalf("write dotenv file: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with .env: %v", err)
	}
	if cfg.DatabaseURL != "postgres://dotenv/finance" {
		t.Fatalf("database URL from .env = %q", cfg.DatabaseURL)
	}

	t.Setenv("DATABASE_URL", "postgres://process/finance")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load with process environment: %v", err)
	}
	if cfg.DatabaseURL != "postgres://process/finance" {
		t.Fatalf("process database URL was overwritten: %q", cfg.DatabaseURL)
	}
}

func TestLoadRejectsInvalidPoolConfiguration(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/finance")
	t.Setenv("DB_MAX_CONNS", "2")
	t.Setenv("DB_MIN_CONNS", "3")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted DB_MIN_CONNS greater than DB_MAX_CONNS")
	}
}

func TestProductionRequiresTLSRedisURL(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://localhost/finance?sslmode=verify-full")
	t.Setenv("PUBLIC_BASE_URL", "https://finance.example.com")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("SMTP_FROM", "finance@example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.ValidateAPI(); err == nil {
		t.Fatal("ValidateAPI accepted production without Redis")
	}

	cfg.RedisURL = "redis://localhost:6379"
	if err := cfg.ValidateAPI(); err == nil {
		t.Fatal("ValidateAPI accepted a non-TLS Redis URL in production")
	}

	cfg.RedisURL = "rediss://localhost:6379"
	if err := cfg.ValidateAPI(); err != nil {
		t.Fatalf("ValidateAPI rejected TLS Redis URL: %v", err)
	}
	cfg.SMTPPassword = ""
	if err := cfg.ValidateAPI(); err == nil {
		t.Fatal("ValidateAPI accepted production without SMTP credentials")
	}
	cfg.SMTPPassword = "secret"
	cfg.PublicBaseURL = "http://finance.example.com"
	if err := cfg.ValidateAPI(); err == nil {
		t.Fatal("ValidateAPI accepted a non-TLS public URL in production")
	}
}

func TestLoadValidatesOptionalSMTPSettings(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/finance")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_USERNAME", "mailer")
	t.Setenv("SMTP_PASSWORD", "secret")
	t.Setenv("SMTP_FROM", "finance@example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with complete SMTP settings: %v", err)
	}
	if cfg.SMTPPort != 587 {
		t.Fatalf("SMTP port = %d, want default 587", cfg.SMTPPort)
	}

	t.Setenv("SMTP_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an out-of-range SMTP port")
	}
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_FROM", "invalid sender")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an invalid SMTP sender address")
	}
}

func TestLoadRejectsUnknownEnvironmentAndInvalidTimeouts(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/finance")
	t.Setenv("APP_ENV", "staging")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an unknown environment")
	}

	t.Setenv("APP_ENV", "test")
	t.Setenv("DB_CONNECT_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an invalid connection timeout")
	}

	t.Setenv("DB_CONNECT_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a nonpositive connection timeout")
	}
}

func TestLoadRejectsInvalidPoolAndAddressSettings(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://localhost/finance")
	t.Setenv("HTTP_ADDR", " ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with a blank optional address: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTP address = %q, want default :8080", cfg.HTTPAddr)
	}

	t.Setenv("HTTP_ADDR", ":8080")
	t.Setenv("DB_MAX_CONNS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a zero maximum connection count")
	}

	t.Setenv("DB_MAX_CONNS", "20")
	t.Setenv("DB_MIN_CONNS", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a negative minimum connection count")
	}
}
