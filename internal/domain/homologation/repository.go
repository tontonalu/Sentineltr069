package homologation

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SessionRepo — persistência do agregado Session.
//
// Save é upsert idempotente. ListMappings/ReplaceMappings cobrem o ciclo
// "carrega tudo, edita, grava de volta" preferido pelo wizard (UI sempre
// envia o conjunto completo de mappings em transições críticas).
type SessionRepo interface {
	Save(ctx context.Context, s *Session) error
	GetByID(ctx context.Context, id uuid.UUID) (*Session, error)
	List(ctx context.Context, f SessionFilter) ([]Session, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status SessionStatus) error
	UpdateTreeSnapshot(ctx context.Context, id uuid.UUID, snapshot []byte) error
	SetGeneratedProfile(ctx context.Context, id uuid.UUID, profileID uuid.UUID) error
	ActiveByDevice(ctx context.Context, deviceID uuid.UUID) (*Session, error)

	// PurgeOldSnapshots zera tree_snapshot de sessões `completed`/`abandoned`
	// finalizadas antes de `before`, mantendo metadados e mappings (auditoria).
	// Retorna o número de linhas afetadas.
	PurgeOldSnapshots(ctx context.Context, before time.Time) (int, error)
}

// SessionFilter — filtros suportados pela listagem.
type SessionFilter struct {
	Status      *SessionStatus
	LabDeviceID *uuid.UUID
	ModelID     *uuid.UUID
	CreatedBy   *uuid.UUID
	Limit       int
	Offset      int
}

// MappingRepo — CRUD por mapping; o agregado vive em Session, mas o ciclo
// de testes (read/write) é por mapping individual e merece API direta.
type MappingRepo interface {
	ListBySession(ctx context.Context, sessionID uuid.UUID) ([]Mapping, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Mapping, error)
	Create(ctx context.Context, m *Mapping) error
	Delete(ctx context.Context, id uuid.UUID) error
	UpdateTemplate(ctx context.Context, id uuid.UUID, valueTemplate string) error
	UpdateReadResult(ctx context.Context, id uuid.UUID, status TestStatus, readValue, errMsg string) error
	UpdateWriteResult(ctx context.Context, id uuid.UUID, status TestStatus, testValue, errMsg string) error
}

// CanonicalKeyRepo — catálogo de chaves padronizadas (read-mostly + admin).
type CanonicalKeyRepo interface {
	List(ctx context.Context, category string) ([]CanonicalKey, error)
	GetByKey(ctx context.Context, key string) (*CanonicalKey, error)
	GetByID(ctx context.Context, id uuid.UUID) (*CanonicalKey, error)
	Create(ctx context.Context, k *CanonicalKey) error
	Update(ctx context.Context, k *CanonicalKey) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// ModelHomologationRepo — registro auditável "modelo X tem profile Y homologado".
//
// IsHomologated é o método consultado pelo gate em provapp.Service.ApplyBulk:
// devolve true se existe ao menos um registro status='homologated' para o par.
type ModelHomologationRepo interface {
	Create(ctx context.Context, h *ModelHomologation) error
	IsHomologated(ctx context.Context, modelID, profileID uuid.UUID) (bool, error)
	ListByModel(ctx context.Context, modelID uuid.UUID) ([]ModelHomologation, error)
	ListByProfile(ctx context.Context, profileID uuid.UUID) ([]ModelHomologation, error)
	Deprecate(ctx context.Context, id uuid.UUID, reason string) error
}
