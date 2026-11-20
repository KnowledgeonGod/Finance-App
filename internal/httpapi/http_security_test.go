package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v5"
)

func TestAuthenticationRequiredAndSecurityHeaders(t *testing.T) {
	handler := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestAuthenticationRejectsMalformedBearerBeforeDatabaseAccess(t *testing.T) {
	server := &Server{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler ran with malformed token")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer too-short")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticationLoadsUserForValidBearerToken(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	token := strings.Repeat("a", 43)
	pool.ExpectQuery(`SELECT user_id FROM sessions WHERE token_hash = \$1 AND expires_at > now\(\)`).
		WithArgs(hashToken(token)).
		WillReturnRows(pgxmock.NewRows([]string{"user_id"}).AddRow(testUserID))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"user_id": userID(r)})
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), testUserID) {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestAuthenticationReturnsInternalErrorWhenSessionStoreFails(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectQuery(`SELECT user_id FROM sessions`).
		WithArgs(pgxmock.AnyArg()).
		WillReturnError(errors.New("simulated database failure"))

	server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler ran after a session-store failure")
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 43))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestJSONOutputEscapesHTMLAndPreservesValue(t *testing.T) {
	const payload = `<script>alert("xss")</script>`
	response := httptest.NewRecorder()
	writeJSON(response, http.StatusOK, map[string]string{"description": payload})

	if strings.Contains(response.Body.String(), payload) {
		t.Fatal("response contains unescaped HTML")
	}
	var decoded map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}
	if decoded["description"] != payload {
		t.Fatalf("decoded value = %q, want original input", decoded["description"])
	}
}

func TestDecodeJSONRequiresApplicationJSON(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"Food"}`))
	request.Header.Set("Content-Type", "text/plain")
	var input struct {
		Name string `json:"name"`
	}
	err := decodeJSON(httptest.NewRecorder(), request, &input)
	if !errors.Is(err, errUnsupportedMediaType) {
		t.Fatalf("decodeJSON error = %v, want unsupported media type", err)
	}
}

func TestDecodeErrorMapsUnsupportedMediaType(t *testing.T) {
	response := httptest.NewRecorder()
	decodeError(response, errUnsupportedMediaType)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
	}
}

func TestDecodeErrorHidesParserDetails(t *testing.T) {
	response := httptest.NewRecorder()
	decodeError(response, errors.New("parser detail"))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if strings.Contains(response.Body.String(), "parser detail") {
		t.Fatal("parser details were exposed to the client")
	}
}

func TestDecodeJSONRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"email":"person@example.com","is_admin":true}`},
		{name: "multiple values", body: `{"email":"person@example.com"} {"email":"other@example.com"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			var input struct {
				Email string `json:"email"`
			}
			if err := decodeJSON(httptest.NewRecorder(), request, &input); err == nil {
				t.Fatal("decodeJSON accepted invalid input")
			}
		})
	}
}

func TestDecodeJSONEnforcesBodyLimit(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", maxRequestBody) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	var input struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(httptest.NewRecorder(), request, &input); err == nil {
		t.Fatal("decodeJSON accepted an oversized body")
	}
}

func TestRateLimiterBoundsRequestsAndKeyCount(t *testing.T) {
	limiter := newRateLimiter(2)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	if !limiter.allow("client-a", 2, time.Minute) || !limiter.allow("client-a", 2, time.Minute) {
		t.Fatal("limiter rejected a request below the limit")
	}
	if limiter.allow("client-a", 2, time.Minute) {
		t.Fatal("limiter accepted a request over the limit")
	}
	if !limiter.allow("client-b", 2, time.Minute) {
		t.Fatal("limiter rejected a new client below the key cap")
	}
	if limiter.allow("client-c", 2, time.Minute) {
		t.Fatal("limiter exceeded its bounded key capacity")
	}
	now = now.Add(time.Minute)
	if !limiter.allow("client-c", 2, time.Minute) {
		t.Fatal("limiter did not expire an old window")
	}
}

func TestInputValidationRejectsInjectionLikeIdentifiers(t *testing.T) {
	for _, value := range []string{
		`' OR 1=1 --`,
		`00000000-0000-0000-0000-000000000000' OR 'x'='x`,
	} {
		if validUUID(value) {
			t.Errorf("validUUID accepted %q", value)
		}
	}
	if _, err := validDate(`2026-01-01' OR '1'='1`); err == nil {
		t.Fatal("validDate accepted SQL-like input")
	}
}

