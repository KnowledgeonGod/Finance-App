package httpapi

import (
	"context"
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}

type rateWindow struct {
	count   int
	expires time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rateWindow
	maxKeys int
	now     func() time.Time
}

func newRateLimiter(maxKeys int) *rateLimiter {
	return &rateLimiter{entries: make(map[string]rateWindow), maxKeys: maxKeys, now: time.Now}
}

func (l *rateLimiter) Allow(_ context.Context, key string, maxRequests int, window time.Duration) (bool, error) {
	return l.allow(key, maxRequests, window), nil
}

func (l *rateLimiter) allow(key string, maxRequests int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, exists := l.entries[key]
	if exists && now.Before(entry.expires) {
		if entry.count >= maxRequests {
			return false
		}
		entry.count++
		l.entries[key] = entry
		return true
	}

	if !exists && len(l.entries) >= l.maxKeys {
		for candidate, old := range l.entries {
			if !now.Before(old.expires) {
				delete(l.entries, candidate)
			}
		}
		if len(l.entries) >= l.maxKeys {
			return false
		}
	}
	l.entries[key] = rateWindow{count: 1, expires: now.Add(window)}
	return true
}
