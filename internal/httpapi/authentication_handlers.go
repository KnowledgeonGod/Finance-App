package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

const sessionLifetime = 24 * time.Hour
const passwordResetLifetime = 30 * time.Minute
const emailVerificationLifetime = 24 * time.Hour

type PasswordResetMailer interface {
	SendPasswordReset(context.Context, string, string) error
}

type EmailVerificationMailer interface {
	SendEmailVerification(context.Context, string, string) error
}

type PasswordResetOptions struct {
	PublicBaseURL string
	Development   bool
	Mailer        PasswordResetMailer
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token     string    `json:"access_token"`
	TokenType string    `json:"token_type"`
	ExpiresAt time.Time `json:"expires_at"`
}

type passwordResetRequest struct {
	Email string `json:"email"`
}

type passwordResetCompletion struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type emailVerificationRequest struct {
	Token string `json:"token"`
}

func (s *Server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var input passwordResetRequest
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	email, err := validEmail(input.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.resetPublicURL == "" || (!s.developmentReset && s.resetMailer == nil) {
		s.logger.Error("password reset delivery is not configured", "request_id", requestID(r))
		writeError(w, http.StatusServiceUnavailable, "password reset is temporarily unavailable")
		return
	}

	token, tokenHash, err := newToken()
	if err != nil {
		s.internalError(w, "generate password reset token", err)
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.internalError(w, "begin password reset request", err)
		return
	}
	defer tx.Rollback(ctx)

	var userID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email = $1 FOR UPDATE`, email).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		s.writePasswordResetAcknowledgement(w)
		return
	}
	if err != nil {
		s.internalError(w, "look up account for password reset", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM password_reset_tokens WHERE user_id = $1`, userID); err != nil {
		s.internalError(w, "replace password reset token", err)
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, time.Now().UTC().Add(passwordResetLifetime),
	); err != nil {
		s.internalError(w, "store password reset token", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, "commit password reset request", err)
		return
	}

	resetURL := s.resetPublicURL + "/#reset_token=" + token
	if s.resetMailer != nil {
		if err := s.resetMailer.SendPasswordReset(ctx, email, resetURL); err != nil {
			s.logger.Error("send password reset email", "request_id", requestID(r), "error", err)
			if s.developmentReset {
				writeError(w, http.StatusBadGateway, "could not send the reset email; check email provider settings and the API log")
				return
			}
		}
	} else if s.developmentReset {
		s.logger.Info("development-only password reset link", "url", resetURL)
	}
	s.writePasswordResetAcknowledgement(w)
}

