package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Environment        string
	HTTPAddr           string
	DatabaseURL        string
	PublicBaseURL      string
	RedisURL           string
	BrevoAPIKey        string
	MailFrom           string
	DBMaxConns         int32
	DBMinConns         int32
	DBConnectTimeout   time.Duration
	DBStatementTimeout time.Duration
	HTTPReadTimeout    time.Duration
	HTTPWriteTimeout   time.Duration
	HTTPIdleTimeout    time.Duration
	ShutdownTimeout    time.Duration
}

func Load() (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	cfg := Config{
		Environment:        strings.ToLower(env("APP_ENV", "development")),
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		DatabaseURL:        strings.TrimSpace(os.Getenv("DATABASE_URL")),
		PublicBaseURL:      env("PUBLIC_BASE_URL", "http://localhost:8080"),
		RedisURL:           strings.TrimSpace(os.Getenv("RATE_LIMIT_REDIS_URL")),
		BrevoAPIKey:        strings.TrimSpace(os.Getenv("BREVO_API_KEY")),
		MailFrom:           strings.TrimSpace(os.Getenv("MAIL_FROM")),
		DBMaxConns:         20,
		DBMinConns:         2,
		DBConnectTimeout:   5 * time.Second,
		DBStatementTimeout: 10 * time.Second,
		HTTPReadTimeout:    10 * time.Second,
		HTTPWriteTimeout:   15 * time.Second,
		HTTPIdleTimeout:    60 * time.Second,
		ShutdownTimeout:    15 * time.Second,
	}

	if cfg.Environment != "development" && cfg.Environment != "test" && cfg.Environment != "production" {
		return Config{}, errors.New("APP_ENV must be development, test, or production")
	}
	if cfg.HTTPAddr == "" {
		return Config{}, errors.New("HTTP_ADDR must not be empty")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if err := validatePublicBaseURL(cfg.PublicBaseURL); err != nil {
		return Config{}, err
	}
	if err := cfg.loadBrevoSettings(); err != nil {
		return Config{}, err
	}
	if max, err := positiveInt32("DB_MAX_CONNS", "20"); err != nil {
		return Config{}, err
	} else {
		cfg.DBMaxConns = max
	}
	if min, err := nonNegativeInt32("DB_MIN_CONNS", "2"); err != nil {
		return Config{}, err
	} else {
		cfg.DBMinConns = min
	}
	if cfg.DBMinConns > cfg.DBMaxConns {
		return Config{}, errors.New("DB_MIN_CONNS must not exceed DB_MAX_CONNS")
	}
	connectTimeout, err := positiveDuration("DB_CONNECT_TIMEOUT", "5s")
	if err != nil {
		return Config{}, err
	}
	cfg.DBConnectTimeout = connectTimeout
	statementTimeout, err := positiveDuration("DB_STATEMENT_TIMEOUT", "10s")
	if err != nil {
		return Config{}, err
	}
	cfg.DBStatementTimeout = statementTimeout
	return cfg, nil
}

func (c Config) ValidateAPI() error {
	if c.Environment != "production" {
		return nil
	}
	if c.RedisURL == "" {
		return errors.New("RATE_LIMIT_REDIS_URL is required in production")
	}
	redisURL, err := url.Parse(c.RedisURL)
	if err != nil || redisURL.Scheme != "rediss" || redisURL.Host == "" {
		return errors.New("RATE_LIMIT_REDIS_URL must use rediss:// in production")
	}
	baseURL, err := url.Parse(c.PublicBaseURL)
	if err != nil || baseURL.Scheme != "https" {
		return errors.New("PUBLIC_BASE_URL must use https:// in production")
	}
	if c.BrevoAPIKey == "" || c.MailFrom == "" {
		return errors.New("BREVO_API_KEY and MAIL_FROM are required in production")
	}
	return nil
}

func (c *Config) loadBrevoSettings() error {
	configured := c.BrevoAPIKey != "" || c.MailFrom != ""
	if !configured {
		return nil
	}
	if c.BrevoAPIKey == "" || c.MailFrom == "" {
		return errors.New("BREVO_API_KEY and MAIL_FROM must be configured together")
	}
	if strings.ContainsAny(c.MailFrom, "\r\n") {
		return errors.New("MAIL_FROM is invalid")
	}
	if _, err := mail.ParseAddress(c.MailFrom); err != nil {
		return errors.New("MAIL_FROM is invalid")
	}
	return nil
}

func validatePublicBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("PUBLIC_BASE_URL must be an absolute http:// or https:// URL without credentials, query, or fragment")
	}
	return nil
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func positiveInt32(key, fallback string) (int32, error) {
	value, err := strconv.ParseInt(env(key, fallback), 10, 32)
	if err != nil || value < 1 {
		return 0, errors.New(key + " must be a positive integer")
	}
	return int32(value), nil
}

func nonNegativeInt32(key, fallback string) (int32, error) {
	value, err := strconv.ParseInt(env(key, fallback), 10, 32)
	if err != nil || value < 0 {
		return 0, errors.New(key + " must be a non-negative integer")
	}
	return int32(value), nil
}

func positiveDuration(key, fallback string) (time.Duration, error) {
	value, err := time.ParseDuration(env(key, fallback))
	if err != nil || value <= 0 {
		return 0, errors.New(key + " must be a positive duration")
	}
	return value, nil
}
