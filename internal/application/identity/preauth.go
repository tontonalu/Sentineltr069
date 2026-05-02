package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// PreauthStore mantém o estado "passou senha mas falta TOTP" em Redis,
// com TTL curto. Não usamos a tabela sessions porque queremos que esse
// estado seja descartável e impossível de elevar acidentalmente para
// sessão completa.
type PreauthStore struct {
	client *redis.Client
}

const (
	preauthPrefix = "preauth:"
	preauthTTL    = 5 * time.Minute
)

func NewPreauthStore(client *redis.Client) *PreauthStore {
	return &PreauthStore{client: client}
}

// Create gera token aleatório e armazena userID. Retorna o token para
// ser colocado em cookie httpOnly de curta duração.
func (p *PreauthStore) Create(ctx context.Context, userID uuid.UUID) (string, error) {
	if p == nil || p.client == nil {
		return "", errors.New("preauth: store nil")
	}
	token := uuid.NewString()
	key := preauthPrefix + token
	if err := p.client.Set(ctx, key, userID.String(), preauthTTL).Err(); err != nil {
		return "", fmt.Errorf("preauth: set: %w", err)
	}
	return token, nil
}

// Consume devolve o userID associado e apaga a entrada (single-use).
// Se o token expirou ou nunca existiu, retorna erro.
func (p *PreauthStore) Consume(ctx context.Context, token string) (uuid.UUID, error) {
	if p == nil || p.client == nil {
		return uuid.Nil, errors.New("preauth: store nil")
	}
	if token == "" {
		return uuid.Nil, errors.New("preauth: token vazio")
	}
	key := preauthPrefix + token
	val, err := p.client.GetDel(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return uuid.Nil, errors.New("preauth: token expirado ou inválido")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("preauth: getdel: %w", err)
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil, fmt.Errorf("preauth: parse uuid: %w", err)
	}
	return id, nil
}

// Discard apaga o token sem consumir o userID — usado em cancelamentos.
func (p *PreauthStore) Discard(ctx context.Context, token string) error {
	if p == nil || p.client == nil || token == "" {
		return nil
	}
	return p.client.Del(ctx, preauthPrefix+token).Err()
}
