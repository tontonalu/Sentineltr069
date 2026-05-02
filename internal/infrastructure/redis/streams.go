package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// StreamProvisioning é o nome canônico do stream de jobs de provisioning.
// Worker consome com XReadGroup; criação do consumer group é idempotente.
const StreamProvisioning = "provisioning.requested"

// ProvisioningGroup é o consumer group default. Múltiplos workers consomem
// jobs distintos via mesmo group ID.
const ProvisioningGroup = "provisioning-workers"

// JobNotifier publica IDs de job no stream. Mensagens são compactas
// (apenas job_id) — payload completo está no Postgres.
type JobNotifier struct {
	rdb *redis.Client
}

func NewJobNotifier(rdb *redis.Client) *JobNotifier {
	return &JobNotifier{rdb: rdb}
}

// Notify enfileira o jobID no stream. MaxLen mantém o tamanho razoável —
// histórico longo fica no Postgres (provisioning_jobs), não no Redis.
func (n *JobNotifier) Notify(ctx context.Context, jobID uuid.UUID) error {
	if n == nil || n.rdb == nil {
		return nil
	}
	_, err := n.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamProvisioning,
		MaxLen: 10000,
		Approx: true,
		Values: map[string]any{"job_id": jobID.String()},
	}).Result()
	return err
}

// EnsureGroup cria o consumer group se não existir. MkStream=true permite
// criar o stream do zero quando o primeiro worker sobe antes de qualquer
// publicador.
func EnsureProvisioningGroup(ctx context.Context, rdb *redis.Client) error {
	err := rdb.XGroupCreateMkStream(ctx, StreamProvisioning, ProvisioningGroup, "0").Err()
	if err == nil {
		return nil
	}
	if isBusyGroupError(err) {
		return nil
	}
	return fmt.Errorf("redis: ensure group: %w", err)
}

// ConsumeProvisioning bloqueia até receber 1+ jobs ou timeout. Devolve job IDs
// e os messageIDs para ack posterior. block=0 → espera indefinidamente.
type StreamMessage struct {
	MessageID string
	JobID     uuid.UUID
}

func ConsumeProvisioning(
	ctx context.Context, rdb *redis.Client,
	consumer string, count int64, block time.Duration,
) ([]StreamMessage, error) {
	res, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    ProvisioningGroup,
		Consumer: consumer,
		Streams:  []string{StreamProvisioning, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []StreamMessage
	for _, s := range res {
		for _, m := range s.Messages {
			id, _ := m.Values["job_id"].(string)
			parsed, err := uuid.Parse(id)
			if err != nil {
				// mensagem corrompida — ack para não reentregar.
				_ = AckProvisioning(ctx, rdb, m.ID)
				continue
			}
			out = append(out, StreamMessage{MessageID: m.ID, JobID: parsed})
		}
	}
	return out, nil
}

func AckProvisioning(ctx context.Context, rdb *redis.Client, messageID string) error {
	return rdb.XAck(ctx, StreamProvisioning, ProvisioningGroup, messageID).Err()
}

// isBusyGroupError detecta o erro "BUSYGROUP" do Redis ao tentar criar
// um group já existente — comportamento idempotente desejado.
func isBusyGroupError(err error) bool {
	return err != nil && len(err.Error()) >= 9 && err.Error()[:9] == "BUSYGROUP"
}
