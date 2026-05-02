// Package provisioning implementa o caso de uso "aplicar profile a devices".
//
// Fluxo:
//  1. Service.ApplyToDevice resolve params (engine.RenderProfile) e enfileira
//     1 Job em status queued.
//  2. Service.ApplyBulk faz o mesmo para N devices, agrupados por batch_id.
//     Acima de provisioning.ApprovalThreshold, batch entra awaiting_approval.
//  3. Worker (cmd/worker) chama JobRepo.ClaimBatch + executa via genieacs.
package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
)

// Repos & deps

type ProfileLoader interface {
	LoadFull(ctx context.Context, id uuid.UUID) (*tmpl.Profile, error)
}

type DeviceLoader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*inventory.Device, error)
}

type CustomerLoader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*inventory.Customer, error)
}

type POPLoader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*inventory.POP, error)
}

type JobRepository interface {
	Create(ctx context.Context, j *prov.Job) error
}

type BatchRepository interface {
	Create(ctx context.Context, b *prov.Batch) error
}

// Notifier publica eventos no Redis Stream para que o worker pegue
// imediatamente sem esperar tick. Implementação opcional (pode ser nil em
// testes ou modo synchronous).
type Notifier interface {
	Notify(ctx context.Context, jobID uuid.UUID) error
}

// Service — entrypoint da Fase 3 para aplicar profiles. Engine é injetado
// para permitir testes/extensões via filtros.
type Service struct {
	engine    *tplapp.Engine
	profiles  ProfileLoader
	devices   DeviceLoader
	customers CustomerLoader
	pops      POPLoader
	jobs      JobRepository
	batches   BatchRepository
	notify    Notifier
}

func NewService(
	engine *tplapp.Engine,
	profiles ProfileLoader, devices DeviceLoader,
	customers CustomerLoader, pops POPLoader,
	jobs JobRepository, batches BatchRepository,
	notify Notifier,
) *Service {
	return &Service{
		engine: engine, profiles: profiles, devices: devices,
		customers: customers, pops: pops, jobs: jobs, batches: batches, notify: notify,
	}
}

