package api

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisKV は go-redis による KV。
type RedisKV struct{ c redis.UniversalClient }

// NewRedisKV は Redis のクライアントから KV を作る。
func NewRedisKV(c redis.UniversalClient) *RedisKV { return &RedisKV{c: c} }

func (r *RedisKV) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := r.c.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (r *RedisKV) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.c.Set(ctx, key, value, ttl).Err()
}

func (r *RedisKV) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return r.c.SetNX(ctx, key, value, ttl).Result()
}

// delIfEqual は、値が一致するときだけ消す（取得と削除を 1 つの Lua で行い、競合させない）。
var delIfEqual = redis.NewScript(`if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`)

func (r *RedisKV) DelIfEqual(ctx context.Context, key, value string) error {
	return delIfEqual.Run(ctx, r.c, []string{key}, value).Err()
}
