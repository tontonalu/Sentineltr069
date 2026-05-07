// Package devices contém casos de uso da página /devices/{id} que vão além
// do CRUD básico do inventário (esse fica em application/inventory).
//
// O ProfileView é o cerne da nova página de detalhe com abas: descobre o
// profile homologado do modelo do device, lê os valores atuais via NBI, e
// devolve uma estrutura agrupada por categoria (wifi/wan/lan/...) pronta
// para o templ renderizar como formulários editáveis.
package devices

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	provapp "github.com/celinet/sentinel-acs/internal/application/provisioning"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	inv "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// FieldView — uma linha (1 canonical_key) na UI da página de device.
type FieldView struct {
	CanonicalKey string
	Label        string        // pt-BR derivado de canonical_keys.label_pt ou do próprio key
	Description  string        // canonical_keys.description (tooltip)
	Category     string        // wifi|wan|lan|mgmt|device|voice|other
	TRPath       string
	DataType     tmpl.DataType
	IsSecret     bool
	Writable     bool      // mapping.write_status='ok' OR ('skipped' AND IsSecret)
	CurrentValue string    // último valor lido do NBI (mascarado se secret)
	HasValue     bool      // true se o NBI retornou algo; false se path inexistente
	LastReadAt   time.Time
	Error        string    // erro pontual de leitura (path missing, etc)
	SortOrder    int
}

// CategoryGroup — coleção de fields de uma mesma categoria, na ordem do
// profile homologado (sort_order).
type CategoryGroup struct {
	Category string
	Label    string
	Fields   []FieldView
}

// DeviceProfileView — agregado consumido pelo template de detalhe.
//
// HasHomologation=false significa "este device não tem profile homologado"
// — a UI mostra um banner direcionando para /homologation. Não é erro;
// só uma instrução para o operador completar o fluxo.
type DeviceProfileView struct {
	DeviceID         uuid.UUID
	HasHomologation  bool
	ProfileID        uuid.UUID
	ProfileName      string
	ProfileVersion   int
	HomologatedAt    time.Time
	GeneratedAt      time.Time
	GroupsByCategory map[string]CategoryGroup
	OrderedCategories []string
}

// FieldByCanonicalKey vasculha o agregado para encontrar 1 field específico
// — usado pelo handler de UpdateField após salvar para devolver só a linha
// alterada via HTMX.
func (v *DeviceProfileView) FieldByCanonicalKey(key string) (FieldView, bool) {
	if v == nil {
		return FieldView{}, false
	}
	for _, g := range v.GroupsByCategory {
		for _, f := range g.Fields {
			if f.CanonicalKey == key {
				return f, true
			}
		}
	}
	return FieldView{}, false
}

// ──────────────── Service ────────────────

// Deps interfaces — injeção feita pelo cmd/server para evitar import cycle.

type ProfileLoader interface {
	LoadFull(ctx context.Context, id uuid.UUID) (*tmpl.Profile, error)
}

type DeviceGetter interface {
	GetByID(ctx context.Context, id uuid.UUID) (*inv.Device, error)
}

type CanonicalKeyByKey interface {
	GetByKey(ctx context.Context, key string) (*hom.CanonicalKey, error)
}

// GenieACSReader — só os métodos que o ProfileView precisa do client.
// Permite mock fácil em testes.
type GenieACSReader interface {
	GetDevice(ctx context.Context, deviceID string) (*genieacs.Device, error)
	GetParameterValues(ctx context.Context, deviceID string, paths []string) (genieacs.TaskID, error)
}

// MappingsByProfile carrega mapeamentos de homologação que originaram um
// profile. Usado para descobrir read_status/write_status de cada parâmetro
// (informação que NÃO está no profile, fica na sessão de homologação).
//
// Implementação: percorre sessions com generated_profile_id=profileID e
// concatena os mappings; em prática há sempre 1 sessão por profile, mas
// modelamos como N por segurança.
type MappingsByProfile interface {
	ListByProfile(ctx context.Context, profileID uuid.UUID) ([]hom.Mapping, error)
}

// Service implementa LoadProfileView e UpdateField.
type Service struct {
	devices     DeviceGetter
	homModels   hom.ModelHomologationRepo
	profiles    ProfileLoader
	canonical   CanonicalKeyByKey
	mappings    MappingsByProfile
	genie       GenieACSReader
	provisioner *provapp.Service
}

func NewService(
	devices DeviceGetter,
	homModels hom.ModelHomologationRepo,
	profiles ProfileLoader,
	canonical CanonicalKeyByKey,
	mappings MappingsByProfile,
	genie GenieACSReader,
	provisioner *provapp.Service,
) *Service {
	return &Service{
		devices: devices, homModels: homModels, profiles: profiles,
		canonical: canonical, mappings: mappings, genie: genie,
		provisioner: provisioner,
	}
}

