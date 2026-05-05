// Package homologation contém o caso de uso central do wizard de homologação.
//
// Fluxo:
//   1. StartSession  — operador escolhe device de lab.
//   2. Probe         — sonda árvore TR-069 via GenieACS NBI.
//   3. BrowseTree    — navega/filtra a árvore sondada.
//   4. AddMapping    — operador associa um path a uma canonical_key.
//   5. RunReadTest   — valida que o path retorna valor.
//   6. RunWriteTest  — opcional, valida SetParameterValues + restore.
//   7. Complete      — gera config_profile homologado vinculado ao model.
//
// O service NÃO duplica versionamento/history de templates: delega tudo a
// tplapp.Service.Create.
package homologation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	inv "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// GenieACSPort é a fatia mínima do cliente GenieACS que o service consome.
// Interface (em vez de *genieacs.Client direto) facilita fakes em testes.
type GenieACSPort interface {
	GetDevice(ctx context.Context, deviceID string) (*genieacs.Device, error)
	Refresh(ctx context.Context, deviceID, objectName string) (genieacs.TaskID, error)
	GetParameterValues(ctx context.Context, deviceID string, paths []string) (genieacs.TaskID, error)
	SetParameterValues(ctx context.Context, deviceID string, params []genieacs.Parameter) (genieacs.TaskID, error)
}

// Service orquestra o ciclo do wizard.
type Service struct {
	sessions   hom.SessionRepo
	mappings   hom.MappingRepo
	canonical  hom.CanonicalKeyRepo
	homModel   hom.ModelHomologationRepo
	devices    inv.DeviceRepository
	models     inv.DeviceModelRepository
	tpl        *tplapp.Service
	genieacs   GenieACSPort
}

// NewService monta o service com todas as dependências. Caller-side wiring
// fica em cmd/server/main.go.
func NewService(
	sessions hom.SessionRepo,
	mappings hom.MappingRepo,
	canonical hom.CanonicalKeyRepo,
	homModel hom.ModelHomologationRepo,
	devices inv.DeviceRepository,
	models inv.DeviceModelRepository,
	tpl *tplapp.Service,
	gen GenieACSPort,
) *Service {
	return &Service{
		sessions:  sessions,
		mappings:  mappings,
		canonical: canonical,
		homModel:  homModel,
		devices:   devices,
		models:    models,
		tpl:       tpl,
		genieacs:  gen,
	}
}

// ──────────────── StartSession ────────────────

