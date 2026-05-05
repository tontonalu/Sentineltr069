package homologation

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"

	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	inv "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// AutoMapSuggestion — proposta de mapeamento gerada pela heurística.
//
// Uma sugestão diz: "a chave canônica X provavelmente bate com o tr_path Y
// nesta árvore". O operador confirma antes de virar Mapping efetivo.
type AutoMapSuggestion struct {
	CanonicalKey string
	LabelPT      string
	TRPath       string
	DataType     string
	IsSecret     bool
	HitValue     any  // valor encontrado no snapshot, útil pra preview
}

// AutoMapResult — saída de SuggestMappings, agregando as sugestões e o que
// não foi encontrado para o operador entender as lacunas.
type AutoMapResult struct {
	Suggestions []AutoMapSuggestion
	Missing     []string // canonical_keys cujo nenhum hint path bateu
	Existing    []string // canonical_keys já mapeadas (skipped — não duplica)
}

// SuggestMappings percorre o catálogo de canonical_keys e procura, para cada,
// um hint_path do TR data model do device de lab que exista na árvore sondada.
// O primeiro hit (na ordem dos hints) vira sugestão. canonical_keys já
// mapeadas na sessão são puladas — saída fica em `Existing`.
//
// Estratégia simples: O(N hints × M paths) por canonical_key. Como hints
// é uma lista pequena (1-3 entries por chave) e o lookup na árvore é via
// FlattenTree pré-computado, fica trivialmente rápido.
func (s *Service) SuggestMappings(ctx context.Context, sessionID uuid.UUID) (*AutoMapResult, error) {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !sess.Status.IsActive() {
		return nil, hom.ErrSessionNotActive
	}
	if len(sess.TreeSnapshot) == 0 {
		return nil, hom.ErrSessionMissingTree
	}

	model, err := s.models.GetByID(ctx, sess.ModelID)
	if err != nil {
		return nil, err
	}

	var raw map[string]any
	if err := json.Unmarshal(sess.TreeSnapshot, &raw); err != nil {
		return nil, err
	}

	keys, err := s.canonical.List(ctx, "")
	if err != nil {
		return nil, err
	}

	existingMappings, err := s.mappings.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	existingKeys := make(map[string]bool, len(existingMappings))
	for _, m := range existingMappings {
		existingKeys[m.CanonicalKey] = true
	}

	res := &AutoMapResult{}
	for _, k := range keys {
		if existingKeys[k.Key] {
			res.Existing = append(res.Existing, k.Key)
			continue
		}
		hints := pickHints(k, model)
		var matched string
		var hitValue any
		for _, h := range hints {
			if v := genieacs.ParamValue(raw, h); v != nil {
				matched = h
				hitValue = v
				break
			}
		}
		if matched == "" {
			res.Missing = append(res.Missing, k.Key)
			continue
		}
		res.Suggestions = append(res.Suggestions, AutoMapSuggestion{
			CanonicalKey: k.Key,
			LabelPT:      k.LabelPT,
			TRPath:       matched,
			DataType:     string(k.SuggestedDataType),
			IsSecret:     k.DefaultIsSecret,
			HitValue:     hitValue,
		})
	}
	return res, nil
}

// ApplyAutoMap pega as suggestions geradas por SuggestMappings (passadas pelo
// caller após confirmação visual do operador) e cria os Mappings na sessão.
// Retorna o número de mappings criados e o primeiro erro fatal (se houver).
//
// Mappings duplicados (canonical_key já existente) são silenciosamente pulados
// — combina com a lógica do SuggestMappings que filtra `Existing`.
func (s *Service) ApplyAutoMap(ctx context.Context, sessionID uuid.UUID, suggestions []AutoMapSuggestion) (int, error) {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if !sess.Status.IsActive() {
		return 0, hom.ErrSessionNotActive
	}
	created := 0
	for _, sg := range suggestions {
		if strings.TrimSpace(sg.CanonicalKey) == "" || strings.TrimSpace(sg.TRPath) == "" {
			continue
		}
		dt := tmpl.DataType(sg.DataType)
		if !dt.Valid() {
			dt = tmpl.DataTypeString
		}
		_, err := s.AddMapping(ctx, AddMappingInput{
			SessionID:    sessionID,
			CanonicalKey: sg.CanonicalKey,
			TRPath:       sg.TRPath,
			DataType:     dt,
			IsSecret:     sg.IsSecret,
		})
		if err == nil {
			created++
			continue
		}
		// duplicate é OK (sessão pode ter ganhado mapping entre Suggest e Apply)
		if err == hom.ErrMappingDuplicate {
			continue
		}
		return created, err
	}
	return created, nil
}

// pickHints escolhe a lista correta de hints conforme o TR data model do device.
// Se o data model do model não for nem TR-098 nem TR-181, usa ambos (best effort).
func pickHints(k hom.CanonicalKey, model *inv.DeviceModel) []string {
	switch model.TRDataModel {
	case inv.TR098:
		return k.HintPathsTR098
	case inv.TR181:
		return k.HintPathsTR181
	}
	out := append([]string{}, k.HintPathsTR098...)
	return append(out, k.HintPathsTR181...)
}
