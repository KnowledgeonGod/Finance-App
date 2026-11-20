package store

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/pashagolub/pgxmock/v5"
)

func TestMigrateAppliesAndRecordsSchemaVersion(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(`SELECT pg_advisory_xact_lock\(724198306\)`).
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(1).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	for _, statement := range strings.Split(initialSchema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		pool.ExpectExec(regexp.QuoteMeta(statement)).
			WillReturnResult(pgxmock.NewResult("CREATE", 0))
	}
	pool.ExpectExec(`INSERT INTO schema_migrations \(version\) VALUES \(\$1\)`).
		WithArgs(1).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	for _, statement := range strings.Split(passwordResetSchema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		pool.ExpectExec(regexp.QuoteMeta(statement)).
			WillReturnResult(pgxmock.NewResult("CREATE", 0))
	}
	pool.ExpectExec(`INSERT INTO schema_migrations \(version\) VALUES \(\$1\)`).
		WithArgs(2).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(3).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	for _, statement := range strings.Split(emailVerificationSchema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		pool.ExpectExec(regexp.QuoteMeta(statement)).
			WillReturnResult(pgxmock.NewResult("CREATE", 0))
	}
	pool.ExpectExec(`INSERT INTO schema_migrations \(version\) VALUES \(\$1\)`).
		WithArgs(3).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestMigrateSkipsAnAppliedSchemaVersion(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(`SELECT pg_advisory_xact_lock\(724198306\)`).
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(1).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(3).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	pool.ExpectCommit()
	pool.ExpectRollback()

	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestMigrateRollsBackWhenASchemaStatementFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(`SELECT pg_advisory_xact_lock\(724198306\)`).
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(1).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	pool.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS users (")).
		WillReturnError(errors.New("simulated DDL failure"))
	pool.ExpectRollback()

	if err := Migrate(context.Background(), pool); err == nil {
		t.Fatal("Migrate succeeded despite a schema statement failure")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestMigrateReturnsBeginError(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin().WillReturnError(errors.New("simulated begin failure"))

	if err := Migrate(context.Background(), pool); err == nil {
		t.Fatal("Migrate hid a transaction begin error")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestMigrateRollsBackWhenRecordingVersionFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec(`SELECT pg_advisory_xact_lock\(724198306\)`).
		WillReturnResult(pgxmock.NewResult("SELECT", 0))
	pool.ExpectExec(`CREATE TABLE IF NOT EXISTS schema_migrations`).
		WillReturnResult(pgxmock.NewResult("CREATE TABLE", 0))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(1).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	for _, statement := range strings.Split(initialSchema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		pool.ExpectExec(regexp.QuoteMeta(statement)).
			WillReturnResult(pgxmock.NewResult("CREATE", 0))
	}
	pool.ExpectExec(`INSERT INTO schema_migrations \(version\) VALUES \(\$1\)`).
		WithArgs(1).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM schema_migrations WHERE version = \$1\)`).
		WithArgs(2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	for _, statement := range strings.Split(passwordResetSchema, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		pool.ExpectExec(regexp.QuoteMeta(statement)).
			WillReturnResult(pgxmock.NewResult("CREATE", 0))
	}
	pool.ExpectExec(`INSERT INTO schema_migrations \(version\) VALUES \(\$1\)`).
		WithArgs(2).
		WillReturnError(errors.New("simulated history failure"))
	pool.ExpectRollback()

	if err := Migrate(context.Background(), pool); err == nil {
		t.Fatal("Migrate succeeded despite a migration history error")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCheckSchemaReturnsVersionState(t *testing.T) {
	tests := []struct {
		name    string
		applied bool
		wantErr bool
	}{
		{name: "migration applied", applied: true},
		{name: "migration missing", applied: false, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatalf("create PostgreSQL mock: %v", err)
			}
			defer pool.Close()
			pool.ExpectQuery(`SELECT EXISTS`).
				WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(test.applied))

			err = CheckSchema(context.Background(), pool)
			if (err != nil) != test.wantErr {
				t.Fatalf("CheckSchema error = %v, wantErr %v", err, test.wantErr)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet PostgreSQL expectations: %v", err)
			}
		})
	}
}

func TestCheckSchemaPropagatesDatabaseErrors(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectQuery(`SELECT EXISTS`).WillReturnError(errors.New("simulated query error"))

	if err := CheckSchema(context.Background(), pool); err == nil {
		t.Fatal("CheckSchema hid a database error")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCleanupExpiredSessionsValidatesBatchAndDeletesBoundedRows(t *testing.T) {
	if _, err := CleanupExpiredSessions(context.Background(), nil, 0); err == nil {
		t.Fatal("cleanup accepted a zero-sized batch")
	}
	if _, err := CleanupExpiredSessions(context.Background(), nil, 10001); err == nil {
		t.Fatal("cleanup accepted a batch over the safety limit")
	}

	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectExec(`DELETE FROM sessions`).
		WithArgs(250).
		WillReturnResult(pgxmock.NewResult("DELETE", 17))

	count, err := CleanupExpiredSessions(context.Background(), pool, 250)
	if err != nil {
		t.Fatalf("CleanupExpiredSessions: %v", err)
	}
	if count != 17 {
		t.Fatalf("deleted count = %d, want 17", count)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCleanupExpiredSessionsPropagatesDatabaseErrors(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectExec(`DELETE FROM sessions`).
		WithArgs(10).
		WillReturnError(errors.New("simulated delete error"))

	if _, err := CleanupExpiredSessions(context.Background(), pool, 10); err == nil {
		t.Fatal("CleanupExpiredSessions hid a database error")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCleanupExpiredPasswordResetTokensValidatesBatchAndDeletesBoundedRows(t *testing.T) {
	if _, err := CleanupExpiredPasswordResetTokens(context.Background(), nil, 0); err == nil {
		t.Fatal("cleanup accepted a zero-sized batch")
	}
	if _, err := CleanupExpiredPasswordResetTokens(context.Background(), nil, 10001); err == nil {
		t.Fatal("cleanup accepted a batch over the safety limit")
	}

	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectExec(`DELETE FROM password_reset_tokens`).
		WithArgs(250).
		WillReturnResult(pgxmock.NewResult("DELETE", 4))

	count, err := CleanupExpiredPasswordResetTokens(context.Background(), pool, 250)
	if err != nil {
		t.Fatalf("CleanupExpiredPasswordResetTokens: %v", err)
	}
	if count != 4 {
		t.Fatalf("deleted count = %d, want 4", count)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCleanupExpiredEmailVerificationTokensDeletesBoundedRows(t *testing.T) {
	if _, err := CleanupExpiredEmailVerificationTokens(context.Background(), nil, 0); err == nil {
		t.Fatal("cleanup accepted a zero-sized batch")
	}
	if _, err := CleanupExpiredEmailVerificationTokens(context.Background(), nil, 10001); err == nil {
		t.Fatal("cleanup accepted a batch over the safety limit")
	}

	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectExec(`DELETE FROM email_verification_tokens`).
		WithArgs(250).
		WillReturnResult(pgxmock.NewResult("DELETE", 3))

	count, err := CleanupExpiredEmailVerificationTokens(context.Background(), pool, 250)
	if err != nil {
		t.Fatalf("CleanupExpiredEmailVerificationTokens: %v", err)
	}
	if count != 3 {
		t.Fatalf("deleted count = %d, want 3", count)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}