// StartSession valida invariantes (lab, model_id, no-active-session) e cria a
// sessão em status='draft'. Retorna ErrSessionAlreadyActive se outra sessão
// está aberta no mesmo device.
func (s *Service) StartSession(ctx context.Context, labDeviceID uuid.UUID, userID *uuid.UUID) (*hom.Session, error) {
	d, err := s.devices.GetByID(ctx, labDeviceID)
	if err != nil {
		return nil, err
	}
	if !d.IsHomologationLab {
		return nil, hom.ErrDeviceNotLab
	}
	if d.ModelID == nil {
		return nil, hom.ErrSessionMissingModel
	}
	if active, _ := s.sessions.ActiveByDevice(ctx, labDeviceID); active != nil {
		return nil, hom.ErrSessionAlreadyActive
	}
	sess := &hom.Session{
		LabDeviceID: labDeviceID,
		ModelID:     *d.ModelID,
		Status:      hom.SessionDraft,
		CreatedBy:   userID,
		StartedAt:   time.Now(),
	}
	if err := s.sessions.Save(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

// GetSession recarrega session + mappings em uma chamada.
func (s *Service) GetSession(ctx context.Context, id uuid.UUID) (*hom.Session, error) {
	sess, err := s.sessions.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	sess.Mappings, err = s.mappings.ListBySession(ctx, id)
	return sess, err
}

// ──────────────── Probe ────────────────

// Probe pede ao GenieACS para revalidar a árvore inteira do device de lab e
// guarda o snapshot em tree_snapshot. Versão Day 2: chama Refresh fire-and-forget,
// depois lê o estado mais recente conhecido via GetDevice — não bloqueia
// esperando a task completar (tasks de Refresh em árvore inteira podem demorar
// minutos em alguns CPEs). Operador pode disparar Probe novamente.
//
// Recovery: sessão é marcada `probing` no início; em qualquer erro (rede, NBI,
// marshal) restaura o status de origem antes de retornar. Sem isso, sessão
// ficaria presa em `probing` e o índice único parcial bloquearia novas tentativas.
func (s *Service) Probe(ctx context.Context, sessionID uuid.UUID) (*hom.Session, error) {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Status == hom.SessionCompleted || sess.Status == hom.SessionAbandoned {
		return nil, hom.ErrSessionNotActive
	}
	d, err := s.devices.GetByID(ctx, sess.LabDeviceID)
	if err != nil {
		return nil, err
	}

	// Status de origem para rollback em caso de erro. Se já estávamos em
	// `testing` (Probe disparado de novo), volta pra testing — operador
	// continua trabalhando com a árvore antiga.
	originalStatus := sess.Status
	if !originalStatus.IsActive() {
		originalStatus = hom.SessionDraft
	}

	// Marca probing antes de bater no NBI — UI pode renderizar spinner.
	if err := s.sessions.UpdateStatus(ctx, sessionID, hom.SessionProbing); err != nil {
		return nil, err
	}

	// Garante recovery do status mesmo se houver panic (raro, mas defesa
	// barata) ou retorno via path de erro abaixo.
	probeOK := false
	defer func() {
		if !probeOK {
			_ = s.sessions.UpdateStatus(context.Background(), sessionID, originalStatus)
		}
	}()

	// Refresh é "best effort". Erro aqui não fatal: ainda podemos trabalhar
	// com a árvore que já está no NBI desde o último Inform.
	_, _ = s.genieacs.Refresh(ctx, d.GenieACSID, "")

	// Lê snapshot atual (NBI mantém a última árvore conhecida em qualquer caso).
	dev, err := s.genieacs.GetDevice(ctx, d.GenieACSID)
	if err != nil {
		return nil, fmt.Errorf("homologation: GetDevice: %w", err)
	}

	// Sanitiza secrets (senhas Wi-Fi/PPPoE/SIP) antes de persistir.
	// In-place: dev.Raw é descartado depois desta função.
	genieacs.SanitizeTree(dev.Raw)

	snap, err := json.Marshal(dev.Raw)
	if err != nil {
		return nil, fmt.Errorf("homologation: marshal snapshot: %w", err)
	}
	if err := s.sessions.UpdateTreeSnapshot(ctx, sessionID, snap); err != nil {
		return nil, err
	}
	if err := s.sessions.UpdateStatus(ctx, sessionID, hom.SessionTesting); err != nil {
		return nil, err
	}
	probeOK = true
	return s.GetSession(ctx, sessionID)
}

// ──────────────── BrowseTree ────────────────

// BrowseTree devolve a árvore TR-069 da sessão filtrada por prefix e search.
// Lê de tree_snapshot (não rebate o NBI). Retorna ErrSessionMissingTree se
// Probe ainda não foi executado.
func (s *Service) BrowseTree(ctx context.Context, sessionID uuid.UUID, prefix, search string) ([]genieacs.TreeEntry, error) {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(sess.TreeSnapshot) == 0 {
		return nil, hom.ErrSessionMissingTree
	}
	var raw map[string]any
	if err := json.Unmarshal(sess.TreeSnapshot, &raw); err != nil {
		return nil, fmt.Errorf("homologation: unmarshal snapshot: %w", err)
	}
	entries := genieacs.FlattenTree(raw)
	return genieacs.FilterTree(entries, prefix, search), nil
}

// ──────────────── Mappings ────────────────

// AddMappingInput — payload para criar um mapeamento. ValueTemplate aceita
// sintaxe do engine de templates (ex: "{{ customer.pppoe_login }}_2G").
type AddMappingInput struct {
	SessionID     uuid.UUID
	CanonicalKey  string
	TRPath        string
	ValueTemplate string
	DataType      tmpl.DataType
	IsSecret      bool
}

func (s *Service) AddMapping(ctx context.Context, in AddMappingInput) (*hom.Mapping, error) {
	in.CanonicalKey = strings.TrimSpace(in.CanonicalKey)
	in.TRPath = strings.TrimSpace(in.TRPath)
	if in.CanonicalKey == "" || in.TRPath == "" {
		return nil, fmt.Errorf("homologation: canonical_key e tr_path obrigatórios")
	}
	if !in.DataType.Valid() {
		return nil, tmpl.ErrInvalidDataType
	}
	sess, err := s.sessions.GetByID(ctx, in.SessionID)
	if err != nil {
		return nil, err
	}
	if !sess.Status.IsActive() {
		return nil, hom.ErrSessionNotActive
	}
	existing, _ := s.mappings.ListBySession(ctx, in.SessionID)
	m := &hom.Mapping{
		SessionID:     in.SessionID,
		CanonicalKey:  in.CanonicalKey,
		TRPath:        in.TRPath,
		ValueTemplate: in.ValueTemplate,
		DataType:      in.DataType,
		IsSecret:      in.IsSecret,
		SortOrder:     len(existing) + 1,
		ReadStatus:    hom.TestPending,
		WriteStatus:   hom.TestPending,
	}
	if err := s.mappings.Create(ctx, m); err != nil {
		return nil, err
	}
	return m, nil
}

// UpdateMappingTemplate atualiza apenas o value_template de um mapping sem
// alterar canonical_key/tr_path/data_type. Sessão precisa estar ativa.
func (s *Service) UpdateMappingTemplate(ctx context.Context, sessionID, mappingID uuid.UUID, valueTemplate string) error {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if !sess.Status.IsActive() {
		return hom.ErrSessionNotActive
	}
	return s.mappings.UpdateTemplate(ctx, mappingID, valueTemplate)
}

func (s *Service) RemoveMapping(ctx context.Context, sessionID, mappingID uuid.UUID) error {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if !sess.Status.IsActive() {
		return hom.ErrSessionNotActive
	}
	return s.mappings.Delete(ctx, mappingID)
}

// ──────────────── Tests ────────────────

// RunReadTest pede ao GenieACS o valor atual do tr_path do mapping e marca
// o resultado. Versão Day 2: lê do tree_snapshot (já capturado no Probe). Se
// quiser revalidar contra o CPE em tempo real, usar RunReadTestLive (Day 3).
func (s *Service) RunReadTest(ctx context.Context, mappingID uuid.UUID) (*hom.Mapping, error) {
	m, err := s.mappings.GetByID(ctx, mappingID)
	if err != nil {
		return nil, err
	}
	sess, err := s.sessions.GetByID(ctx, m.SessionID)
	if err != nil {
		return nil, err
	}
	if !sess.Status.IsActive() {
		return nil, hom.ErrSessionNotActive
	}
	if len(sess.TreeSnapshot) == 0 {
		return nil, hom.ErrSessionMissingTree
	}
	var raw map[string]any
	if err := json.Unmarshal(sess.TreeSnapshot, &raw); err != nil {
		return nil, err
	}
	val := genieacs.ParamValue(raw, m.TRPath)
	if val == nil {
		errMsg := "path não encontrado na árvore TR-069 sondada"
		if err := s.mappings.UpdateReadResult(ctx, mappingID, hom.TestFail, "", errMsg); err != nil {
			return nil, err
		}
		return s.mappings.GetByID(ctx, mappingID)
	}
	// Para mappings marcados como secret, não persistimos o valor real lido —
	// armazenamos apenas a marca "(redacted)" para que o operador saiba que o
	// path retornou algo, mas o valor não vaza no banco.
	readVal := genieacs.ParamString(raw, m.TRPath)
	if m.IsSecret {
		readVal = redactedMarker
	}
	if err := s.mappings.UpdateReadResult(ctx, mappingID, hom.TestOK, readVal, ""); err != nil {
		return nil, err
	}
	return s.mappings.GetByID(ctx, mappingID)
}

// redactedMarker é o placeholder usado em mappings.read_value quando is_secret=true.
// Visível na UI; serve só para sinalizar que o read passou sem expor o valor.
const redactedMarker = "(redacted)"

// RunWriteTestInput — payload para teste de escrita.
type RunWriteTestInput struct {
	MappingID       uuid.UUID
	TestValue       string // valor a escrever
	RestoreOriginal bool   // se true, escreve testValue → confirma → escreve original de volta
}

// RunWriteTest envia SetParameterValues com o valor de teste. is_secret força
// skipped (não fazemos write em senha pra evitar destruir o original).
//
// Day 2: implementação síncrona simples — postTask retorna task_id, marcamos
// OK se NBI aceitou e nenhuma fault apareceu rapidamente. Validação completa
// (poll de fault, verify-after-write) fica para Day 3.
func (s *Service) RunWriteTest(ctx context.Context, in RunWriteTestInput) (*hom.Mapping, error) {
	m, err := s.mappings.GetByID(ctx, in.MappingID)
	if err != nil {
		return nil, err
	}
	if m.IsSecret {
		_ = s.mappings.UpdateWriteResult(ctx, in.MappingID, hom.TestSkipped, "", "is_secret=true: write não testado")
		return s.mappings.GetByID(ctx, in.MappingID)
	}
	sess, err := s.sessions.GetByID(ctx, m.SessionID)
	if err != nil {
		return nil, err
	}
	if !sess.Status.IsActive() {
		return nil, hom.ErrSessionNotActive
	}
	d, err := s.devices.GetByID(ctx, sess.LabDeviceID)
	if err != nil {
		return nil, err
	}

	param := genieacs.Parameter{
		Path:  m.TRPath,
		Value: in.TestValue,
		Type:  m.DataType.XSD(),
	}
	if _, err := s.genieacs.SetParameterValues(ctx, d.GenieACSID, []genieacs.Parameter{param}); err != nil {
		_ = s.mappings.UpdateWriteResult(ctx, in.MappingID, hom.TestFail, in.TestValue, err.Error())
		return s.mappings.GetByID(ctx, in.MappingID)
	}

	// Restore opcional: lê valor original do snapshot e re-grava.
	if in.RestoreOriginal && m.ReadValue != nil && *m.ReadValue != "" {
		restore := genieacs.Parameter{Path: m.TRPath, Value: *m.ReadValue, Type: m.DataType.XSD()}
		_, _ = s.genieacs.SetParameterValues(ctx, d.GenieACSID, []genieacs.Parameter{restore})
	}

	_ = s.mappings.UpdateWriteResult(ctx, in.MappingID, hom.TestOK, in.TestValue, "")
	return s.mappings.GetByID(ctx, in.MappingID)
}

// ──────────────── Maintenance ────────────────

// PurgeOldSnapshots remove tree_snapshot de sessões finalizadas antes de
// `before`. Útil em job periódico para liberar espaço (snapshots de árvore
// inteira chegam a 1-2 MB cada). Mantém metadados e mappings — auditoria
// continua intacta. Idempotente.
func (s *Service) PurgeOldSnapshots(ctx context.Context, before time.Time) (int, error) {
	return s.sessions.PurgeOldSnapshots(ctx, before)
}

// ──────────────── Complete ────────────────

// CompleteInput — payload para finalizar a sessão e gerar o profile.
type CompleteInput struct {
	SessionID   uuid.UUID
	ProfileName string
	Description string
	UserID      *uuid.UUID
	ChangeNote  string
}

// Complete materializa o profile a partir dos mappings elegíveis e grava
// o registro de homologação para o modelo. Erro se nenhum mapping passou.
func (s *Service) Complete(ctx context.Context, in CompleteInput) (*tmpl.Profile, error) {
	in.ProfileName = strings.TrimSpace(in.ProfileName)
	if in.ProfileName == "" {
		return nil, fmt.Errorf("homologation: nome do profile obrigatório")
	}
	sess, err := s.sessions.GetByID(ctx, in.SessionID)
	if err != nil {
		return nil, err
	}
	if !sess.Status.IsActive() {
		return nil, hom.ErrSessionNotActive
	}
	mappings, err := s.mappings.ListBySession(ctx, in.SessionID)
	if err != nil {
		return nil, err
	}

	params := make([]tmpl.Parameter, 0, len(mappings))
	for _, m := range mappings {
		if !m.EligibleForProfile() {
			continue
		}
		// Default value_template = "{{ device.serial }}" pra mappings sem template?
		// Não — se operador não definiu, usa o valor lido como literal.
		valueTemplate := m.ValueTemplate
		if valueTemplate == "" && m.ReadValue != nil {
			valueTemplate = *m.ReadValue
		}
		params = append(params, tmpl.Parameter{
			CanonicalKey:  m.CanonicalKey,
			TRPath:        m.TRPath,
			ValueTemplate: valueTemplate,
			DataType:      m.DataType,
			IsSecret:      m.IsSecret,
		})
	}
	if len(params) == 0 {
		return nil, hom.ErrNoEligibleMappings
	}

	// Vendor do model — necessário para o profile saber o vendor pai.
	model, err := s.models.GetByID(ctx, sess.ModelID)
	if err != nil {
		return nil, err
	}
	vendorID := model.VendorID

	prof, err := s.tpl.Create(ctx, tplapp.CreateInput{
		Name:        in.ProfileName,
		Description: in.Description,
		VendorID:    &vendorID,
		ModelID:     &sess.ModelID,
		IsActive:    true,
		CreatedBy:   in.UserID,
		Parameters:  params,
		ChangeNote:  in.ChangeNote,
	})
	if err != nil {
		return nil, err
	}

	// Grava homologação por modelo + vincula ao session + marca completed.
	hh := &hom.ModelHomologation{
		ModelID:       sess.ModelID,
		ProfileID:     prof.ID,
		SessionID:     &sess.ID,
		HomologatedBy: in.UserID,
		Status:        hom.StatusHomologated,
	}
	if err := s.homModel.Create(ctx, hh); err != nil {
		return nil, err
	}
	if err := s.sessions.SetGeneratedProfile(ctx, sess.ID, prof.ID); err != nil {
		return nil, err
	}
	if err := s.sessions.UpdateStatus(ctx, sess.ID, hom.SessionCompleted); err != nil {
		return nil, err
	}
	return prof, nil
}

// Abandon marca a sessão como abandonada — usado quando operador desiste.
func (s *Service) Abandon(ctx context.Context, sessionID uuid.UUID) error {
	return s.sessions.UpdateStatus(ctx, sessionID, hom.SessionAbandoned)
}

// ──────────────── List helpers ────────────────

func (s *Service) ListSessions(ctx context.Context, f hom.SessionFilter) ([]hom.Session, error) {
	return s.sessions.List(ctx, f)
}

func (s *Service) ListCanonicalKeys(ctx context.Context, category string) ([]hom.CanonicalKey, error) {
	return s.canonical.List(ctx, category)
}
