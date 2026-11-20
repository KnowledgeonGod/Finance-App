package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
)

type redisMockExpecter interface {
	ExpectEvalSha(string, []string, ...interface{}) *redismock.ExpectedCmd
	ExpectPing() *redismock.ExpectedStatus
	ExpectationsWereMet() error
}

func TestNewRedisRejectsInvalidURL(t *testing.T) {
	if _, err := NewRedis("not-a-redis-url"); err == nil {
		t.Fatal("NewRedis accepted an invalid Redis URL")
	}
}

func TestNewRedisCreatesAClosableClient(t *testing.T) {
	limiter, err := NewRedis("redis://localhost:6379/2")
	if err != nil {
		t.Fatalf("NewRedis: %v", err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRedisLimiterAllowsRequestsBelowLimit(t *testing.T) {
	client, rawMock := redismock.NewClientMock()
	mock := rawMock.(redisMockExpecter)
	defer client.Close()

	const identity = "203.0.113.8:auth"
	digest := sha256.Sum256([]byte(identity))
	key := "finance-api:ratelimit:" + hex.EncodeToString(digest[:])
	mock.ExpectEvalSha(incrementWindow.Hash(), []string{key}, "60000").SetVal(int64(4))

	limiter := &Redis{client: client}
	allowed, err := limiter.Allow(context.Background(), identity, 10, time.Minute)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !allowed {
		t.Fatal("Allow rejected a request below the configured limit")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet Redis expectations: %v", err)
	}
}

func TestRedisLimiterRejectsRequestsOverLimit(t *testing.T) {
	client, rawMock := redismock.NewClientMock()
	mock := rawMock.(redisMockExpecter)
	defer client.Close()
	mock.ExpectEvalSha(incrementWindow.Hash(), []string{hashKey("client")}, "900000").SetVal(int64(11))

	limiter := &Redis{client: client}
	allowed, err := limiter.Allow(context.Background(), "client", 10, 15*time.Minute)
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if allowed {
		t.Fatal("Allow accepted a request over the configured limit")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet Redis expectations: %v", err)
	}
}

func TestRedisLimiterPropagatesBackendErrors(t *testing.T) {
	client, rawMock := redismock.NewClientMock()
	mock := rawMock.(redisMockExpecter)
	defer client.Close()
	mock.ExpectEvalSha(incrementWindow.Hash(), []string{hashKey("client")}, "60000").
		SetErr(errors.New("redis unavailable"))

	limiter := &Redis{client: client}
	if _, err := limiter.Allow(context.Background(), "client", 10, time.Minute); err == nil {
		t.Fatal("Allow hid a Redis backend error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet Redis expectations: %v", err)
	}
}

func TestRedisPingUsesBackend(t *testing.T) {
	client, rawMock := redismock.NewClientMock()
	mock := rawMock.(redisMockExpecter)
	defer client.Close()
	mock.ExpectPing().SetVal("PONG")

	if err := (&Redis{client: client}).Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet Redis expectations: %v", err)
	}
}

func hashKey(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return "finance-api:ratelimit:" + hex.EncodeToString(digest[:])
}
