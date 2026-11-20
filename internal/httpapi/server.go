package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxRequestBody = 1 << 20

type Database interface {
	Ping(context.Context) error
	Begin(context.Context) (pgx.Tx, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type contextKey string

const (
	userIDKey       contextKey = "userID"
	requestIDKey    contextKey = "requestID"
	requestIDHeader            = "X-Request-ID"
)

type Server struct {
	db                 Database
	logger             *slog.Logger
	limit              RateLimiter
	mux                *http.ServeMux
	resetPublicURL     string
	resetMailer        PasswordResetMailer
	verificationMailer EmailVerificationMailer
	developmentReset   bool
}

func New(db Database, logger *slog.Logger) http.Handler {
	return NewWithRateLimiter(db, logger, newRateLimiter(10000))
}

func NewWithRateLimiter(db Database, logger *slog.Logger, limiter RateLimiter) http.Handler {
	return NewWithPasswordResetOptions(db, logger, limiter, PasswordResetOptions{})
}

func NewWithPasswordResetOptions(
	db Database,
	logger *slog.Logger,
	limiter RateLimiter,
	resetOptions PasswordResetOptions,
) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if limiter == nil {
		limiter = newRateLimiter(10000)
	}
	s := &Server{
		db:               db,
		logger:           logger,
		limit:            limiter,
		mux:              http.NewServeMux(),
		resetPublicURL:   strings.TrimRight(resetOptions.PublicBaseURL, "/"),
		resetMailer:      resetOptions.Mailer,
		developmentReset: resetOptions.Development,
	}
	s.verificationMailer, _ = resetOptions.Mailer.(EmailVerificationMailer)
	s.routes()
	var handler http.Handler = s.mux
	handler = s.recoverPanics(handler)
	handler = s.securityHeaders(handler)
	handler = s.rateLimit(handler)
	handler = s.accessLog(handler)
	handler = s.requestID(handler)
	return handler
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", serveFrontend)
	s.mux.Handle("GET /assets/", frontendAssets())
	s.mux.HandleFunc("GET /livez", s.live)
	s.mux.HandleFunc("GET /readyz", s.ready)
	s.mux.HandleFunc("GET /healthz", s.ready)
	s.mux.HandleFunc("POST /api/v1/auth/register", s.register)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.login)
	s.mux.HandleFunc("POST /api/v1/auth/password-reset/request", s.requestPasswordReset)
	s.mux.HandleFunc("POST /api/v1/auth/password-reset/complete", s.completePasswordReset)
	s.mux.HandleFunc("POST /api/v1/auth/email-verification/complete", s.completeEmailVerification)
	s.mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.logout)))
	s.mux.Handle("GET /api/v1/categories", s.requireAuth(http.HandlerFunc(s.listCategories)))
	s.mux.Handle("POST /api/v1/categories", s.requireAuth(http.HandlerFunc(s.createCategory)))
	s.mux.Handle("GET /api/v1/transactions", s.requireAuth(http.HandlerFunc(s.listTransactions)))
	s.mux.Handle("POST /api/v1/transactions", s.requireAuth(http.HandlerFunc(s.createTransaction)))
	s.mux.Handle("PUT /api/v1/transactions/{id}", s.requireAuth(http.HandlerFunc(s.updateTransaction)))
	s.mux.Handle("DELETE /api/v1/transactions/{id}", s.requireAuth(http.HandlerFunc(s.deleteTransaction)))
	s.mux.Handle("GET /api/v1/reports/monthly", s.requireAuth(http.HandlerFunc(s.monthlyReport)))
	s.mux.Handle("GET /api/v1/budgets", s.requireAuth(http.HandlerFunc(s.listBudgets)))
	s.mux.Handle("POST /api/v1/budgets", s.requireAuth(http.HandlerFunc(s.createBudget)))
	s.mux.Handle("GET /api/v1/budgets/alerts", s.requireAuth(http.HandlerFunc(s.budgetAlerts)))
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/livez" || r.URL.Path == "/readyz" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		maxRequests := 120
		window := time.Minute
		if isAuthRateLimitedPath(r.URL.Path) {
			maxRequests = 10
			window = 15 * time.Minute
		}
		key := clientIP(r)
		if maxRequests == 10 {
			key += ":auth"
		}
		allowed, err := s.limit.Allow(r.Context(), key, maxRequests, window)
		if err != nil {
			s.logger.Error("rate limiter unavailable", "request_id", requestID(r), "error", err)
			writeError(w, http.StatusServiceUnavailable, "service temporarily unavailable")
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAuthRateLimitedPath(path string) bool {
	switch path {
	case "/api/v1/auth/login",
		"/api/v1/auth/register",
		"/api/v1/auth/password-reset/request",
		"/api/v1/auth/password-reset/complete",
		"/api/v1/auth/email-verification/complete":
		return true
	default:
		return false
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		if len(token) != 43 {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		userID, err := s.userForToken(r.Context(), token)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				s.logger.Error("look up session", "error", err)
				writeError(w, http.StatusInternalServerError, "internal server error")
				return
			}
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userID(r *http.Request) string {
	id, _ := r.Context().Value(userIDKey).(string)
	return id
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		s.logger.Error("health check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if dependency, ok := s.limit.(interface{ Ping(context.Context) error }); ok {
		if err := dependency.Ping(ctx); err != nil {
			s.logger.Error("rate limiter health check failed", "request_id", requestID(r), "error", err)
			writeError(w, http.StatusServiceUnavailable, "rate limiter unavailable")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := newUUID()
		if err != nil {
			s.logger.Error("generate request ID", "error", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(requestIDKey).(string)
	return id
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w}
		started := time.Now()
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		s.logger.Info("http request",
			"request_id", requestID(r),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(started),
		)
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder, ok := w.(*statusRecorder)
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("HTTP handler panic recovered",
					"request_id", requestID(r),
					"method", r.Method,
					"path", r.URL.Path,
				)
				if !ok || recorder.status == 0 {
					writeError(w, http.StatusInternalServerError, "internal server error")
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if mediaType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mediaType != "application/json" {
		return errUnsupportedMediaType
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

var errUnsupportedMediaType = errors.New("content type must be application/json")

func decodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUnsupportedMediaType) {
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON request")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func parseLimitOffset(r *http.Request) (int, int, error) {
	limit := 50
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return 0, 0, errors.New("limit must be between 1 and 100")
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1000000 {
			return 0, 0, errors.New("offset must be between 0 and 1000000")
		}
		offset = value
	}
	return limit, offset, nil
}

func (s *Server) internalError(w http.ResponseWriter, message string, err error) {
	s.logger.Error(message, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
