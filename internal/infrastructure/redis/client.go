// Package redis encapsula o cliente go-redis.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client = *redis.Client

func New(ctx context.Context, url string) (Client, error) {
	if url == "" {
		return nil, fmt.Errorf("redis: REDIS_URL vazio")
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url: %w", err)
	}
	c := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return c, nil
}

func Ping(ctx context.Context, c Client) error {
	if c == nil {
		return fmt.Errorf("redis: client nil")
	}
	return c.Ping(ctx).Err()
}
