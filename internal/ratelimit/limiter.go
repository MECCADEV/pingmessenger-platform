// Package ratelimit contains the small shared limiter boundary used by HTTP
// handlers. Redis is the production implementation; the in-memory version is
// useful for local development and deterministic unit tests.
package ratelimit

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}

type RedisLimiter struct{ client *redis.Client }

func NewRedis(rawURL string) (*RedisLimiter, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") {
		return nil, fmt.Errorf("REDIS_URL must be a redis:// or rediss:// URL")
	}
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	return &RedisLimiter{client: redis.NewClient(options)}, nil
}

func (r *RedisLimiter) Close() error { return r.client.Close() }

func (r *RedisLimiter) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

func (r *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if limit <= 0 || window <= 0 {
		return false, fmt.Errorf("invalid rate limit")
	}
	const script = `local n = redis.call('INCR', KEYS[1]); if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]); end; return n`
	n, err := r.client.Eval(ctx, script, []string{key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return n <= int64(limit), nil
}

type localEntry struct {
	count     int
	expiresAt time.Time
}

type LocalLimiter struct {
	mu      sync.Mutex
	entries map[string]localEntry
}

func NewLocal() *LocalLimiter { return &LocalLimiter{entries: make(map[string]localEntry)} }

func (l *LocalLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
	if limit <= 0 || window <= 0 {
		return false, fmt.Errorf("invalid rate limit")
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		entry = localEntry{expiresAt: now.Add(window)}
	}
	entry.count++
	l.entries[key] = entry
	return entry.count <= limit, nil
}
