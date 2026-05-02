package genieacs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache controla cache opcional para GetDevice. Set por WithCache; nil = desligado.
type Cache struct {
	client *redis.Client
	ttl    time.Duration
}

// WithCache habilita cache Redis para GetDevice. Mutações (SetParameterValues,
// Reboot etc.) invalidam automaticamente. ttl <= 0 desabilita.
func (c *Client) WithCache(r *redis.Client, ttl time.Duration) *Client {
	if r == nil || ttl <= 0 {
		c.cache = nil
		return c
	}
	c.cache = &Cache{client: r, ttl: ttl}
	return c
}

const cacheKeyDevicePrefix = "genieacs:device:"

// cacheGet tenta recuperar Raw de um device. miss + erro inocente devolvem (nil, false).
func (c *Client) cacheGet(ctx context.Context, deviceID string) (map[string]any, bool) {
	if c.cache == nil || c.cache.client == nil {
		return nil, false
	}
	data, err := c.cache.client.Get(ctx, cacheKeyDevicePrefix+deviceID).Bytes()
	if err != nil {
		return nil, false
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	return raw, true
}

func (c *Client) cacheSet(ctx context.Context, deviceID string, raw map[string]any) {
	if c.cache == nil || c.cache.client == nil || raw == nil {
		return
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return
	}
	// Cache miss → não bloqueia o caller; falha silenciosa em telemetria.
	_ = c.cache.client.Set(ctx, cacheKeyDevicePrefix+deviceID, data, c.cache.ttl).Err()
}

// InvalidateDevice apaga a entrada de cache do device. Chamada após writes.
func (c *Client) InvalidateDevice(ctx context.Context, deviceID string) {
	if c.cache == nil || c.cache.client == nil {
		return
	}
	_ = c.cache.client.Del(ctx, cacheKeyDevicePrefix+deviceID).Err()
}

// errCacheMiss é interno — usado para detectar Get vazio sem alocar.
var errCacheMiss = errors.New("genieacs: cache miss")
