package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a Cache backed by a Redis server, shared by every API and worker instance.
type Redis struct {
	client *redis.Client
	prefix string
}

// incrScript increments a counter and sets its expiry on first use, atomically, so a
// crash between the two steps can never leave a rate-limit key without a TTL.
var incrScript = redis.NewScript(`
local v = redis.call('INCR', KEYS[1])
if v == 1 and tonumber(ARGV[1]) > 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return v
`)

// NewRedis connects to the Redis server at url (redis://host:port/db) and verifies it.
func NewRedis(ctx context.Context, url, keyPrefix string) (*Redis, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return &Redis{client: client, prefix: keyPrefix}, nil
}

func (r *Redis) key(k string) string { return r.prefix + k }

func (r *Redis) Get(ctx context.Context, key string) ([]byte, bool, error) {
	val, err := r.client.Get(ctx, r.key(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return val, true, nil
}

func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, r.key(key), value, ttl).Err()
}

func (r *Redis) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = r.key(k)
	}
	return r.client.Del(ctx, full...).Err()
}

func (r *Redis) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	return incrScript.Run(ctx, r.client, []string{r.key(key)}, ttl.Milliseconds()).Int64()
}

func (r *Redis) Close() error { return r.client.Close() }