func (s *Server) writePasswordResetAcknowledgement(w http.ResponseWriter) {
	message := "If an account exists for that email, a reset link will be sent."
	if s.developmentReset && s.resetMailer == nil {
		message += " In local development, check the API terminal for the link."
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": message})
}

func (s *Server) completePasswordReset(w http.ResponseWriter, r *http.Request) {
	var input passwordResetCompletion
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	if len(input.Token) != 43 {
		writeError(w, http.StatusBadRequest, "reset token is invalid or expired")
		return
	}
	if err := validRegistrationPassword(input.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.internalError(w, "begin password reset completion", err)
		return
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `
		SELECT user_id FROM password_reset_tokens
		WHERE token_hash = $1 AND expires_at > now()
		FOR UPDATE
	`, hashToken(input.Token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "reset token is invalid or expired")
		return
	}
	if err != nil {
		s.internalError(w, "look up password reset token", err)
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		s.internalError(w, "hash reset password", err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, string(passwordHash), userID); err != nil {
		s.internalError(w, "update account password", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		s.internalError(w, "revoke sessions after password reset", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM password_reset_tokens WHERE user_id = $1`, userID); err != nil {
		s.internalError(w, "consume password reset tokens", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, "commit password reset", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var input credentials
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	email, err := validEmail(input.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validRegistrationPassword(input.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.resetPublicURL == "" || (!s.developmentReset && s.verificationMailer == nil) {
		s.logger.Error("email verification delivery is not configured", "request_id", requestID(r))
		writeError(w, http.StatusServiceUnavailable, "email verification is temporarily unavailable")
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}
	id, err := newUUID()
	if err != nil {
		s.internalError(w, "generate user ID", err)
		return
	}
	token, tokenHash, err := newToken()
	if err != nil {
		s.internalError(w, "generate email verification token", err)
		return
	}

	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.internalError(w, "begin user registration", err)
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		id, email, string(passwordHash)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		s.internalError(w, "create user", err)
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO email_verification_tokens (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, id, time.Now().UTC().Add(emailVerificationLifetime)); err != nil {
		s.internalError(w, "create email verification token", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, "commit user registration", err)
		return
	}
	verificationURL := s.resetPublicURL + "/#verify_email=" + token
	if s.verificationMailer != nil {
		if err := s.verificationMailer.SendEmailVerification(ctx, email, verificationURL); err != nil {
			s.logger.Error("send email verification", "request_id", requestID(r), "error", err)
			if _, cleanupErr := s.db.Exec(ctx,
				`DELETE FROM users WHERE id = $1 AND email_verified_at IS NULL`, id); cleanupErr != nil {
				s.logger.Error("remove account after verification delivery failure",
					"request_id", requestID(r), "error", cleanupErr)
			}
			writeError(w, http.StatusBadGateway, "could not send the verification email; check email provider settings and try signing up again")
			return
		}
	} else if s.developmentReset {
		s.logger.Info("development-only email verification link", "url", verificationURL)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": "Account created. Check your email for a verification link before signing in.",
	})
}

func (s *Server) completeEmailVerification(w http.ResponseWriter, r *http.Request) {
	var input emailVerificationRequest
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	if len(input.Token) != 43 {
		writeError(w, http.StatusBadRequest, "verification link is invalid or expired")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		s.internalError(w, "begin email verification", err)
		return
	}
	defer tx.Rollback(ctx)

	var userID string
	err = tx.QueryRow(ctx, `
		SELECT user_id FROM email_verification_tokens
		WHERE token_hash = $1 AND expires_at > now()
		FOR UPDATE
	`, hashToken(input.Token)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "verification link is invalid or expired")
		return
	}
	if err != nil {
		s.internalError(w, "look up email verification token", err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET email_verified_at = now() WHERE id = $1 AND email_verified_at IS NULL`, userID); err != nil {
		s.internalError(w, "verify user email", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM email_verification_tokens WHERE user_id = $1`, userID); err != nil {
		s.internalError(w, "consume email verification token", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, "commit email verification", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Email verified. You can now sign in."})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input credentials
	if err := decodeJSON(w, r, &input); err != nil {
		decodeError(w, err)
		return
	}
	email, err := validEmail(input.Email)
	if err != nil || validPassword(input.Password) != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	var id, passwordHash string
	var emailVerified bool
	err = s.db.QueryRow(r.Context(),
		`SELECT id, password_hash, email_verified_at IS NOT NULL FROM users WHERE email = $1`, email,
	).Scan(&id, &passwordHash, &emailVerified)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, hashErr := bcrypt.GenerateFromPassword([]byte(input.Password), 12); hashErr != nil {
			s.internalError(w, "verify unknown account", hashErr)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		s.internalError(w, "look up user for login", err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if !emailVerified {
		writeError(w, http.StatusForbidden, "verify your email before signing in")
		return
	}

	token, tokenHash, err := newToken()
	if err != nil {
		s.internalError(w, "generate session token", err)
		return
	}
	expiresAt := time.Now().UTC().Add(sessionLifetime)
	if _, err := s.db.Exec(r.Context(),
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, id, expiresAt); err != nil {
		s.internalError(w, "create login session", err)
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, TokenType: "Bearer", ExpiresAt: expiresAt})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if _, err := s.db.Exec(r.Context(),
		`DELETE FROM sessions WHERE token_hash = $1 AND user_id = $2`,
		hashToken(token), userID(r)); err != nil {
		s.internalError(w, "revoke session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userForToken(ctx context.Context, token string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at > now()`,
		hashToken(token),
	).Scan(&id)
	return id, err
}

func newToken() (string, []byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
