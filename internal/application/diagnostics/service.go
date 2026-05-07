// Package diagnostics orquestra os testes remotos disparáveis pela UI:
// IPPing e TraceRoute via TR-069 padrão (RPC do CWMP).
//
// Fluxo conceitual:
//
//	UI submete form → Service.EnqueuePing
//	    → identifica data model (TR-098 ou TR-181)
//	    → SetParameterValues com [Host, NumberOfRepetitions, ..., DiagnosticsState="Requested"]
//	    → INSERT na tabela diagnostics com status=running, deadline=now+timeout
//
//	Worker tick (a cada N segundos) → Service.PollOne
//	    → lê tree do CPE
//	    → if DiagnosticsState=Complete → extrai SuccessCount/AvgResponseTime/...
//	    → if DiagnosticsState=Error_* → status=error, error=mensagem
//	    → if now > Deadline → status=timeout
//	    → else: continua aguardando
//
// Decisões deliberadas:
//
//   - Sem fila Redis pra diagnostics. Volume baixo (operador dispara um por vez)
//     e a tabela já é a fila — o poller varre `WHERE status IN (...)` direto.
//
//   - GenieACS GetParameterValues é assíncrono no NBI; chamamos `Refresh` na
//     subárvore IPPingDiagnostics antes do polling pra forçar a leitura
//     fresca. Cache de 30s do client poderia mascarar o resultado novo.
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	diag "github.com/celinet/sentinel-acs/internal/domain/diagnostics"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// DeviceResolver — adapter mínimo entre a Service e o repo de devices.
// Service só precisa do genieacs_id pra falar com o NBI.
type DeviceResolver interface {
	ResolveGenieACSID(ctx context.Context, internalID uuid.UUID) (string, error)
}

// GenieClient — subset do genieacs.Client que o serviço usa. Mantém o
// acoplamento solto pra facilitar mocks em teste.
type GenieClient interface {
	GetDevice(ctx context.Context, deviceID string) (*genieacs.Device, error)
	SetParameterValues(ctx context.Context, deviceID string, params []genieacs.Parameter) (genieacs.TaskID, error)
	Refresh(ctx context.Context, deviceID, objectName string) (genieacs.TaskID, error)
}

type Service struct {
	repo    diag.Repository
	devices DeviceResolver
	genie   GenieClient
}

func NewService(repo diag.Repository, devices DeviceResolver, genie GenieClient) *Service {
	return &Service{repo: repo, devices: devices, genie: genie}
}

// ──────────────── enqueue ────────────────

// PingDefaults preenche valores razoáveis quando o operador deixou em branco.
func PingDefaults(req diag.PingRequest) diag.PingRequest {
	if req.Count <= 0 {
		req.Count = 4
	}
	if req.Count > 100 {
		req.Count = 100
	}
	if req.SizeBytes <= 0 {
		req.SizeBytes = 64
	}
	if req.SizeBytes > 1500 {
		req.SizeBytes = 1500
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = 5000
	}
	return req
}

func TracerouteDefaults(req diag.TracerouteRequest) diag.TracerouteRequest {
	if req.MaxHops <= 0 {
		req.MaxHops = 30
	}
	if req.MaxHops > 64 {
		req.MaxHops = 64
	}
	if req.SizeBytes <= 0 {
		req.SizeBytes = 64
	}
	if req.TimeoutMS <= 0 {
		req.TimeoutMS = 5000
	}
	return req
}

