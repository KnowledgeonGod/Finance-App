package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"finance-tracker/internal/config"
	"finance-tracker/internal/database"
	"finance-tracker/internal/httpapi"
	"finance-tracker/internal/mailer"
	"finance-tracker/internal/ratelimit"
	"finance-tracker/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadAPIConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := store.CheckSchema(ctx, pool); err != nil {
		return err
	}
	cleanupDone := make(chan struct{})
	cleanSessions := func(cleanupCtx context.Context, batchSize int) (int64, error) {
		removed, err := store.CleanupExpiredSessions(cleanupCtx, pool, batchSize)
		if err != nil {
			return 0, err
		}
		removedTokens, err := store.CleanupExpiredPasswordResetTokens(cleanupCtx, pool, batchSize)
		if err != nil {
			return 0, err
		}
		removedVerificationTokens, err := store.CleanupExpiredEmailVerificationTokens(cleanupCtx, pool, batchSize)
		if err != nil {
			return 0, err
		}
		return removed + removedTokens + removedVerificationTokens, nil
	}
	go cleanupExpiredSessions(ctx, cleanSessions, logger, cleanupDone)
	defer func() {
		stop()
		<-cleanupDone
	}()

	var limiter httpapi.RateLimiter
	if cfg.RedisURL == "" {
		logger.Warn("using process-local rate limiting; configure Redis before scaling replicas")
	} else {
		redisLimiter, err := ratelimit.NewRedis(cfg.RedisURL)
		if err != nil {
			return err
		}
		defer redisLimiter.Close()
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = redisLimiter.Ping(pingCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("connect to rate-limit Redis: %w", err)
		}
		limiter = redisLimiter
	}
	var resetMailer httpapi.PasswordResetMailer
	if cfg.BrevoAPIKey != "" {
		resetMailer, err = mailer.NewBrevo(cfg.BrevoAPIKey, cfg.MailFrom)
		if err != nil {
			return err
		}
	}
	handler := httpapi.NewWithPasswordResetOptions(pool, logger, limiter, httpapi.PasswordResetOptions{
		PublicBaseURL: cfg.PublicBaseURL,
		Development:   cfg.Environment == "development",
		Mailer:        resetMailer,
	})

	server := newHTTPServer(cfg, handler)

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		shutdownErr <- server.Shutdown(shutdownCtx)
	}()

	logger.Info("finance API listening", "address", cfg.HTTPAddr, "environment", cfg.Environment)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if ctx.Err() != nil {
		if err := <-shutdownErr; err != nil {
			return err
		}
	}
	return nil
}

func loadAPIConfig() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, err
	}
	if err := cfg.ValidateAPI(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}
}

func cleanupExpiredSessions(
	ctx context.Context,
	clean func(context.Context, int) (int64, error),
	logger *slog.Logger,
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var removed int64
		for batch := 0; batch < 10; batch++ {
			if cleanupCtx.Err() != nil {
				break
			}
			count, err := clean(cleanupCtx, 1000)
			if err != nil {
				logger.Error("clean up expired auth records", "error", err)
				return
			}
			removed += count
			if count < 1000 {
				break
			}
		}
		if removed > 0 {
			logger.Info("expired auth records removed", "count", removed)
		}
	}

	cleanup()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}
