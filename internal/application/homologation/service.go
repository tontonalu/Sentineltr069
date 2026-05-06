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
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// GenieACSPort é a fatia mínima do cliente GenieACS que o service consome.
// Interface (em vez de *genieacs.Client direto) facilita fakes em testes.
type GenieACSPort interface {
	GetDevice(ctx context.Context, deviceID string) (*genieacs.Device, error)
	Refresh(ctx context.Context, deviceID, objectName string) (genieacs.TaskID, error)
	GetParameterValues(ctx context.Context, deviceID string, paths []string) (genieacs.TaskID, error)
	SetParameterValues(ctx context.Context, deviceID string, params []genieacs.Parameter) (genieacs.TaskID, error)
	// GetTask devolve uma task ainda na fila do NBI. Quando a task completa,
	// o NBI a remove e o client retorna ErrTaskNotFound — usamos isso como
	// sinal de "done" no Probe largo.
	GetTask(ctx context.Context, taskID string) (*genieacs.Task, error)
	// GetFaults lista falhas que o NBI registrou para o device — útil pra
	// diagnosticar quando refreshObject/getParameterValues retornam vazio.
	GetFaults(ctx context.Context, deviceID string) ([]genieacs.Fault, error)
}

// probeWaitTimeoutPass1 é o teto do refresh largo: refreshObject por ramo
// top-level. Em CPEs cooperativos isso já basta; em ONTs minimalistas (Vsol,
// FiberHome) os tasks faultam rápido e a Pass 2 supre via getParameterValues
// direto. Como o Probe roda em goroutine no handler, o timeout só limita
// a espera do snapshot — não bloqueia o operador.
const probeWaitTimeoutPass1 = 60 * time.Second

// probeWaitTimeoutPass2 é o teto da Pass 2 (targeted fetch). Cada hint vira
// um task individual com connection_request, então o tempo total varia com
// o número de hints e a latência do CPE. 180s cobre o catálogo completo
// (~80 hints × 2s típico) com folga.
const probeWaitTimeoutPass2 = 180 * time.Second