// LoadProfileView é o entrypoint principal. Tolerante a falhas parciais —
// se o NBI estiver lento, devolve campos com Error preenchido em vez de
// abortar a página inteira.
func (s *Service) LoadProfileView(ctx context.Context, deviceID uuid.UUID) (*DeviceProfileView, error) {
	dev, err := s.devices.GetByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	view := &DeviceProfileView{
		DeviceID:          deviceID,
		GroupsByCategory:  map[string]CategoryGroup{},
		OrderedCategories: nil,
	}
	if dev.ModelID == nil {
		return view, nil // sem modelo, sem homologação
	}

	rec, err := s.homModels.FindActiveByModel(ctx, *dev.ModelID)
	if errors.Is(err, hom.ErrModelHomologationNotFound) {
		return view, nil
	}
	if err != nil {
		return view, fmt.Errorf("profile_view: lookup homologação: %w", err)
	}

	prof, err := s.profiles.LoadFull(ctx, rec.ProfileID)
	if err != nil {
		return view, fmt.Errorf("profile_view: carregar profile: %w", err)
	}

	view.HasHomologation = true
	view.ProfileID = prof.ID
	view.ProfileName = prof.Name
	view.ProfileVersion = prof.Version
	view.HomologatedAt = rec.HomologatedAt

	// Mappings da sessão original — para saber Writable de cada canonical_key.
	mappingByKey := map[string]hom.Mapping{}
	if s.mappings != nil {
		ms, err := s.mappings.ListByProfile(ctx, prof.ID)
		if err == nil {
			for _, m := range ms {
				mappingByKey[m.CanonicalKey] = m
			}
		}
	}

	// Snapshot do device a partir do GenieACS (cache 30s).
	now := time.Now()
	var raw map[string]any
	if d, err := s.genie.GetDevice(ctx, dev.GenieACSID); err == nil && d != nil {
		raw = d.Raw
	}
	view.GeneratedAt = now

	// Constroi os FieldViews na ordem do profile.
	tmp := map[string][]FieldView{}
	for _, p := range prof.Parameters {
		f := FieldView{
			CanonicalKey: p.CanonicalKey,
			TRPath:       p.TRPath,
			DataType:     p.DataType,
			IsSecret:     p.IsSecret,
			SortOrder:    p.SortOrder,
			LastReadAt:   now,
		}
		s.enrichFromCanonical(ctx, &f)

		// Decide writable: read_status='ok' AND write_status in ('ok','skipped').
		// Mas vetamos chaves que são logicamente read-only (firmware version,
		// uptime, MAC, BSSID, status/speed/duplex de portas, sinal óptico, etc):
		// mesmo que a sessão de homologação tenha marcado write_status='ok',
		// essas leituras nunca devem virar input editável na UI.
		if m, ok := mappingByKey[p.CanonicalKey]; ok && !isReadOnlyKey(p.CanonicalKey) {
			f.Writable = m.WriteStatus == hom.TestOK ||
				(m.WriteStatus == hom.TestSkipped && p.IsSecret)
		}

		// Lê valor atual via snapshot.
		if raw != nil {
			val := genieacs.ParamString(raw, p.TRPath)
			if val == "" {
				f.Error = "valor não disponível neste device"
				f.HasValue = false
			} else {
				f.HasValue = true
				if f.IsSecret {
					f.CurrentValue = "••••••••"
				} else {
					f.CurrentValue = val
				}
			}
		} else {
			f.Error = "device offline ou snapshot indisponível"
		}

		cat := f.Category
		if cat == "" {
			cat = hom.CategoryOther
		}
		tmp[cat] = append(tmp[cat], f)
	}

	for cat, fields := range tmp {
		sort.SliceStable(fields, func(i, j int) bool {
			return fields[i].SortOrder < fields[j].SortOrder
		})
		view.GroupsByCategory[cat] = CategoryGroup{
			Category: cat,
			Label:    categoryLabel(cat),
			Fields:   fields,
		}
	}
	view.OrderedCategories = orderedCategories(view.GroupsByCategory)
	return view, nil
}

// enrichFromCanonical popula Label/Category/Description a partir do catálogo.
// Tolerante a chave não cadastrada — derive label do próprio key e categoria
// do prefixo (wifi.* → wifi).
func (s *Service) enrichFromCanonical(ctx context.Context, f *FieldView) {
	if s.canonical != nil {
		ck, err := s.canonical.GetByKey(ctx, f.CanonicalKey)
		if err == nil && ck != nil {
			f.Label = ck.LabelPT
			f.Description = ck.Description
			f.Category = ck.Category
			return
		}
	}
	f.Label = humanizeKey(f.CanonicalKey)
	f.Category = inferCategory(f.CanonicalKey)
}

