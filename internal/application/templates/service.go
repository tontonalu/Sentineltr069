package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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
	// ListByModel devolve profiles existentes para um model_id específico.
	// Usado pelo SuggestProfileName na UI de homologação para detectar a
	// próxima versão livre e popular o datalist de autocomplete.
	ListByModel(ctx context.Context, modelID uuid.UUID) ([]tmpl.Profile, error)
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
//
// IsHomologated, quando true, marca o profile como imutável já no Create —
// caso de uso: o homologation.Service.Complete usa este flag para garantir
// que a versão gerada do wizard nunca é editada depois.
type CreateInput struct {
	Name           string
	Description    string
	VendorID       *uuid.UUID
	ModelID        *uuid.UUID
	IsActive       bool
	IsHomologated  bool
	CreatedBy      *uuid.UUID
	Parameters     []tmpl.Parameter
	ChangeNote     string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (*tmpl.Profile, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("templates: name obrigatório")
	}
	if err := validateParameters(in.Parameters); err != nil {
		return nil, err
	}
	p := &tmpl.Profile{
		Name:          strings.TrimSpace(in.Name),
		Description:   in.Description,
		VendorID:      in.VendorID,
		ModelID:       in.ModelID,
		Version:       1,
		IsActive:      in.IsActive,
		IsHomologated: in.IsHomologated,
		CreatedBy:     in.CreatedBy,
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

	// Profiles homologados são imutáveis — só is_active pode mudar (admin
	// aposenta uma versão sem mexer no conteúdo). Qualquer outra mudança
	// exige nova sessão de homologação que gera profile novo (_v2).
	if cur.IsHomologated {
		onlyIsActiveChange := !paramsChanged &&
			cur.Name == in.Name &&
			cur.Description == in.Description &&
			ptrUUIDEqual(cur.VendorID, in.VendorID) &&
			ptrUUIDEqual(cur.ModelID, in.ModelID) &&
			cur.IsActive != in.IsActive
		if !onlyIsActiveChange {
			return nil, tmpl.ErrProfileImmutable
		}
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

// ProfileNameSuggestion — saída do SuggestProfileName para a UI.
type ProfileNameSuggestion struct {
	// Suggested é o nome pré-preenchido no input (próxima versão livre).
	Suggested string
	// Existing são os nomes já usados para o mesmo modelo, para alimentar o
	// <datalist> de autocomplete. Inclui homologados e não-homologados.
	Existing []string
}

// profileVersionPattern bate o padrão "<algo>_v<num>" no fim do nome — usado
// pra detectar versões existentes e calcular a próxima. O grupo \d+ pega o
// número; tudo antes (incluindo o "_v") é o "stem" do nome.
var profileVersionPattern = regexp.MustCompile(`^(.*)_v(\d+)$`)

// SuggestProfileName olha os profiles já cadastrados para um modelo e devolve:
//   - um nome sugerido com a próxima versão livre (ex.: "Vsol_V2804AX_homologated_v3"),
//   - a lista dos nomes existentes (alimenta datalist de autocomplete).
//
// Algoritmo:
//  1. monta o "stem" base "{vendor}_{model}_homologated".
//  2. lista profiles do modelo; extrai sufixos "_vN" via regex.
//  3. próxima versão = max(N) + 1 (ou 1 se nada existir).
//
// Se modelID for nil, devolve sugestão genérica sem consultar DB.
func (s *Service) SuggestProfileName(ctx context.Context, vendor, model string, modelID *uuid.UUID) (*ProfileNameSuggestion, error) {
	stem := buildProfileStem(vendor, model)
	if modelID == nil {
		return &ProfileNameSuggestion{Suggested: stem + "_v1"}, nil
	}
	profiles, err := s.profiles.ListByModel(ctx, *modelID)
	if err != nil {
		return nil, err
	}

	maxVersion := 0
	existing := make([]string, 0, len(profiles))
	stemPrefix := stem + "_v"
	for _, p := range profiles {
		existing = append(existing, p.Name)
		// Só considera o sufixo se o nome começar com o stem deste vendor/model.
		// Profiles de outros nomes (manuais) não influenciam a versão.
		if !strings.HasPrefix(p.Name, stemPrefix) {
			continue
		}
		m := profileVersionPattern.FindStringSubmatch(p.Name)
		if len(m) < 3 {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			continue
		}
		if n > maxVersion {
			maxVersion = n
		}
	}
	return &ProfileNameSuggestion{
		Suggested: fmt.Sprintf("%s_v%d", stem, maxVersion+1),
		Existing:  existing,
	}, nil
}

// buildProfileStem normaliza o prefixo "vendor_model_homologated" — espaços
// viram underscore, evitando nomes inválidos.
func buildProfileStem(vendor, model string) string {
	v := strings.ReplaceAll(strings.TrimSpace(vendor), " ", "_")
	m := strings.ReplaceAll(strings.TrimSpace(model), " ", "_")
	if v == "" && m == "" {
		return "homologated_profile"
	}
	if v == "" {
		return m + "_homologated"
	}
	if m == "" {
		return v + "_homologated"
	}
	return v + "_" + m + "_homologated"
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
