package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"golang.org/x/crypto/bcrypt"
)

type recordingPasswordResetMailer struct {
	recipient string
	resetURL  string
	verifyURL string
	err       error
}

func (m *recordingPasswordResetMailer) SendPasswordReset(_ context.Context, recipient, resetURL string) error {
	m.recipient = recipient
	m.resetURL = resetURL
	return m.err
}

func (m *recordingPasswordResetMailer) SendEmailVerification(_ context.Context, recipient, verificationURL string) error {
	m.recipient = recipient
	m.verifyURL = verificationURL
	return m.err
}

func TestRegisterRejectsInvalidCredentialsBeforeDatabaseAccess(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid email", body: `{"email":"bad-email","password":"long-enough-password"}`},
		{name: "short password", body: `{"email":"person@example.com","password":"short"}`},
		{name: "password without uppercase", body: `{"email":"person@example.com","password":"password1!"}`},
		{name: "password without special character", body: `{"email":"person@example.com","password":"Password1"}`},
		{name: "long password", body: `{"email":"person@example.com","password":"` + strings.Repeat("x", 73) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			server.register(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestRegisterCreatesUnverifiedUserAndTokenWithoutSession(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectExec(`INSERT INTO users`).
		WithArgs(pgxmock.AnyArg(), "person@example.com", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec(`INSERT INTO email_verification_tokens`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	var logOutput bytes.Buffer
	server := &Server{
		db: pool, logger: slog.New(slog.NewJSONHandler(&logOutput, nil)),
		resetPublicURL: "http://localhost:8080", developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":" Person@Example.com ","password":"Long-enough-password!"}`))
	request.Header.Set("Content-Type", "application/json")
	server.register(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Check your email") {
		t.Fatalf("registration did not acknowledge verification: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "access_token") {
		t.Fatalf("registration created an authenticated session: %s", response.Body.String())
	}
	if !strings.Contains(logOutput.String(), "development-only email verification link") {
		t.Fatalf("development verification link was not logged: %s", logOutput.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestRegisterSendsVerificationLinkToNewAccountEmail(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectExec(`INSERT INTO users`).
		WithArgs(pgxmock.AnyArg(), "person@example.com", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec(`INSERT INTO email_verification_tokens`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	mailer := &recordingPasswordResetMailer{}
	server := &Server{
		db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		resetPublicURL: "https://finance.example.com", verificationMailer: mailer,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"Long-enough-password!"}`))
	request.Header.Set("Content-Type", "application/json")
	server.register(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if mailer.recipient != "person@example.com" ||
		!strings.HasPrefix(mailer.verifyURL, "https://finance.example.com/#verify_email=") {
		t.Fatalf("mailer received recipient/link %q/%q", mailer.recipient, mailer.verifyURL)
	}
	if strings.Contains(response.Body.String(), "access_token") ||
		strings.Contains(response.Body.String(), mailer.verifyURL[strings.LastIndex(mailer.verifyURL, "=")+1:]) {
		t.Fatalf("registration response exposed an authentication or verification token: %s", response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestRegisterRollsBackWhenVerificationTokenInsertFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectExec(`INSERT INTO users`).
		WithArgs(pgxmock.AnyArg(), "person@example.com", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec(`INSERT INTO email_verification_tokens`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errors.New("simulated database failure"))
	pool.ExpectRollback()

	server := &Server{
		db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		resetPublicURL: "http://localhost:8080", developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"Long-enough-password!"}`))
	request.Header.Set("Content-Type", "application/json")
	server.register(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestRegisterMapsDuplicateEmailToConflict(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectExec(`INSERT INTO users`).
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(&pgconn.PgError{Code: "23505"})
	pool.ExpectRollback()

	server := &Server{
		db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		resetPublicURL: "http://localhost:8080", developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"Long-enough-password!"}`))
	request.Header.Set("Content-Type", "application/json")
	server.register(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusConflict)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestLoginUsesSameUnauthorizedResponseForUnknownAccount(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectQuery(`SELECT id, password_hash, email_verified_at IS NOT NULL FROM users WHERE email = \$1`).
		WithArgs("person@example.com").
		WillReturnError(pgx.ErrNoRows)

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"long-enough-password"}`))
	request.Header.Set("Content-Type", "application/json")
	server.login(response, request)

	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "invalid email or password") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestLoginCreatesSessionForCorrectPassword(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("long-enough-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	pool.ExpectQuery(`SELECT id, password_hash, email_verified_at IS NOT NULL FROM users WHERE email = \$1`).
		WithArgs("person@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "password_hash", "verified"}).
			AddRow(testUserID, string(passwordHash), true))
	pool.ExpectExec(`INSERT INTO sessions`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"long-enough-password"}`))
	request.Header.Set("Content-Type", "application/json")
	server.login(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result authResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Token == "" || result.TokenType != "Bearer" {
		t.Fatalf("unexpected login response: %+v", result)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	pool.ExpectQuery(`SELECT id, password_hash, email_verified_at IS NOT NULL FROM users WHERE email = \$1`).
		WithArgs("person@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "password_hash", "verified"}).
			AddRow(testUserID, string(passwordHash), true))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"incorrect-password"}`))
	request.Header.Set("Content-Type", "application/json")
	server.login(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestLoginRejectsUnverifiedEmail(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	pool.ExpectQuery(`SELECT id, password_hash, email_verified_at IS NOT NULL FROM users WHERE email = \$1`).
		WithArgs("person@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id", "password_hash", "verified"}).
			AddRow(testUserID, string(passwordHash), false))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com","password":"correct-password"}`))
	request.Header.Set("Content-Type", "application/json")
	server.login(response, request)

	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "verify your email") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestCompleteEmailVerificationConsumesToken(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	token := strings.Repeat("a", 43)
	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT user_id FROM email_verification_tokens`).
		WithArgs(hashToken(token)).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(testUserID))
	pool.ExpectExec(`UPDATE users SET email_verified_at = now\(\) WHERE id = \$1 AND email_verified_at IS NULL`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec(`DELETE FROM email_verification_tokens WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"token":"`+token+`"}`))
	request.Header.Set("Content-Type", "application/json")
	server.completeEmailVerification(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Email verified") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestLogoutRevokesOnlyCurrentUsersSession(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	token := strings.Repeat("a", 43)
	pool.ExpectExec(`DELETE FROM sessions WHERE token_hash = \$1 AND user_id = \$2`).
		WithArgs(hashToken(token), testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request = request.WithContext(context.WithValue(request.Context(), userIDKey, testUserID))
	response := httptest.NewRecorder()
	server.logout(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestLogoutReportsSessionStoreFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	token := strings.Repeat("a", 43)
	pool.ExpectExec(`DELETE FROM sessions`).
		WithArgs(hashToken(token), testUserID).
		WillReturnError(errors.New("simulated database failure"))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request = request.WithContext(context.WithValue(request.Context(), userIDKey, testUserID))
	response := httptest.NewRecorder()
	server.logout(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestPasswordResetRequestDoesNotRevealUnknownAccounts(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT id FROM users WHERE email = \$1 FOR UPDATE`).
		WithArgs("person@example.com").
		WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()

	server := &Server{
		db:               pool,
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		resetPublicURL:   "http://localhost:8080",
		developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	server.requestPasswordReset(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if !strings.Contains(response.Body.String(), "If an account exists") {
		t.Fatalf("response does not contain generic acknowledgement: %s", response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestPasswordResetRequestStoresTokenHashAndLogsDevLink(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT id FROM users WHERE email = \$1 FOR UPDATE`).
		WithArgs("person@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(testUserID))
	pool.ExpectExec(`DELETE FROM password_reset_tokens WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectExec(`INSERT INTO password_reset_tokens`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	var logOutput bytes.Buffer
	server := &Server{
		db:               pool,
		logger:           slog.New(slog.NewJSONHandler(&logOutput, nil)),
		resetPublicURL:   "https://finance.example.com",
		developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	server.requestPasswordReset(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	const linkPrefix = "https://finance.example.com/#reset_token="
	logged := logOutput.String()
	start := strings.Index(logged, linkPrefix)
	if start < 0 {
		t.Fatalf("development reset link was not logged: %s", logged)
	}
	loggedToken := logged[start+len(linkPrefix):]
	end := strings.IndexByte(loggedToken, '"')
	if end < 0 || len(loggedToken[:end]) != 43 {
		t.Fatalf("development reset link has invalid token: %s", logged)
	}
	if strings.Contains(response.Body.String(), loggedToken[:end]) {
		t.Fatal("reset token was exposed in the HTTP response")
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestPasswordResetRequestUsesConfiguredMailerInDevelopment(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT id FROM users WHERE email = \$1 FOR UPDATE`).
		WithArgs("person@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(testUserID))
	pool.ExpectExec(`DELETE FROM password_reset_tokens WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectExec(`INSERT INTO password_reset_tokens`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	var logOutput bytes.Buffer
	mailer := &recordingPasswordResetMailer{}
	server := &Server{
		db:               pool,
		logger:           slog.New(slog.NewJSONHandler(&logOutput, nil)),
		resetPublicURL:   "http://localhost:8080",
		resetMailer:      mailer,
		developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	server.requestPasswordReset(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if mailer.recipient != "person@example.com" ||
		!strings.HasPrefix(mailer.resetURL, "http://localhost:8080/#reset_token=") {
		t.Fatalf("mailer received recipient/link %q/%q", mailer.recipient, mailer.resetURL)
	}
	if strings.Contains(logOutput.String(), "development-only password reset link") {
		t.Fatal("configured mail delivery also logged a development reset link")
	}
	if !strings.Contains(response.Body.String(), "reset link will be sent") {
		t.Fatalf("response did not acknowledge the generic reset request: %s", response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestPasswordResetRequestReportsDevelopmentMailDeliveryFailure(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT id FROM users WHERE email = \$1 FOR UPDATE`).
		WithArgs("person@example.com").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(testUserID))
	pool.ExpectExec(`DELETE FROM password_reset_tokens WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectExec(`INSERT INTO password_reset_tokens`).
		WithArgs(pgxmock.AnyArg(), testUserID, pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	mailer := &recordingPasswordResetMailer{err: errors.New("Brevo API rejected request")}
	server := &Server{
		db:               pool,
		logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		resetPublicURL:   "http://localhost:8080",
		resetMailer:      mailer,
		developmentReset: true,
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"email":"person@example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	server.requestPasswordReset(response, request)

	if response.Code != http.StatusBadGateway ||
		!strings.Contains(response.Body.String(), "could not send the reset email") {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestPasswordResetConsumesTokenAndRevokesSessions(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	token := strings.Repeat("a", 43)
	pool.ExpectBegin()
	pool.ExpectQuery(`SELECT user_id FROM password_reset_tokens`).
		WithArgs(hashToken(token)).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(testUserID))
	pool.ExpectExec(`UPDATE users SET password_hash = \$1 WHERE id = \$2`).
		WithArgs(pgxmock.AnyArg(), testUserID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec(`DELETE FROM sessions WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	pool.ExpectExec(`DELETE FROM password_reset_tokens WHERE user_id = \$1`).
		WithArgs(testUserID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectCommit()
	pool.ExpectRollback()

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"token":"`+token+`","password":"New-password1!"}`))
	request.Header.Set("Content-Type", "application/json")
	server.completePasswordReset(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestUserForTokenHashesTokenBeforeDatabaseLookup(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	token := strings.Repeat("a", 43)
	pool.ExpectQuery(`SELECT user_id FROM sessions WHERE token_hash = \$1 AND expires_at > now\(\)`).
		WithArgs(hashToken(token)).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(testUserID))

	server := &Server{db: pool}
	got, err := server.userForToken(context.Background(), token)
	if err != nil {
		t.Fatalf("userForToken: %v", err)
	}
	if got != testUserID {
		t.Fatalf("user ID = %q, want %q", got, testUserID)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}