// EnqueuePing dispara um IPPingDiagnostics no CPE.
//
// Erros possíveis:
//   - Host vazio → ErrInvalidRequest
//   - Device não tem genieacs_id → propagado do resolver
//   - Data model não identificado → ErrUnsupportedDataModel
//   - GenieACS rejeitou setParameterValues → propagado (status=error)
func (s *Service) EnqueuePing(ctx context.Context, deviceID uuid.UUID, req diag.PingRequest, actorID *uuid.UUID) (*diag.Diagnostic, error) {
	req = PingDefaults(req)
	if strings.TrimSpace(req.Host) == "" {
		return nil, ErrInvalidRequest
	}
	genieID, err := s.devices.ResolveGenieACSID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	dm, err := s.detectDataModel(ctx, genieID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// Deadline: 3× o timeout total esperado + margem. Total esperado =
	// count * (timeoutMS) — mas o CPE também leva tempo pra rodar entre
	// pings; multiplicador 3× cobre overhead.
	deadline := now.Add(time.Duration(req.Count*req.TimeoutMS*3)*time.Millisecond + 30*time.Second)
	if deadline.Sub(now) < 30*time.Second {
		deadline = now.Add(30 * time.Second)
	}

	d := &diag.Diagnostic{
		DeviceID:    deviceID,
		Type:        diag.TypePing,
		Status:      diag.StatusRequested,
		Request: map[string]any{
			"host":         req.Host,
			"count":        req.Count,
			"size_bytes":   req.SizeBytes,
			"timeout_ms":   req.TimeoutMS,
			"interface":    req.Interface,
			"data_model":   dm,
		},
		RequestedBy: actorID,
		RequestedAt: now,
		Deadline:    deadline,
	}
	if err := s.repo.Create(ctx, d); err != nil {
		return nil, err
	}

	// Dispara no CPE — se falhar, marcamos error e devolvemos.
	params := pingParams(dm, req)
	if _, err := s.genie.SetParameterValues(ctx, genieID, params); err != nil {
		_ = s.repo.UpdateStatus(ctx, d.ID, diag.StatusError,
			"falha ao enfileirar setParameterValues: "+err.Error())
		d.Status = diag.StatusError
		d.Error = err.Error()
		return d, nil
	}
	if err := s.repo.UpdateStatus(ctx, d.ID, diag.StatusRunning, ""); err != nil {
		return d, err
	}
	d.Status = diag.StatusRunning
	return d, nil
}

func (s *Service) EnqueueTraceroute(ctx context.Context, deviceID uuid.UUID, req diag.TracerouteRequest, actorID *uuid.UUID) (*diag.Diagnostic, error) {
	req = TracerouteDefaults(req)
	if strings.TrimSpace(req.Host) == "" {
		return nil, ErrInvalidRequest
	}
	genieID, err := s.devices.ResolveGenieACSID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	dm, err := s.detectDataModel(ctx, genieID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	// Traceroute pode levar até maxHops × timeout + overhead.
	deadline := now.Add(time.Duration(req.MaxHops*req.TimeoutMS*2)*time.Millisecond + 60*time.Second)

	d := &diag.Diagnostic{
		DeviceID: deviceID,
		Type:     diag.TypeTraceroute,
		Status:   diag.StatusRequested,
		Request: map[string]any{
			"host":       req.Host,
			"max_hops":   req.MaxHops,
			"size_bytes": req.SizeBytes,
			"timeout_ms": req.TimeoutMS,
			"interface":  req.Interface,
			"data_model": dm,
		},
		RequestedBy: actorID,
		RequestedAt: now,
		Deadline:    deadline,
	}
	if err := s.repo.Create(ctx, d); err != nil {
		return nil, err
	}

	params := tracerouteParams(dm, req)
	if _, err := s.genie.SetParameterValues(ctx, genieID, params); err != nil {
		_ = s.repo.UpdateStatus(ctx, d.ID, diag.StatusError,
			"falha ao enfileirar setParameterValues: "+err.Error())
		d.Status = diag.StatusError
		d.Error = err.Error()
		return d, nil
	}
	if err := s.repo.UpdateStatus(ctx, d.ID, diag.StatusRunning, ""); err != nil {
		return d, err
	}
	d.Status = diag.StatusRunning
	return d, nil
}

// ──────────────── poll ────────────────

// PollAll varre ListActive e atualiza cada um. Volta a quantidade processada.
// Erros individuais não interrompem a varredura — logam e seguem.
type PollResult struct {
	Processed int
	Completed int
	TimedOut  int
	Errors    int
}

func (s *Service) PollAll(ctx context.Context) (PollResult, error) {
	var res PollResult
	active, err := s.repo.ListActive(ctx, 50)
	if err != nil {
		return res, err
	}
	for _, d := range active {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		state, perr := s.PollOne(ctx, d)
		res.Processed++
		switch state {
		case diag.StatusComplete:
			res.Completed++
		case diag.StatusTimeout:
			res.TimedOut++
		case diag.StatusError:
			res.Errors++
		}
		if perr != nil && !errors.Is(perr, context.Canceled) {
			res.Errors++
		}
	}
	return res, nil
}

// PollOne avança o estado de UM diagnostic. Devolve o estado pós-tick
// pra o caller logar/contar; erro só é propagado quando o diagnostic não
// pôde ser atualizado no banco — falha de NBI vira status=error.
func (s *Service) PollOne(ctx context.Context, d diag.Diagnostic) (diag.Status, error) {
	if d.Status.Terminal() {
		return d.Status, nil
	}
	if time.Now().UTC().After(d.Deadline) {
		_ = s.repo.UpdateStatus(ctx, d.ID, diag.StatusTimeout,
			"deadline expirou — CPE não devolveu DiagnosticsState=Complete")
		return diag.StatusTimeout, nil
	}
	genieID, err := s.devices.ResolveGenieACSID(ctx, d.DeviceID)
	if err != nil {
		return d.Status, err
	}
	// Refresca a sub-árvore relevante. Se falhar, segue (cache pode estar
	// fresca o suficiente). Refresh tem custo no GenieACS então pulamos
	// nos primeiros 5s — o CPE provavelmente nem começou.
	if time.Since(d.RequestedAt) > 5*time.Second {
		base := diagnosticsBase(string(d.Type), dataModelFromRequest(d))
		if base != "" {
			_, _ = s.genie.Refresh(ctx, genieID, base)
		}
	}

	dev, err := s.genie.GetDevice(ctx, genieID)
	if err != nil {
		return d.Status, err
	}
	switch d.Type {
	case diag.TypePing:
		return s.completePing(ctx, d, dev.Raw)
	case diag.TypeTraceroute:
		return s.completeTraceroute(ctx, d, dev.Raw)
	}
	return d.Status, nil
}

func (s *Service) completePing(ctx context.Context, d diag.Diagnostic, raw map[string]any) (diag.Status, error) {
	dm := dataModelFromRequest(d)
	state := genieacs.FirstNonEmpty(raw, paths(dm, "ping", "DiagnosticsState")...)
	switch {
	case state == "" || state == "None" || state == "Requested":
		return d.Status, nil
	case strings.HasPrefix(state, "Error"):
		_ = s.repo.UpdateStatus(ctx, d.ID, diag.StatusError, "CPE devolveu "+state)
		return diag.StatusError, nil
	case state == "Complete":
		result := map[string]any{
			"success_count":    parseInt(raw, paths(dm, "ping", "SuccessCount")...),
			"failure_count":    parseInt(raw, paths(dm, "ping", "FailureCount")...),
			"avg_response_ms":  parseInt(raw, paths(dm, "ping", "AverageResponseTime")...),
			"min_response_ms":  parseInt(raw, paths(dm, "ping", "MinimumResponseTime")...),
			"max_response_ms":  parseInt(raw, paths(dm, "ping", "MaximumResponseTime")...),
		}
		if err := s.repo.UpdateResult(ctx, d.ID, diag.StatusComplete, result); err != nil {
			return d.Status, err
		}
		return diag.StatusComplete, nil
	}
	return d.Status, nil
}

func (s *Service) completeTraceroute(ctx context.Context, d diag.Diagnostic, raw map[string]any) (diag.Status, error) {
	dm := dataModelFromRequest(d)
	state := genieacs.FirstNonEmpty(raw, paths(dm, "traceroute", "DiagnosticsState")...)
	switch {
	case state == "" || state == "None" || state == "Requested":
		return d.Status, nil
	case strings.HasPrefix(state, "Error"):
		_ = s.repo.UpdateStatus(ctx, d.ID, diag.StatusError, "CPE devolveu "+state)
		return diag.StatusError, nil
	case state == "Complete":
		hops := extractHops(dm, raw)
		result := map[string]any{
			"response_time_ms": parseInt(raw, paths(dm, "traceroute", "ResponseTime")...),
			"hops":             hops,
		}
		if err := s.repo.UpdateResult(ctx, d.ID, diag.StatusComplete, result); err != nil {
			return d.Status, err
		}
		return diag.StatusComplete, nil
	}
	return d.Status, nil
}

// ──────────────── path resolution ────────────────

// detectDataModel inspeciona o tree do device e devolve "TR-098" ou "TR-181".
// Cobre ambos os modelos suportados — a maioria dos vendors brasileiros usa
// TR-098 (InternetGatewayDevice), mas modelos novos (FiberHome, alguns Huawei)
// já vêm em TR-181 (Device).
const (
	DataModelTR098 = "TR-098"
	DataModelTR181 = "TR-181"
)

func (s *Service) detectDataModel(ctx context.Context, genieID string) (string, error) {
	dev, err := s.genie.GetDevice(ctx, genieID)
	if err != nil {
		return "", fmt.Errorf("detect data model: %w", err)
	}
	if _, ok := dev.Raw["InternetGatewayDevice"]; ok {
		return DataModelTR098, nil
	}
	if _, ok := dev.Raw["Device"]; ok {
		return DataModelTR181, nil
	}
	return "", ErrUnsupportedDataModel
}

func dataModelFromRequest(d diag.Diagnostic) string {
	if d.Request == nil {
		return DataModelTR098
	}
	if v, ok := d.Request["data_model"].(string); ok && v != "" {
		return v
	}
	return DataModelTR098
}

// diagnosticsBase devolve a sub-árvore relevante pra Refresh.
func diagnosticsBase(typeStr, dm string) string {
	switch typeStr {
	case string(diag.TypePing):
		if dm == DataModelTR181 {
			return "Device.IP.Diagnostics.IPPing"
		}
		return "InternetGatewayDevice.IPPingDiagnostics"
	case string(diag.TypeTraceroute):
		if dm == DataModelTR181 {
			return "Device.IP.Diagnostics.TraceRoute"
		}
		return "InternetGatewayDevice.TraceRouteDiagnostics"
	}
	return ""
}

// paths devolve [TR-098, TR-181] pro field — útil para FirstNonEmpty quando
// o caller não tem certeza qual modelo o CPE usa (no PollOne já decidimos
// pelo data_model salvo no Request, mas mantemos a lista por defesa).
func paths(dm, kind, field string) []string {
	tr98, tr181 := pathFor(kind, field)
	if dm == DataModelTR181 {
		return []string{tr181, tr98}
	}
	return []string{tr98, tr181}
}

func pathFor(kind, field string) (tr98, tr181 string) {
	switch kind {
	case "ping":
		return "InternetGatewayDevice.IPPingDiagnostics." + field,
			"Device.IP.Diagnostics.IPPing." + field
	case "traceroute":
		return "InternetGatewayDevice.TraceRouteDiagnostics." + field,
			"Device.IP.Diagnostics.TraceRoute." + field
	}
	return "", ""
}

// ──────────────── set-params builders ────────────────

func pingParams(dm string, req diag.PingRequest) []genieacs.Parameter {
	out := make([]genieacs.Parameter, 0, 6)
	add := func(field string, value any, ttype string) {
		tr98, tr181 := pathFor("ping", field)
		path := tr98
		if dm == DataModelTR181 {
			path = tr181
		}
		out = append(out, genieacs.Parameter{Path: path, Value: value, Type: ttype})
	}
	add("Host", req.Host, "xsd:string")
	add("NumberOfRepetitions", req.Count, "xsd:unsignedInt")
	add("DataBlockSize", req.SizeBytes, "xsd:unsignedInt")
	add("Timeout", req.TimeoutMS, "xsd:unsignedInt")
	if req.Interface != "" {
		add("Interface", req.Interface, "xsd:string")
	}
	// "Requested" é o trigger: o CPE inicia o teste assim que vê esse estado.
	add("DiagnosticsState", "Requested", "xsd:string")
	return out
}

func tracerouteParams(dm string, req diag.TracerouteRequest) []genieacs.Parameter {
	out := make([]genieacs.Parameter, 0, 6)
	add := func(field string, value any, ttype string) {
		tr98, tr181 := pathFor("traceroute", field)
		path := tr98
		if dm == DataModelTR181 {
			path = tr181
		}
		out = append(out, genieacs.Parameter{Path: path, Value: value, Type: ttype})
	}
	add("Host", req.Host, "xsd:string")
	add("MaxHopCount", req.MaxHops, "xsd:unsignedInt")
	add("DataBlockSize", req.SizeBytes, "xsd:unsignedInt")
	add("Timeout", req.TimeoutMS, "xsd:unsignedInt")
	if req.Interface != "" {
		add("Interface", req.Interface, "xsd:string")
	}
	add("DiagnosticsState", "Requested", "xsd:string")
	return out
}

// ──────────────── result extraction ────────────────

func extractHops(dm string, raw map[string]any) []map[string]any {
	base := "InternetGatewayDevice.TraceRouteDiagnostics.RouteHops"
	if dm == DataModelTR181 {
		base = "Device.IP.Diagnostics.TraceRoute.RouteHops"
	}
	obj := genieacs.ParamObject(raw, base)
	if obj == nil {
		return nil
	}
	out := make([]map[string]any, 0, 16)
	for i := 1; i <= 64; i++ {
		idx := strconv.Itoa(i)
		hop, ok := obj[idx].(map[string]any)
		if !ok {
			continue
		}
		// As chaves variam por vendor — `HopHost` e `HopHostAddress` são as
		// canônicas, alguns expõem só `Host`/`HostAddress`.
		host := genieacs.ParamString(hop, "HopHost")
		if host == "" {
			host = genieacs.ParamString(hop, "Host")
		}
		ip := genieacs.ParamString(hop, "HopHostAddress")
		if ip == "" {
			ip = genieacs.ParamString(hop, "HostAddress")
		}
		rtt := genieacs.ParamString(hop, "HopRTTimes")
		if rtt == "" {
			rtt = genieacs.ParamString(hop, "RTTimes")
		}
		out = append(out, map[string]any{
			"hop":  i,
			"host": host,
			"ip":   ip,
			"rtt":  rtt,
		})
	}
	return out
}

// parseInt — devolve 0 quando o campo está vazio ou inválido. Diagnostic
// resultados raramente são negativos; 0 representa "ausente" sem complicar
// o JSON com null mixado.
func parseInt(raw map[string]any, paths ...string) int {
	v := genieacs.FirstNonEmpty(raw, paths...)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}