// UpdateField é chamado pelo handler POST /devices/{id}/fields/{canonical_key}.
// Valida que o campo é writable no profile homologado e enfileira 1 job.
func (s *Service) UpdateField(
	ctx context.Context,
	deviceID uuid.UUID,
	canonicalKey, newValue string,
	actorID *uuid.UUID,
) (*FieldView, uuid.UUID, error) {
	view, err := s.LoadProfileView(ctx, deviceID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if !view.HasHomologation {
		return nil, uuid.Nil, fmt.Errorf("profile_view: device sem profile homologado")
	}
	field, ok := view.FieldByCanonicalKey(canonicalKey)
	if !ok {
		return nil, uuid.Nil, fmt.Errorf("profile_view: canonical_key %q não está mapeada no profile %s",
			canonicalKey, view.ProfileName)
	}
	if !field.Writable {
		return nil, uuid.Nil, fmt.Errorf("profile_view: campo %q não é editável (write_status do mapping não é ok/skipped)",
			canonicalKey)
	}
	if s.provisioner == nil {
		return nil, uuid.Nil, fmt.Errorf("profile_view: provisioning service indisponível")
	}

	job, err := s.provisioner.EnqueueSingleField(ctx, provapp.SingleFieldRequest{
		DeviceID:     deviceID,
		CanonicalKey: canonicalKey,
		TRPath:       field.TRPath,
		DataType:     field.DataType,
		RawValue:     newValue,
		IsSecret:     field.IsSecret,
		RequestedBy:  actorID,
	})
	if err != nil {
		return &field, uuid.Nil, err
	}

	// Otimisticamente já mostra o novo valor (mascarado se secret) — o
	// próximo refresh confirmará via NBI. Cache do GenieACS é invalidado
	// pelo postTask interno do client.
	updated := field
	if field.IsSecret {
		updated.CurrentValue = "••••••••"
	} else {
		updated.CurrentValue = strings.TrimSpace(newValue)
	}
	updated.HasValue = true
	updated.LastReadAt = time.Now()
	return &updated, job.ID, nil
}

// ──────────────── helpers ────────────────

func humanizeKey(k string) string {
	parts := strings.Split(k, ".")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " · ")
}

func inferCategory(k string) string {
	idx := strings.Index(k, ".")
	if idx < 0 {
		return hom.CategoryOther
	}
	switch k[:idx] {
	case "wifi":
		return hom.CategoryWiFi
	case "wan", "pppoe", "pon":
		return hom.CategoryWAN
	case "lan":
		return hom.CategoryLAN
	case "mgmt", "time":
		return hom.CategoryMgmt
	case "device", "port":
		return hom.CategoryDevice
	case "voice":
		return hom.CategoryVoice
	}
	return hom.CategoryOther
}

func categoryLabel(cat string) string {
	switch cat {
	case hom.CategoryWiFi:
		return "Wireless"
	case hom.CategoryWAN:
		return "Internet"
	case hom.CategoryLAN:
		return "LAN"
	case hom.CategoryMgmt:
		return "Gerência"
	case hom.CategoryDevice:
		return "Dispositivo"
	case hom.CategoryVoice:
		return "VoIP"
	}
	return "Outros"
}

// isReadOnlyKey — canonical_keys que são puramente informacionais e nunca
// devem virar input editável, mesmo se o homologador marcou write_status='ok'.
//
// Critério: são valores que o CPE expõe via TR-069 mas não aceita
// SetParameterValues (ou aceita silenciosamente sem efeito). Edita-los
// confunde o operador e gera jobs ruidosos no histórico.
func isReadOnlyKey(key string) bool {
	if _, ok := readOnlyKeysExact[key]; ok {
		return true
	}
	for _, prefix := range readOnlyKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// readOnlyKeysExact — match exato.
var readOnlyKeysExact = map[string]struct{}{
	"device.firmware.version":  {},
	"device.uptime":            {},
	"device.serial":            {},
	"device.manufacturer":      {},
	"device.model":             {},
	"device.hardware.version":  {},
	"device.product_class":     {},
	"device.spec_version":      {},
	"device.provisioning_code": {},
	"wan.ip":                   {},
	"wan.mac":                  {},
	"wifi.bssid.2g":            {},
	"wifi.bssid.5g":            {},
	"pon.rx_dbm":               {},
	"pon.tx_dbm":               {},
}

// readOnlyKeyPrefixes — todos os portstatus/speed/duplex (port.lanX.*, port.wan.*)
// são read-only por natureza. Speed/duplex podem ser configurados em alguns
// modelos avançados, mas a UI atual não suporta esse caso de borda.
var readOnlyKeyPrefixes = []string{
	"port.",
}

// orderedCategories devolve as categorias na ordem que faz sentido na UI:
// device → wan → wifi → lan → voice → mgmt → other. Pula categorias vazias.
func orderedCategories(groups map[string]CategoryGroup) []string {
	canonical := []string{
		hom.CategoryDevice,
		hom.CategoryWAN,
		hom.CategoryWiFi,
		hom.CategoryLAN,
		hom.CategoryVoice,
		hom.CategoryMgmt,
		hom.CategoryOther,
	}
	out := make([]string, 0, len(canonical))
	for _, c := range canonical {
		if g, ok := groups[c]; ok && len(g.Fields) > 0 {
			out = append(out, c)
		}
	}
	return out
}
