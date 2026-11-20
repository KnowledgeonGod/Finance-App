package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"finance-tracker/internal/config"
)

func Connect(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	poolConfig, err := newPoolConfig(cfg)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL connection pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return pool, nil
}

func newPoolConfig(cfg config.Config) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	if cfg.Environment == "production" &&
		(poolConfig.ConnConfig.TLSConfig == nil || poolConfig.ConnConfig.TLSConfig.InsecureSkipVerify) {
		return nil, errors.New("PostgreSQL TLS certificate verification must be enabled in production")
	}
	poolConfig.MaxConns = cfg.DBMaxConns
	poolConfig.MinConns = cfg.DBMinConns
	poolConfig.ConnConfig.ConnectTimeout = cfg.DBConnectTimeout
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = cfg.DBStatementTimeout.String()
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "finance-tracker-api"
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	return poolConfig, nil
}
