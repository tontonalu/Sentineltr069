package provisioning

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// SingleFieldRequest descreve uma alteração inline de 1 parâmetro a partir
// da página /devices/{id}. Difere do ApplyToDevice padrão por NÃO depender
// de um Profile pré-existente — o caller (devices.profile_view) já tem
// canonical_key/tr_path/data_type vindos do mapping homologado, e quer
// apenas enfileirar 1 SetParameterValues no worker.
//
// Mantém todo o resto da pipeline igual (Job → Worker → genieacs.Client),
// preservando audit trail, retry, monitoração, etc.
type SingleFieldRequest struct {
	DeviceID     uuid.UUID
	CanonicalKey string
	TRPath       string
	DataType     tmpl.DataType
	RawValue     string // string serializada do form HTML
	IsSecret     bool
	RequestedBy  *uuid.UUID
}

// EnqueueSingleField valida o RawValue contra o DataType, monta um payload
// `PayloadEnvelope` com 1 parâmetro renderizado e cria 1 Job em status queued.
// O worker pega no próximo tick (ou imediatamente, se Notifier estiver setado).
//
// Retorna o Job criado para que a UI possa linkar para /provisioning/jobs/{id}.
func (s *Service) EnqueueSingleField(ctx context.Context, req SingleFieldRequest) (*prov.Job, error) {
	if req.DeviceID == uuid.Nil {
		return nil, fmt.Errorf("provisioning: device_id obrigatório")
	}
	if strings.TrimSpace(req.TRPath) == "" {
		return nil, fmt.Errorf("provisioning: tr_path obrigatório")
	}
	if !req.DataType.Valid() {
		req.DataType = tmpl.DataTypeString
	}

	value, err := coerceValue(req.RawValue, req.DataType)
	if err != nil {
		return nil, fmt.Errorf("provisioning: valor inválido para %s: %w", req.CanonicalKey, err)
	}

	resolved := []tmpl.ResolvedParameter{{
		CanonicalKey: req.CanonicalKey,
		TRPath:       req.TRPath,
		Value:        value,
		DataType:     req.DataType,
		IsSecret:     req.IsSecret,
	}}

	// Sem profile_id porque a edição inline não passa por profile —
	// usamos uuid.Nil + version=0 no envelope só como marcadores. O worker
	// não usa esses campos para executar; apenas o array Parameters.
	payload, err := encodePayload(uuid.Nil, 0, resolved)
	if err != nil {
		return nil, err
	}

	job := &prov.Job{
		DeviceID:    req.DeviceID,
		ProfileID:   nil, // ad-hoc, sem profile
		RequestedBy: req.RequestedBy,
		Status:      prov.JobQueued,
		Payload:     payload,
		ScheduledAt: time.Now(),
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	if s.notify != nil {
		_ = s.notify.Notify(ctx, job.ID) // best-effort
	}
	return job, nil
}

// coerceValue converte a string do form HTML para o tipo Go esperado pelo
// engine de provisioning. Espelha o que o engine.RenderProfile faria após
// rodar Pongo2 — aqui pulamos o template engine porque o usuário já forneceu
// o valor literal, não uma expressão.
func coerceValue(raw string, dt tmpl.DataType) (any, error) {
	s := strings.TrimSpace(raw)
	switch dt {
	case tmpl.DataTypeBool:
		switch strings.ToLower(s) {
		case "true", "1", "on", "yes":
			return true, nil
		case "false", "0", "off", "no", "":
			return false, nil
		}
		return nil, fmt.Errorf("bool inválido: %q", raw)
	case tmpl.DataTypeInt:
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("int inválido: %w", err)
		}
		return v, nil
	case tmpl.DataTypeUnsignedInt:
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unsignedInt inválido: %w", err)
		}
		return v, nil
	case tmpl.DataTypeDateTime:
		// Aceita RFC3339 (ex: 2026-05-07T12:34:56Z); o engine real faz parse
		// próprio quando RenderProfile, mas aqui nem temos template — só
		// validamos que a string parsea para evitar erro tardio no worker.
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return nil, fmt.Errorf("dateTime inválido (use RFC3339): %w", err)
		}
		return s, nil
	default: // string
		return s, nil
	}
}