// ApplyToDevice resolve o profile contra o device + customer + pop e
// enfileira 1 job. Retorna o job criado já com payload renderizado.
func (s *Service) ApplyToDevice(
	ctx context.Context,
	profileID, deviceID uuid.UUID,
	requestedBy *uuid.UUID,
) (*prov.Job, error) {
	prof, dev, tplCtx, err := s.resolveContext(ctx, profileID, deviceID)
	if err != nil {
		return nil, err
	}
	resolved, err := s.engine.RenderProfile(prof.Parameters, tplCtx)
	if err != nil {
		return nil, fmt.Errorf("provisioning: render: %w", err)
	}
	payload, err := encodePayload(prof.ID, prof.Version, resolved)
	if err != nil {
		return nil, err
	}
	job := &prov.Job{
		DeviceID:    dev.ID,
		ProfileID:   &prof.ID,
		RequestedBy: requestedBy,
		Status:      prov.JobQueued,
		Payload:     payload,
		ScheduledAt: time.Now(),
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	if s.notify != nil {
		_ = s.notify.Notify(ctx, job.ID) // best-effort; worker tem fallback de polling
	}
	return job, nil
}

// BulkRequest descreve uma operação em massa.
type BulkRequest struct {
	ProfileID     uuid.UUID
	DeviceIDs     []uuid.UUID
	RequestedBy   uuid.UUID
	FilterSummary string
	FilterPayload prov.FilterPayload
}

// BulkResult — resumo retornado para a UI após enfileirar.
type BulkResult struct {
	BatchID            uuid.UUID
	Total              int
	Status             prov.BatchStatus
	RequiresApproval   bool
}

// ApplyBulk cria 1 batch + N jobs. Acima de ApprovalThreshold, batch fica
// awaiting_approval — jobs ainda são criados em 'queued' (worker descobre
// via batch.status='running' antes de executar; ver ClaimBatch).
//
// NOTA: a regra de bloqueio efetiva fica num check do worker (ClaimBatch
// ignora jobs cujo batch != running/queued). Aqui apenas marcamos status.
func (s *Service) ApplyBulk(ctx context.Context, req BulkRequest) (*BulkResult, error) {
	if len(req.DeviceIDs) == 0 {
		return nil, fmt.Errorf("provisioning: lista de devices vazia")
	}
	prof, err := s.profiles.LoadFull(ctx, req.ProfileID)
	if err != nil {
		return nil, err
	}

	filterJSON, err := json.Marshal(req.FilterPayload)
	if err != nil {
		return nil, err
	}

	batch := &prov.Batch{
		ProfileID:      prof.ID,
		ProfileVersion: prof.Version,
		RequestedBy:    req.RequestedBy,
		FilterSummary:  trimTo(req.FilterSummary, 1000),
		FilterPayload:  filterJSON,
		TotalDevices:   len(req.DeviceIDs),
	}
	requiresApproval := len(req.DeviceIDs) > prov.ApprovalThreshold
	if requiresApproval {
		batch.Status = prov.BatchAwaitingApproval
	} else {
		batch.Status = prov.BatchQueued
	}
	if err := s.batches.Create(ctx, batch); err != nil {
		return nil, err
	}

	for _, devID := range req.DeviceIDs {
		_, dev, tplCtx, err := s.resolveContext(ctx, prof.ID, devID)
		if err != nil {
			// não aborta o batch — registra job com erro pré-execução.
			payload, _ := json.Marshal(map[string]any{"error": err.Error()})
			_ = s.jobs.Create(ctx, &prov.Job{
				DeviceID:     devID,
				ProfileID:    &prof.ID,
				RequestedBy:  &req.RequestedBy,
				BatchID:      &batch.ID,
				Status:       prov.JobFailed,
				Payload:      payload,
				ErrorMessage: err.Error(),
				FinishedAt:   ptrTime(time.Now()),
			})
			continue
		}
		resolved, err := s.engine.RenderProfile(prof.Parameters, tplCtx)
		if err != nil {
			payload, _ := json.Marshal(map[string]any{"error": err.Error()})
			_ = s.jobs.Create(ctx, &prov.Job{
				DeviceID:     dev.ID,
				ProfileID:    &prof.ID,
				RequestedBy:  &req.RequestedBy,
				BatchID:      &batch.ID,
				Status:       prov.JobFailed,
				Payload:      payload,
				ErrorMessage: err.Error(),
				FinishedAt:   ptrTime(time.Now()),
			})
			continue
		}
		payload, err := encodePayload(prof.ID, prof.Version, resolved)
		if err != nil {
			return nil, err
		}
		j := &prov.Job{
			DeviceID:    dev.ID,
			ProfileID:   &prof.ID,
			RequestedBy: &req.RequestedBy,
			BatchID:     &batch.ID,
			Status:      prov.JobQueued,
			Payload:     payload,
			ScheduledAt: time.Now(),
		}
		if err := s.jobs.Create(ctx, j); err != nil {
			return nil, err
		}
		if !requiresApproval && s.notify != nil {
			_ = s.notify.Notify(ctx, j.ID)
		}
	}

	return &BulkResult{
		BatchID:          batch.ID,
		Total:            batch.TotalDevices,
		Status:           batch.Status,
		RequiresApproval: requiresApproval,
	}, nil
}

// PreviewToDevice — render-only, sem persistir job. Usado pela UI para mostrar
// o que será aplicado antes da confirmação.
func (s *Service) PreviewToDevice(ctx context.Context, profileID, deviceID uuid.UUID) ([]tmpl.ResolvedParameter, error) {
	prof, _, tplCtx, err := s.resolveContext(ctx, profileID, deviceID)
	if err != nil {
		return nil, err
	}
	return s.engine.RenderProfile(prof.Parameters, tplCtx)
}

// ──────────────── internals ────────────────

func (s *Service) resolveContext(
	ctx context.Context, profileID, deviceID uuid.UUID,
) (*tmpl.Profile, *inventory.Device, tplapp.Context, error) {
	prof, err := s.profiles.LoadFull(ctx, profileID)
	if err != nil {
		return nil, nil, tplapp.Context{}, err
	}
	dev, err := s.devices.GetByID(ctx, deviceID)
	if err != nil {
		return nil, nil, tplapp.Context{}, err
	}
	tplCtx := tplapp.Context{Device: dev, Now: time.Now()}
	if dev.CustomerID != nil {
		c, err := s.customers.GetByID(ctx, *dev.CustomerID)
		if err == nil {
			tplCtx.Customer = c
		}
	}
	if dev.POPID != nil {
		p, err := s.pops.GetByID(ctx, *dev.POPID)
		if err == nil {
			tplCtx.POP = p
		}
	}
	return prof, dev, tplCtx, nil
}

// PayloadEnvelope — formato canônico do payload persistido. Mantém referência
// estável ao profile/version mesmo se o profile mudar antes do worker executar.
type PayloadEnvelope struct {
	ProfileID      uuid.UUID                 `json:"profile_id"`
	ProfileVersion int                       `json:"profile_version"`
	Parameters     []tmpl.ResolvedParameter  `json:"parameters"`
}

func encodePayload(profileID uuid.UUID, version int, params []tmpl.ResolvedParameter) ([]byte, error) {
	return json.Marshal(PayloadEnvelope{
		ProfileID:      profileID,
		ProfileVersion: version,
		Parameters:     params,
	})
}

// DecodePayload — usado pelo worker para reconstruir o set de params.
func DecodePayload(raw []byte) (*PayloadEnvelope, error) {
	var p PayloadEnvelope
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func trimTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func ptrTime(t time.Time) *time.Time { return &t }