func TestAuthenticationRateLimitIgnoresForwardedIPHeaders(t *testing.T) {
	handler := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	status := 0
	for i := 0; i < 11; i++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		status = response.Code
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("11th login request status = %d, want %d", status, http.StatusTooManyRequests)
	}
}

func TestPasswordResetEndpointsUseAuthenticationRateLimit(t *testing.T) {
	for _, path := range []string{
		"/api/v1/auth/password-reset/request",
		"/api/v1/auth/password-reset/complete",
	} {
		t.Run(path, func(t *testing.T) {
			handler := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			status := 0
			for i := 0; i < 11; i++ {
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				status = response.Code
			}
			if status != http.StatusTooManyRequests {
				t.Fatalf("11th request status = %d, want %d", status, http.StatusTooManyRequests)
			}
		})
	}
}

func TestSessionTokensAreOpaqueAndRandom(t *testing.T) {
	first, firstHash, err := newToken()
	if err != nil {
		t.Fatalf("newToken: %v", err)
	}
	second, _, err := newToken()
	if err != nil {
		t.Fatalf("newToken: %v", err)
	}
	if len(first) != 43 || len(firstHash) != 32 {
		t.Fatalf("token/hash lengths = %d/%d, want 43/32", len(first), len(firstHash))
	}
	if first == second {
		t.Fatal("two independently generated tokens were identical")
	}
	if !bytes.Equal(firstHash, hashToken(first)) {
		t.Fatal("stored token hash does not match the bearer token")
	}
}

func TestNullableUUIDFormatting(t *testing.T) {
	value := pgtype.UUID{
		Bytes: [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0},
		Valid: true,
	}
	got := nullableUUID(value)
	if got == nil || *got != "12345678-9abc-def0-1234-56789abcdef0" {
		t.Fatalf("nullableUUID() = %v, want canonical UUID", got)
	}
	if nullableUUID(pgtype.UUID{}) != nil {
		t.Fatal("nullableUUID() for NULL did not return nil")
	}
}

func TestRequestIDIsGeneratedAndReturned(t *testing.T) {
	handler := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/categories?secret=do-not-log", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	id := response.Header().Get(requestIDHeader)
	if !validUUID(id) {
		t.Fatalf("request ID = %q, want a generated UUID", id)
	}
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestLivenessCheckDoesNotRequireDatabase(t *testing.T) {
	handler := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestReadinessCheckUsesDatabaseHealth(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		pool, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("create PostgreSQL mock: %v", err)
		}
		defer pool.Close()
		pool.ExpectPing()
		server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), limit: newRateLimiter(10)}
		response := httptest.NewRecorder()
		server.ready(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet PostgreSQL expectations: %v", err)
		}
	})
	t.Run("database unavailable", func(t *testing.T) {
		pool, err := pgxmock.NewPool()
		if err != nil {
			t.Fatalf("create PostgreSQL mock: %v", err)
		}
		defer pool.Close()
		pool.ExpectPing().WillReturnError(errors.New("simulated database failure"))
		server := &Server{db: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), limit: newRateLimiter(10)}
		response := httptest.NewRecorder()
		server.ready(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
		}
		if err := pool.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet PostgreSQL expectations: %v", err)
		}
	})
}

func TestReadinessCheckFailsWhenSharedRateLimiterIsUnavailable(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create PostgreSQL mock: %v", err)
	}
	defer pool.Close()
	pool.ExpectPing()
	server := &Server{
		db:     pool,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		limit:  unhealthyRateLimiter{},
	}
	response := httptest.NewRecorder()
	server.ready(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet PostgreSQL expectations: %v", err)
	}
}

func TestPanicRecoveryReturnsInternalServerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := &Server{logger: logger}
	handler := server.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("sensitive panic detail")
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), "sensitive panic detail") {
		t.Fatal("panic details were exposed to the client")
	}
}

type unavailableRateLimiter struct{}

func (unavailableRateLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) {
	return false, errors.New("backend unavailable")
}

type unhealthyRateLimiter struct{}

func (unhealthyRateLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) {
	return true, nil
}

func (unhealthyRateLimiter) Ping(context.Context) error {
	return errors.New("backend unavailable")
}

func TestRateLimiterFailureFailsClosed(t *testing.T) {
	handler := NewWithRateLimiter(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), unavailableRateLimiter{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}
