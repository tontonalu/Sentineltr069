package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	homapp "github.com/celinet/sentinel-acs/internal/application/homologation"
	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	inv "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
	mw "github.com/celinet/sentinel-acs/internal/transport/http/middleware"
	hompages "github.com/celinet/sentinel-acs/internal/views/pages/homologation"
)

// HomologationHandler — UI + endpoints do wizard.
type HomologationHandler struct {
	Service  *homapp.Service
	Devices  inv.DeviceRepository
	Models   inv.DeviceModelRepository
	Vendors  inv.VendorRepository
	HomModel hom.ModelHomologationRepo
	// Templates é opcional: quando setado, alimenta o autocomplete de nome
	// de profile no painel de finalização (próxima versão + nomes existentes).
	Templates *tplapp.Service
}

// List GET /homologation — sessões ativas + recentes + atalho para nova.
func (h *HomologationHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.Service.ListSessions(r.Context(), hom.SessionFilter{Limit: 50})
	if err != nil {
		logger.FromContext(r.Context()).Error("homologation list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Lista devices marcados como lab para o select de "iniciar sessão".
	allDevs, _, _ := h.Devices.List(r.Context(), inv.DeviceFilter{}, inv.Page{Limit: 500})
	var labs []inv.Device
	for _, d := range allDevs {
		if d.IsHomologationLab {
			labs = append(labs, d)
		}
	}
	csrf := mw.CSRFTokenFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hompages.List(hompages.ListInput{
		Sessions: sessions,
		Labs:     labs,
		CSRF:     csrf,
	}).Render(r.Context(), w)
}

// Create POST /homologation/sessions — inicia nova sessão.
func (h *HomologationHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	deviceID, err := uuid.Parse(r.PostForm.Get("device_id"))
	if err != nil {
		http.Error(w, "device_id inválido", http.StatusBadRequest)
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	var userID *uuid.UUID
	if user != nil {
		userID = &user.ID
	}
	sess, err := h.Service.StartSession(r.Context(), deviceID, userID)
	if err != nil {
		logger.FromContext(r.Context()).Error("start session", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+sess.ID.String(), http.StatusSeeOther)
}

// Wizard GET /homologation/sessions/{id} — painel principal do wizard.
func (h *HomologationHandler) Wizard(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	sess, err := h.Service.GetSession(r.Context(), id)
	if errors.Is(err, hom.ErrSessionNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.FromContext(r.Context()).Error("wizard load", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	in := hompages.WizardInput{
		Session: *sess,
		CSRF:    mw.CSRFTokenFromContext(r.Context()),
	}
	if dev, err := h.Devices.GetByID(r.Context(), sess.LabDeviceID); err == nil {
		in.Device = dev
	}
	if model, err := h.Models.GetByID(r.Context(), sess.ModelID); err == nil {
		in.Model = model
		if v, err := h.Vendors.GetByID(r.Context(), model.VendorID); err == nil {
			in.Vendor = v
		}
	}
	keys, _ := h.Service.ListCanonicalKeys(r.Context(), "")
	in.CanonicalKeys = keys

	// Indexa canonical_keys já mapeadas nesta sessão — UI desabilita opções
	// duplicadas no <select> inline da árvore.
	in.MappedKeys = make(map[string]bool, len(sess.Mappings))
	for _, m := range sess.Mappings {
		in.MappedKeys[m.CanonicalKey] = true
	}

	// Tree opcional — só renderiza se há snapshot.
	if len(sess.TreeSnapshot) > 0 {
		prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
		search := strings.TrimSpace(r.URL.Query().Get("q"))
		entries, err := h.Service.BrowseTree(r.Context(), id, prefix, search)
		if err == nil {
			in.Tree = entries
			in.TreePrefix = prefix
			in.TreeSearch = search
		}
		// Sugere canonical_key por linha. Pass 1 (exact): bate hints do
		// catálogo direto contra o path. Pass 2 (fuzzy): para paths não-WiFi
		// observados na árvore, normaliza números de instância e tenta de
		// novo — cobre WANConnectionDevice.{1,2,3} variando entre vendors.
		// Passamos os paths visíveis na árvore filtrada; é suficiente para
		// pré-selecionar nas linhas que o operador está vendo.
		observedPaths := make([]string, 0, len(in.Tree))
		for _, e := range in.Tree {
			observedPaths = append(observedPaths, e.Path)
		}
		in.SuggestedKeyByPath = buildSuggestedKeyByPath(keys, in.Model, observedPaths)

		// Diagnóstico: contagem total de leaves + faults do NBI. Quando o
		// CPE responde minimamente, mostra um aviso "árvore rasa" no painel
		// de diagnóstico para o operador entender o que aconteceu.
		if stats, err := h.Service.SnapshotStats(r.Context(), id); err == nil {
			in.Stats = stats
		}
	}

	// Sugestão de nome do profile: próxima versão + nomes já usados pra este
	// modelo (alimenta datalist de autocomplete no finalize panel). Só faz
	// sentido quando temos templates service E vendor/model resolvidos.
	if h.Templates != nil && in.Vendor != nil && in.Model != nil {
		modelID := in.Model.ID
		vendorName := in.Vendor.Name
		modelName := in.Model.Model
		if sg, err := h.Templates.SuggestProfileName(r.Context(), vendorName, modelName, &modelID); err == nil {
			in.ProfileSuggestion = sg
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = hompages.Wizard(in).Render(r.Context(), w)
}

// Probe POST /homologation/sessions/{id}/probe — dispara sondagem assíncrona.
//
// Pass 1 + Pass 2 levam até ~4 minutos em ONTs minimalistas (Vsol, FiberHome)
// porque Pass 2 emite ~80 tasks getParameterValues via TR-069 — bloquear o
// HTTP request por isso falha em proxies/timeouts e dá péssima UX.
//
// Fluxo assíncrono:
//  1. MarkProbing (sync, <50ms): seta status=probing no DB.
//  2. Goroutine roda Probe completo com context.Background — não morre se o
//     operador fechar a página. Logger é propagado via NewContext.
//  3. Redirect imediato — o wizard renderiza estado "probing" com auto-refresh
//     a cada 8s. Quando o snapshot fica pronto, status vira "testing" e a
//     árvore aparece no próximo refresh.
func (h *HomologationHandler) Probe(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	if err := h.Service.MarkProbing(r.Context(), id); err != nil {
		logger.FromContext(r.Context()).Error("mark probing", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	// Logger sobrevive à goroutine; ctx do request seria cancelado quando o
	// redirect retorna e o request fecha — usamos Background para não matar
	// o probe no meio. Por isso suprimimos o gosec G118 (CWE-400): a
	// detachment é intencional e bounded por context.WithTimeout(5min).
	parentLog := logger.FromContext(r.Context())
	// #nosec G118 -- async probe deve sobreviver ao request HTTP que o disparou; bound por timeout abaixo
	go func() {
		bgCtx, cancel := context.WithTimeout(
			logger.WithLogger(context.Background(), parentLog),
			5*time.Minute,
		)
		defer cancel()
		if _, err := h.Service.Probe(bgCtx, id); err != nil {
			parentLog.Error("async probe failed", "session", id, "err", err)
		}
	}()
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// AddMapping POST /homologation/sessions/{id}/mappings — adiciona path mapeado.
func (h *HomologationHandler) AddMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	dt := tmpl.DataType(r.PostForm.Get("data_type"))
	if !dt.Valid() {
		dt = tmpl.DataTypeString
	}
	// canonical_key: input livre com datalist (operador pode escolher do catálogo
	// ou digitar valor novo, apenas um campo).
	_, err := h.Service.AddMapping(r.Context(), homapp.AddMappingInput{
		SessionID:     id,
		CanonicalKey:  r.PostForm.Get("canonical_key"),
		TRPath:        r.PostForm.Get("tr_path"),
		ValueTemplate: r.PostForm.Get("value_template"),
		DataType:      dt,
		IsSecret:      r.PostForm.Get("is_secret") == "1",
	})
	if err != nil {
		logger.FromContext(r.Context()).Error("add mapping", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// RemoveMapping POST /homologation/sessions/{id}/mappings/{mid}/delete
func (h *HomologationHandler) RemoveMapping(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "mid"))
	if err != nil {
		http.Error(w, "mid inválido", http.StatusBadRequest)
		return
	}
	if err := h.Service.RemoveMapping(r.Context(), id, mid); err != nil {
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// UpdateMappingTemplate POST /homologation/sessions/{id}/mappings/{mid}/template
// Permite editar o value_template do mapping sem recriar — útil quando
// operador quer trocar `{{ customer.pppoe_login }}_2G` por outra expressão.
func (h *HomologationHandler) UpdateMappingTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "mid"))
	if err != nil {
		http.Error(w, "mid inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	tpl := strings.TrimSpace(r.PostForm.Get("value_template"))
	if err := h.Service.UpdateMappingTemplate(r.Context(), id, mid, tpl); err != nil {
		logger.FromContext(r.Context()).Error("update template", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// TestRead POST /homologation/sessions/{id}/mappings/{mid}/test-read
func (h *HomologationHandler) TestRead(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "mid"))
	if err != nil {
		http.Error(w, "mid inválido", http.StatusBadRequest)
		return
	}
	if _, err := h.Service.RunReadTest(r.Context(), mid); err != nil {
		logger.FromContext(r.Context()).Error("test read", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// TestWrite POST /homologation/sessions/{id}/mappings/{mid}/test-write
func (h *HomologationHandler) TestWrite(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	mid, err := uuid.Parse(chi.URLParam(r, "mid"))
	if err != nil {
		http.Error(w, "mid inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	if _, err := h.Service.RunWriteTest(r.Context(), homapp.RunWriteTestInput{
		MappingID:       mid,
		TestValue:       r.PostForm.Get("test_value"),
		RestoreOriginal: r.PostForm.Get("restore") != "0", // default = restore
	}); err != nil {
		logger.FromContext(r.Context()).Error("test write", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// Complete POST /homologation/sessions/{id}/complete — finaliza sessão.
func (h *HomologationHandler) Complete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	user, _ := mw.UserFromContext(r.Context())
	var userID *uuid.UUID
	if user != nil {
		userID = &user.ID
	}
	prof, err := h.Service.Complete(r.Context(), homapp.CompleteInput{
		SessionID:   id,
		ProfileName: r.PostForm.Get("profile_name"),
		Description: r.PostForm.Get("description"),
		ChangeNote:  r.PostForm.Get("change_note"),
		UserID:      userID,
	})
	if err != nil {
		logger.FromContext(r.Context()).Error("complete session", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/templates/"+prof.ID.String(), http.StatusSeeOther)
}

// AutoMap POST /homologation/sessions/{id}/automap — varre o catálogo de
// canonical_keys e cria mappings para todas que tiveram match na árvore
// sondada (via hint paths). Idempotente: pula chaves já mapeadas.
func (h *HomologationHandler) AutoMap(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	res, err := h.Service.SuggestMappings(r.Context(), id)
	if err != nil {
		logger.FromContext(r.Context()).Error("automap suggest", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.Service.ApplyAutoMap(r.Context(), id, res.Suggestions); err != nil {
		logger.FromContext(r.Context()).Error("automap apply", "err", err)
		http.Error(w, friendlyHomError(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/homologation/sessions/"+id.String(), http.StatusSeeOther)
}

// Deprecate POST /homologation/model-homologations/{id}/deprecate — marca um
// registro de homologação como deprecated. Apply-bulk volta a recusar este
// par (model, profile). Operacional: o registro fica preservado para auditoria.
func (h *HomologationHandler) Deprecate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form inválido", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.PostForm.Get("reason"))
	if h.HomModel == nil {
		http.Error(w, "homologação não disponível", http.StatusServiceUnavailable)
		return
	}
	if err := h.HomModel.Deprecate(r.Context(), id, reason); err != nil {
		if errors.Is(err, hom.ErrModelHomologationNotFound) {
			http.NotFound(w, r)
			return
		}
		logger.FromContext(r.Context()).Error("deprecate", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Volta para o template de origem se referer veio de lá.
	if ref := r.Header.Get("Referer"); ref != "" {
		http.Redirect(w, r, ref, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/homologation", http.StatusSeeOther)
}

// Abandon POST /homologation/sessions/{id}/abandon — desiste da sessão.
func (h *HomologationHandler) Abandon(w http.ResponseWriter, r *http.Request) {
	id, ok := h.parseSessionID(w, r)
	if !ok {
		return
	}
	if err := h.Service.Abandon(r.Context(), id); err != nil {
		http.Error(w, friendlyHomError(err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/homologation", http.StatusSeeOther)
}

// ──────────────── helpers ────────────────

// buildSuggestedKeyByPath inverte o catálogo (canonical_key → hint paths)
// numa indexação path → canonical_key, para que o dropdown inline da árvore
// consiga pré-selecionar a chave certa em O(1) por linha.
//
// Duas passadas:
//
//  1. Exact match: cada hint path do catálogo vira uma entrada direta. É a
//     forma mais segura — diferencia Wi-Fi 2.4 (.WLANConfiguration.1.SSID)
//     de Wi-Fi 5GHz (.WLANConfiguration.5.SSID) sem ambiguidade.
//
//  2. Fuzzy match (instance-agnostic): para cada path observado na árvore
//     que não bateu exact, normalizamos os números de instância (`.1.` →
//     `.*.`) e procuramos um hint com mesma forma normalizada. Cobre o
//     caso clássico de WANConnectionDevice.{1,2,3} variando entre vendors
//     (Vsol/ZTE/Huawei). Paths Wi-Fi são EXCLUÍDOS desta passada porque o
//     número da instância carrega a banda (perderia a distinção 2.4/5).
//
// Em caso de colisão (dois canonical_keys reivindicando o mesmo path), o
// primeiro vence — o seed do catálogo é desenhado para evitar isso, mas a
// heurística não trava.
func buildSuggestedKeyByPath(keys []hom.CanonicalKey, model *inv.DeviceModel, observedPaths []string) map[string]string {
	out := make(map[string]string, len(keys))

	// Pass 1 — exact match.
	for _, k := range keys {
		for _, h := range pickHintsForModel(k, model) {
			if h == "" {
				continue
			}
			if _, exists := out[h]; exists {
				continue
			}
			out[h] = k.Key
		}
	}

	if len(observedPaths) == 0 {
		return out
	}

	// Pass 2 — fuzzy: normalized hint index (skip Wi-Fi).
	normIndex := make(map[string]string, len(keys))
	for _, k := range keys {
		for _, h := range pickHintsForModel(k, model) {
			if h == "" || isWiFiPath(h) {
				continue
			}
			nh := normalizeNumericSegments(h)
			if _, exists := normIndex[nh]; exists {
				continue
			}
			normIndex[nh] = k.Key
		}
	}
	for _, p := range observedPaths {
		if _, exists := out[p]; exists {
			continue
		}
		if isWiFiPath(p) {
			continue
		}
		np := normalizeNumericSegments(p)
		if k, ok := normIndex[np]; ok {
			out[p] = k
		}
	}
	return out
}

// normalizeNumericSegments substitui cada segmento puramente numérico por
// "*", produzindo uma forma canônica que ignora número de instância.
//
// Ex.: "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.Username"
//   → "InternetGatewayDevice.WANDevice.*.WANConnectionDevice.*.WANPPPConnection.*.Username"
func normalizeNumericSegments(p string) string {
	parts := strings.Split(p, ".")
	for i, s := range parts {
		if isAllDigits(s) {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, ".")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isWiFiPath identifica paths cujo número de instância carrega informação
// semântica (banda/AP) e portanto NÃO podem entrar na fuzzy match.
func isWiFiPath(p string) bool {
	return strings.Contains(p, "WLANConfiguration") ||
		strings.Contains(p, "WiFi.Radio") ||
		strings.Contains(p, "WiFi.SSID") ||
		strings.Contains(p, "WiFi.AccessPoint")
}

// pickHintsForModel devolve SEMPRE os hints dos dois data models (TR-098 +
// TR-181). O exact match contra a árvore real garante que só hints válidos
// pra aquele CPE produzam sugestão — tentar ambos custa O(N) extra mas
// resolve dois cenários comuns:
//  1) Cadastro do modelo marcou o data model errado (operador colocou TR-181
//     num CPE que reporta InternetGatewayDevice.*).
//  2) CPE genuinamente híbrido (raro mas existe — alguns Huawei expõem TR-098
//     por compat e TR-181 simultaneamente).
//
// model é mantido na assinatura para facilitar futura priorização (ex.: dar
// peso menor ao data model "errado" caso queiramos rankear sugestões).
func pickHintsForModel(k hom.CanonicalKey, model *inv.DeviceModel) []string {
	_ = model
	out := make([]string, 0, len(k.HintPathsTR098)+len(k.HintPathsTR181))
	out = append(out, k.HintPathsTR098...)
	out = append(out, k.HintPathsTR181...)
	return out
}

func (h *HomologationHandler) parseSessionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "id inválido", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

// friendlyHomError converte erros de domínio em mensagens em português para
// resposta HTTP. Mensagens internas (db, NBI) ficam genéricas para evitar leak.
func friendlyHomError(err error) string {
	switch {
	case errors.Is(err, hom.ErrSessionNotFound):
		return "Sessão não encontrada."
	case errors.Is(err, hom.ErrSessionAlreadyActive):
		return "Já existe uma sessão ativa para este device."
	case errors.Is(err, hom.ErrSessionNotActive):
		return "Sessão não está em estado editável."
	case errors.Is(err, hom.ErrSessionMissingModel):
		return "O device de lab precisa ter um modelo (device_model) atribuído."
	case errors.Is(err, hom.ErrSessionMissingTree):
		return "Sonde a árvore TR-069 antes (botão Sondar)."
	case errors.Is(err, hom.ErrDeviceNotLab):
		return "Device não está marcado como laboratório de homologação."
	case errors.Is(err, hom.ErrMappingDuplicate):
		return "Esta canonical_key já foi mapeada nesta sessão."
	case errors.Is(err, hom.ErrMappingNotFound):
		return "Mapping não encontrado."
	case errors.Is(err, hom.ErrNoEligibleMappings):
		return "Nenhum mapping passou nos testes — não há o que homologar."
	default:
		return err.Error()
	}
}
