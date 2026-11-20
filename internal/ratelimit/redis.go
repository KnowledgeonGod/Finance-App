package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var incrementWindow = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
`)

type Redis struct {
	client *redis.Client
}

func NewRedis(rawURL string) (*Redis, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, errors.New("RATE_LIMIT_REDIS_URL is invalid")
	}
	options.PoolSize = 20
	return &Redis{client: redis.NewClient(options)}, nil
}

func (r *Redis) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *Redis) Close() error {
	return r.client.Close()
}

func (r *Redis) Allow(ctx context.Context, key string, maxRequests int, window time.Duration) (bool, error) {
	digest := sha256.Sum256([]byte(key))
	redisKey := "finance-api:ratelimit:" + hex.EncodeToString(digest[:])
	count, err := incrementWindow.Run(
		ctx, r.client, []string{redisKey},
		strconv.FormatInt(window.Milliseconds(), 10),
	).Int64()
	if err != nil {
		return false, err
	}
	return count <= int64(maxRequests), nil
}
