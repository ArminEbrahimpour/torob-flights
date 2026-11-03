package pnr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache interface {
	Get(ctx context.Context, key CacheKey) (*CheckResult, error)
	Set(ctx context.Context, key CacheKey, result CheckResult) error
	Ping(ctx context.Context) error
	Close() error
}

type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
	prefix string
}

func NewRedisCache(client *redis.Client, prefix string, ttl time.Duration) *RedisCache {
	return &RedisCache{client: client, prefix: prefix, ttl: ttl}
}

func (c *RedisCache) Get(ctx context.Context, key CacheKey) (*CheckResult, error) {
	value, err := c.client.Get(ctx, c.redisKey(key)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}
	var result CheckResult
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, fmt.Errorf("decode cached result: %w", err)
	}
	return &result, nil
}

func (c *RedisCache) Set(ctx context.Context, key CacheKey, result CheckResult) error {
	value, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode cached result: %w", err)
	}
	if err := c.client.Set(ctx, c.redisKey(key), value, c.ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}

func (c *RedisCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}

func (c *RedisCache) redisKey(key CacheKey) string {
	normalized := strings.ToUpper(strings.TrimSpace(key.PNR)) + "\x00" +
		strings.ToUpper(strings.TrimSpace(key.LastName)) + "\x00" +
		key.FlightDate + "\x00" + normalizeAirline(key.Airline)
	hash := sha256.Sum256([]byte(normalized))
	return strings.TrimRight(c.prefix, ":") + ":" + hex.EncodeToString(hash[:])
}

type NoopCache struct{}

func (NoopCache) Get(context.Context, CacheKey) (*CheckResult, error) { return nil, nil }
func (NoopCache) Set(context.Context, CacheKey, CheckResult) error   { return nil }
func (NoopCache) Ping(context.Context) error                         { return nil }
func (NoopCache) Close() error                                       { return nil }