// probeBranches são os ramos top-level que o Probe pede refresh. Cobrimos
// TR-098 e TR-181 simultaneamente — CPEs respondem só os que conhecem;
// caminhos inválidos viram task fault no NBI sem afetar os demais.
var probeBranches = []string{
	// TR-098
	"InternetGatewayDevice.DeviceInfo",
	"InternetGatewayDevice.ManagementServer",
	"InternetGatewayDevice.Time",
	"InternetGatewayDevice.WANDevice",
	"InternetGatewayDevice.LANDevice",
	"InternetGatewayDevice.Services",

	// TR-181
	"Device.DeviceInfo",
	"Device.ManagementServer",
	"Device.Time",
	"Device.WiFi",
	"Device.IP",
	"Device.PPP",
	"Device.DHCPv4",
	"Device.Ethernet",
	"Device.Services",
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

// MarkProbing transiciona a sessão para `probing` em uma operação síncrona
// e curta — só mexe no DB. Usado pelo handler HTTP antes de disparar o Probe
// real numa goroutine: dessa forma a UI já reflete o estado "sondando" no
// próximo render, mesmo que a goroutine ainda não tenha começado.
//
// Idempotente: chamar duas vezes não erra.
//
// Erros de validação propagados:
//   - ErrSessionNotActive  — sessão completed/abandoned
//   - ErrDeviceNotLab      — device não está marcado como lab
//   - ErrSessionMissingTree (não emitido aqui, só pelo Probe real)
func (s *Service) MarkProbing(ctx context.Context, sessionID uuid.UUID) error {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.Status == hom.SessionCompleted || sess.Status == hom.SessionAbandoned {
		return hom.ErrSessionNotActive
	}
	if sess.Status == hom.SessionProbing {
		return nil
	}
	return s.sessions.UpdateStatus(ctx, sessionID, hom.SessionProbing)
}


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

	log := logger.FromContext(ctx)

	// Pass 1 — refresh largo: emite refreshObject para os ramos top-level
	// dos dois data models. Em CPEs cooperativos traz a árvore inteira em
	// 5-15s. Em ONTs minimalistas (Vsol, FiberHome) muitas tasks faultam,
	// daí a necessidade da Pass 2 abaixo.
	refreshIDs := s.issueWideRefresh(ctx, d.GenieACSID)
	log.Info("homologation probe: refresh tasks issued",
		"device", d.GenieACSID, "count", len(refreshIDs))
	s.waitForTasks(ctx, refreshIDs, probeWaitTimeoutPass1)

	// Pass 2 — targeted fetch: getParameterValues por hint path do catálogo,
	// 1 task por path (Fault 9005 isolado por task). Demora minutos pra
	// ~80 paths, mas como o Probe roda em goroutine via handler, o operador
	// não fica bloqueado. Resultado: árvore completa (800+ entradas em ONT
	// típico Vsol) com todas as canonical_keys pré-mapeáveis.
	fetchIDs := s.issueTargetedFetches(ctx, d.GenieACSID)
	log.Info("homologation probe: targeted fetch tasks issued",
		"device", d.GenieACSID, "count", len(fetchIDs))
	s.waitForTasks(ctx, fetchIDs, probeWaitTimeoutPass2)

	// Faults do NBI ajudam a diagnosticar árvore rasa: cada fault é uma
	// RPC que o CPE rejeitou (path inexistente, timeout). Logamos pra
	// debug — também aparecem no painel de diagnóstico do wizard.
	if faults, err := s.genieacs.GetFaults(ctx, d.GenieACSID); err == nil && len(faults) > 0 {
		log.Warn("homologation probe: device has GenieACS faults",
			"device", d.GenieACSID, "fault_count", len(faults))
		for _, f := range faults {
			log.Info("homologation probe fault",
				"code", f.Code, "string", f.String, "path", f.Path)
		}
	}

	// Lê snapshot atual.
	dev, err := s.genieacs.GetDevice(ctx, d.GenieACSID)
	if err != nil {
		return nil, fmt.Errorf("homologation: GetDevice: %w", err)
	}

	// Auto-correção do data model: detecta a partir das chaves de topo da
	// árvore (InternetGatewayDevice → TR-098 / Device → TR-181) e ajusta o
	// cadastro do model se estiver divergente. Sem isso, hints e templates
	// futuros olham a coluna errada e a sugestão automática nada encontra.
	if detected := detectTRDataModel(dev.Raw); detected != "" {
		if model, err := s.models.GetByID(ctx, sess.ModelID); err == nil && model.TRDataModel != detected {
			_ = s.models.SetTRDataModel(ctx, model.ID, detected)
		}
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

// issueTargetedFetches percorre o catálogo de canonical_keys, deduplicando
// hint paths (TR-098 + TR-181), e dispara um getParameterValues por path.
// Cada task isola sua falha — CPEs que retornam Fault 9005 (path inexistente)
// para um path não derrubam os demais.
//
// Demora porque cada task vai com connection_request: 80 hints × 1-3s =
// 80-240s. Como Probe roda em goroutine no handler, o operador não fica
// bloqueado — a UI mostra estado "probing" com auto-refresh.
func (s *Service) issueTargetedFetches(ctx context.Context, deviceID string) []genieacs.TaskID {
	keys, err := s.canonical.List(ctx, "")
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	out := make([]genieacs.TaskID, 0, 128)
	for _, k := range keys {
		hints := append([]string{}, k.HintPathsTR098...)
		hints = append(hints, k.HintPathsTR181...)
		for _, h := range hints {
			h = strings.TrimSpace(h)
			if h == "" || seen[h] {
				continue
			}
			seen[h] = true
			tid, err := s.genieacs.GetParameterValues(ctx, deviceID, []string{h})
			if err != nil || tid == "" {
				continue
			}
			out = append(out, tid)
		}
	}
	return out
}

// issueWideRefresh dispara um refreshObject para cada ramo top-level conhecido
// (TR-098 e TR-181). Erros individuais (ramo inexistente, NBI lento) são
// engolidos: queremos best-effort — o que conseguir, conseguiu. Devolve as
// task IDs que foram aceitas pelo NBI para que o caller possa esperá-las.
func (s *Service) issueWideRefresh(ctx context.Context, deviceID string) []genieacs.TaskID {
	out := make([]genieacs.TaskID, 0, len(probeBranches))
	for _, br := range probeBranches {
		if tid, err := s.genieacs.Refresh(ctx, deviceID, br); err == nil && tid != "" {
			out = append(out, tid)
		}
	}
	return out
}

// waitForTasks bloqueia até todas as tasks completarem ou expirar `timeout`.
// "Completou" = NBI já não devolve a task (ErrTaskNotFound) — é como o
// GenieACS sinaliza done (task removida da fila ativa). Polling em 2s
// equilibra latência percebida vs carga no NBI.
//
// Não devolve erro: probe é best-effort. Se o timeout estourar, segue com a
// árvore parcial — melhor algo do que travar a UI.
func (s *Service) waitForTasks(ctx context.Context, ids []genieacs.TaskID, timeout time.Duration) {
	if len(ids) == 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	pending := make([]genieacs.TaskID, len(ids))
	copy(pending, ids)

	for time.Now().Before(deadline) && len(pending) > 0 {
		remaining := pending[:0]
		for _, id := range pending {
			_, err := s.genieacs.GetTask(ctx, string(id))
			if err == genieacs.ErrTaskNotFound {
				continue // done
			}
			remaining = append(remaining, id)
		}
		pending = remaining
		if len(pending) == 0 {
			return
		}

		// Espera curta entre rodadas. Se ctx cancelar (operador fechou a
		// página), abortamos o wait — probe segue com o que tem.
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// detectTRDataModel inspeciona as chaves de topo do snapshot do GenieACS e
// devolve "tr098", "tr181" ou "" (indeterminado).
//
//   - TR-098 expõe parâmetros sob `InternetGatewayDevice.*`.
//   - TR-181 expõe sob `Device.*`.
//
// CPEs que respondem ambos (raros) seguem o que vier primeiro na ordem
// abaixo. Quando inconcluso, devolvemos "" — caller mantém o cadastro atual
// em vez de chutar.
func detectTRDataModel(raw map[string]any) string {
	if raw == nil {
		return ""
	}
	if _, ok := raw["InternetGatewayDevice"]; ok {
		return inv.TR098
	}
	if _, ok := raw["Device"]; ok {
		return inv.TR181
	}
	return ""
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

// SnapshotStats devolve métricas do snapshot atual da sessão para a UI:
// total de leaves descobertas e faults pendentes no NBI. Útil quando o
// operador precisa entender por que a árvore veio rasa.
type SnapshotStats struct {
	TotalEntries int
	Faults       []genieacs.Fault
}

// SnapshotStats produz métricas do tree_snapshot da sessão. Sem snapshot,
// devolve contadores zerados (não é erro). Faults vêm direto do NBI — se o
// upstream estiver fora, devolvemos só o count e seguimos.
func (s *Service) SnapshotStats(ctx context.Context, sessionID uuid.UUID) (*SnapshotStats, error) {
	sess, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := &SnapshotStats{}
	if len(sess.TreeSnapshot) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(sess.TreeSnapshot, &raw); err == nil {
			out.TotalEntries = len(genieacs.FlattenTree(raw))
		}
	}
	d, err := s.devices.GetByID(ctx, sess.LabDeviceID)
	if err == nil {
		if faults, err := s.genieacs.GetFaults(ctx, d.GenieACSID); err == nil {
			out.Faults = faults
		}
	}
	return out, nil
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
