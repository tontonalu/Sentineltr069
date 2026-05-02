package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// ProfileRepo — interface mínima que o service consome do Postgres.
type ProfileRepo interface {
	Create(ctx context.Context, p *tmpl.Profile) error
	Update(ctx context.Context, p *tmpl.Profile) error
	GetByID(ctx context.Context, id uuid.UUID) (*tmpl.Profile, error)
	IncrementVersion(ctx context.Context, id uuid.UUID) (int, error)
	SetActive(ctx context.Context, id uuid.UUID, active bool) error
}

type ParameterRepo interface {
	ListByProfile(ctx context.Context, profileID uuid.UUID) ([]tmpl.Parameter, error)
	Replace(ctx context.Context, profileID uuid.UUID, params []tmpl.Parameter) error
}

type HistoryRepo interface {
	Append(ctx context.Context, e *tmpl.HistoryEntry) error
}

// Service orquestra create/update de profile + history snapshot + versionamento.
//
// Contrato:
//   - Save: detecta diff em parâmetros; se mudou (ou cabeçalho mudou), bump
//     version e grava entrada no history.
//   - Snapshot é JSON do profile + parameters no momento. Imutável.
type Service struct {
	profiles ProfileRepo
	params   ParameterRepo
	history  HistoryRepo
}

func NewService(p ProfileRepo, pr ParameterRepo, h HistoryRepo) *Service {
	return &Service{profiles: p, params: pr, history: h}
}

// CreateInput — payload da UI ao criar profile.
type CreateInput struct {
	Name        string
	Description string
	VendorID    *uuid.UUID
	ModelID     *uuid.UUID
	IsActive    bool
	CreatedBy   *uuid.UUID
	Parameters  []tmpl.Parameter
	ChangeNote  string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*tmpl.Profile, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("templates: name obrigatório")
	}
	if err := validateParameters(in.Parameters); err != nil {
		return nil, err
	}
	p := &tmpl.Profile{
		Name:        strings.TrimSpace(in.Name),
		Description: in.Description,
		VendorID:    in.VendorID,
		ModelID:     in.ModelID,
		Version:     1,
		IsActive:    in.IsActive,
		CreatedBy:   in.CreatedBy,
	}
	if err := s.profiles.Create(ctx, p); err != nil {
		return nil, err
	}
	if err := s.params.Replace(ctx, p.ID, in.Parameters); err != nil {
		return nil, err
	}
	p.Parameters, _ = s.params.ListByProfile(ctx, p.ID)
	if err := s.appendHistory(ctx, p, in.CreatedBy, in.ChangeNote); err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateInput — sempre vem com o conjunto completo de parâmetros (UI envia
// tabela inteira), simplificando o diff. Se any campo material mudou, bump.
type UpdateInput struct {
	ID          uuid.UUID
	Name        string
	Description string
	VendorID    *uuid.UUID
	ModelID     *uuid.UUID
	IsActive    bool
	Parameters  []tmpl.Parameter
	ChangedBy   *uuid.UUID
	ChangeNote  string
}

// Update aplica edição. Lógica:
//  1. carrega profile + params atuais.
//  2. compara cabeçalho e params com input.
//  3. se igual → no-op (retorna profile sem incrementar).
//  4. se diferente → grava + IncrementVersion + history snapshot.
func (s *Service) Update(ctx context.Context, in UpdateInput) (*tmpl.Profile, error) {
	if err := validateParameters(in.Parameters); err != nil {
		return nil, err
	}
	cur, err := s.profiles.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	curParams, err := s.params.ListByProfile(ctx, in.ID)
	if err != nil {
		return nil, err
	}

	headerChanged := cur.Name != in.Name ||
		cur.Description != in.Description ||
		!ptrUUIDEqual(cur.VendorID, in.VendorID) ||
		!ptrUUIDEqual(cur.ModelID, in.ModelID) ||
		cur.IsActive != in.IsActive
	paramsChanged := !sameParameters(curParams, in.Parameters)

	if !headerChanged && !paramsChanged {
		cur.Parameters = curParams
		return cur, nil
	}

	cur.Name = strings.TrimSpace(in.Name)
	cur.Description = in.Description
	cur.VendorID = in.VendorID
	cur.ModelID = in.ModelID
	cur.IsActive = in.IsActive
	if err := s.profiles.Update(ctx, cur); err != nil {
		return nil, err
	}
	if paramsChanged {
		if err := s.params.Replace(ctx, cur.ID, in.Parameters); err != nil {
			return nil, err
		}
	}
	newVer, err := s.profiles.IncrementVersion(ctx, cur.ID)
	if err != nil {
		return nil, err
	}
	cur.Version = newVer
	cur.UpdatedAt = time.Now()
	cur.Parameters, _ = s.params.ListByProfile(ctx, cur.ID)
	if err := s.appendHistory(ctx, cur, in.ChangedBy, in.ChangeNote); err != nil {
		return nil, err
	}
	return cur, nil
}

// LoadFull lê profile + parameters em uma chamada — usada por list/detail.
func (s *Service) LoadFull(ctx context.Context, id uuid.UUID) (*tmpl.Profile, error) {
	p, err := s.profiles.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	p.Parameters, err = s.params.ListByProfile(ctx, id)
	return p, err
}

func (s *Service) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	return s.profiles.SetActive(ctx, id, active)
}

// ──────────────── helpers ────────────────

func (s *Service) appendHistory(ctx context.Context, p *tmpl.Profile, by *uuid.UUID, note string) error {
	snap, err := json.Marshal(map[string]any{
		"profile":    p,
		"parameters": p.Parameters,
	})
	if err != nil {
		return err
	}
	return s.history.Append(ctx, &tmpl.HistoryEntry{
		ProfileID:  p.ID,
		Version:    p.Version,
		Snapshot:   snap,
		ChangedBy:  by,
		ChangeNote: note,
	})
}

func validateParameters(ps []tmpl.Parameter) error {
	if len(ps) == 0 {
		return tmpl.ErrEmptyParameters
	}
	seen := map[string]bool{}
	for i, p := range ps {
		if strings.TrimSpace(p.CanonicalKey) == "" || strings.TrimSpace(p.TRPath) == "" {
			return fmt.Errorf("parametro #%d: canonical_key e tr_path obrigatórios", i+1)
		}
		if !p.DataType.Valid() {
			return tmpl.ErrInvalidDataType
		}
		if seen[p.CanonicalKey] {
			return fmt.Errorf("canonical_key duplicada: %q", p.CanonicalKey)
		}
		seen[p.CanonicalKey] = true
	}
	return nil
}

func ptrUUIDEqual(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// sameParameters compara dois conjuntos sem considerar ID/SortOrder
// (campos derivados ou do banco). Ordem-independente: ordena por canonical_key.
func sameParameters(a, b []tmpl.Parameter) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]tmpl.Parameter, len(a))
	for _, p := range a {
		idx[p.CanonicalKey] = p
	}
	for _, p := range b {
		other, ok := idx[p.CanonicalKey]
		if !ok {
			return false
		}
		if other.TRPath != p.TRPath ||
			other.ValueTemplate != p.ValueTemplate ||
			other.DataType != p.DataType ||
			other.IsSecret != p.IsSecret {
			return false
		}
	}
	return true
}
