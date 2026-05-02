// Package ratelimit fornece rate limiting baseado em Redis para endpoints
// sensíveis (login, TOTP verify, webhooks, etc).
//
// Estratégia: contador de janela fixa via INCR + EXPIRE NX. Mais simples e
// mais barato que sliding window precisa, e suficiente para nossos casos
// (anti brute-force, anti-abuse). Para casos de cobrança/quota refinada
// trocamos por implementação ZSET sliding-window depois.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter expõe Allow/Reset. Construído com cliente Redis.
type Limiter struct {
	client *redis.Client
}

func New(client *redis.Client) *Limiter { return &Limiter{client: client} }

// Result do Allow — útil pra setar headers X-RateLimit-*.
type Result struct {
	Allowed   bool
	Count     int64
	Limit     int64
	ResetIn   time.Duration
}

// Allow registra uma tentativa em key. Devolve Allowed=false quando o
// contador da janela atingiu limit.
//
// Em caso de erro de Redis, devolvemos err — caller decide fail-open ou
// fail-closed. O middleware HTTP padrão é fail-open (Redis caído não
// derruba a aplicação).
func (l *Limiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (Result, error) {
	if l == nil || l.client == nil {
		return Result{}, errors.New("ratelimit: limiter nil")
	}
	fullKey := "rl:" + key

	pipe := l.client.Pipeline()
	incrCmd := pipe.Incr(ctx, fullKey)
	pipe.ExpireNX(ctx, fullKey, window) // só seta TTL se a key foi criada agora
	ttlCmd := pipe.TTL(ctx, fullKey)
	if _, err := pipe.Exec(ctx); err != nil {
		return Result{}, fmt.Errorf("ratelimit pipeline: %w", err)
	}

	count := incrCmd.Val()
	ttl := ttlCmd.Val()
	if ttl < 0 {
		ttl = window
	}

	return Result{
		Allowed: count <= limit,
		Count:   count,
		Limit:   limit,
		ResetIn: ttl,
	}, nil
}

// Reset apaga o contador (use após login bem-sucedido para não punir o user).
func (l *Limiter) Reset(ctx context.Context, key string) error {
	if l == nil || l.client == nil {
		return nil
	}
	return l.client.Del(ctx, "rl:"+key).Err()
}
