package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed migrations/001_create_finance_tables.sql
var initialSchema string

//go:embed migrations/002_create_password_reset_tokens.sql
var passwordResetSchema string

//go:embed migrations/003_create_email_verification_tokens.sql
var emailVerificationSchema string

type queryPool interface {
	Begin(context.Context) (pgx.Tx, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func Migrate(ctx context.Context, pool queryPool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724198306)`); err != nil {
		return fmt.Errorf("lock schema migration: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}
	for _, migration := range []struct {
		version int
		schema  string
	}{
		{version: 1, schema: initialSchema},
		{version: 2, schema: passwordResetSchema},
		{version: 3, schema: emailVerificationSchema},
	} {
		var applied bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
			migration.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d history: %w", migration.version, err)
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, tx, migration.version, migration.schema); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func applyMigration(ctx context.Context, tx pgx.Tx, version int, schema string) error {
	for _, statement := range strings.Split(schema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("execute migration %d statement: %w", version, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version) VALUES ($1)`, version,
	); err != nil {
		return fmt.Errorf("record schema migration %d: %w", version, err)
	}
	return nil
}

func CheckSchema(ctx context.Context, pool queryPool) error {
	var applied bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM schema_migrations WHERE version = 3
		)
	`).Scan(&applied)
	if err != nil {
		return fmt.Errorf("check database schema version: %w", err)
	}
	if !applied {
		return fmt.Errorf("required database migration 3 has not been applied")
	}
	return nil
}

func CleanupExpiredSessions(ctx context.Context, pool queryPool, batchSize int) (int64, error) {
	if batchSize < 1 || batchSize > 10000 {
		return 0, errors.New("session cleanup batch size must be between 1 and 10000")
	}
	result, err := pool.Exec(ctx, `
		DELETE FROM sessions
		WHERE token_hash IN (
			SELECT token_hash
			FROM sessions
			WHERE expires_at <= now()
			ORDER BY expires_at
			LIMIT $1
		)
	`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return result.RowsAffected(), nil
}

func CleanupExpiredPasswordResetTokens(ctx context.Context, pool queryPool, batchSize int) (int64, error) {
	if batchSize < 1 || batchSize > 10000 {
		return 0, errors.New("password reset cleanup batch size must be between 1 and 10000")
	}
	result, err := pool.Exec(ctx, `
		DELETE FROM password_reset_tokens
		WHERE token_hash IN (
			SELECT token_hash
			FROM password_reset_tokens
			WHERE expires_at <= now()
			ORDER BY expires_at
			LIMIT $1
		)
	`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("delete expired password reset tokens: %w", err)
	}
	return result.RowsAffected(), nil
}

func CleanupExpiredEmailVerificationTokens(ctx context.Context, pool queryPool, batchSize int) (int64, error) {
	if batchSize < 1 || batchSize > 10000 {
		return 0, errors.New("email verification cleanup batch size must be between 1 and 10000")
	}
	result, err := pool.Exec(ctx, `
		DELETE FROM email_verification_tokens
		WHERE token_hash IN (
			SELECT token_hash
			FROM email_verification_tokens
			WHERE expires_at <= now()
			ORDER BY expires_at
			LIMIT $1
		)
	`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("delete expired email verification tokens: %w", err)
	}
	return result.RowsAffected(), nil
}
