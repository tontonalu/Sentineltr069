package provisioning

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	tplapp "github.com/celinet/sentinel-acs/internal/application/templates"
	"github.com/celinet/sentinel-acs/internal/domain/inventory"
	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// ──────────── Fakes mínimos ────────────

type stubProfileLoader struct{ p *tmpl.Profile }

func (s stubProfileLoader) LoadFull(_ context.Context, _ uuid.UUID) (*tmpl.Profile, error) {
	return s.p, nil
}

type stubDeviceLoader struct{ d *inventory.Device }

func (s stubDeviceLoader) GetByID(_ context.Context, _ uuid.UUID) (*inventory.Device, error) {
	cp := *s.d
	return &cp, nil
}

type stubCustomerLoader struct{}

func (stubCustomerLoader) GetByID(_ context.Context, _ uuid.UUID) (*inventory.Customer, error) {
	return nil, nil
}

type stubPOPLoader struct{}

func (stubPOPLoader) GetByID(_ context.Context, _ uuid.UUID) (*inventory.POP, error) {
	return nil, nil
}

type stubJobRepo struct {
	created int
	jobs    []prov.Job
}

func (s *stubJobRepo) Create(_ context.Context, j *prov.Job) error {
	if j.ID == uuid.Nil {
		j.ID = uuid.New()
	}
	s.created++
	s.jobs = append(s.jobs, *j)
	return nil
}

type stubBatchRepo struct{ batch *prov.Batch }

func (s *stubBatchRepo) Create(_ context.Context, b *prov.Batch) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	s.batch = b
	return nil
}

// stubGate fixa o resultado de IsHomologated (homologated boolean) e opcionalmente
// retorna erro para testar caminho de falha do gate.
type stubGate struct {
	homologated bool
	err         error
}

func (g stubGate) IsHomologated(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return g.homologated, g.err
}

// ──────────── helpers ────────────

func newSvcWithGate(t *testing.T, gate HomologationGate, prof *tmpl.Profile, dev *inventory.Device) (*Service, *stubBatchRepo, *stubJobRepo) {
	t.Helper()
	jobs := &stubJobRepo{}
	batches := &stubBatchRepo{}
	svc := NewService(
		tplapp.NewEngine(),
		stubProfileLoader{p: prof},
		stubDeviceLoader{d: dev},
		stubCustomerLoader{},
		stubPOPLoader{},
		jobs, batches, nil,
	)
	if gate != nil {
		svc = svc.WithHomologationGate(gate)
	}
	return svc, batches, jobs
}

func sampleProfileWithModel(modelID *uuid.UUID) *tmpl.Profile {
	return &tmpl.Profile{
		ID:      uuid.New(),
		Name:    "test",
		Version: 1,
		ModelID: modelID,
		Parameters: []tmpl.Parameter{
			{
				CanonicalKey:  "wifi.ssid.2g",
				TRPath:        "Device.WiFi.SSID.1.SSID",
				ValueTemplate: "test-ssid",
				DataType:      tmpl.DataTypeString,
			},
		},
	}
}

func sampleDevice(modelID uuid.UUID) *inventory.Device {
	return &inventory.Device{
		ID:         uuid.New(),
		GenieACSID: "g-1",
		ModelID:    &modelID,
		Status:     inventory.StatusOnline,
	}
}

// ──────────── Tests ────────────

func TestApplyBulk_GateBlocksWhenNotHomologated(t *testing.T) {
	modelID := uuid.New()
	prof := sampleProfileWithModel(&modelID)
	dev := sampleDevice(modelID)

	svc, batches, jobs := newSvcWithGate(t, stubGate{homologated: false}, prof, dev)

	_, err := svc.ApplyBulk(context.Background(), BulkRequest{
		ProfileID:   prof.ID,
		DeviceIDs:   []uuid.UUID{dev.ID},
		RequestedBy: uuid.New(),
	})
	if !errors.Is(err, prov.ErrProfileNotHomologated) {
		t.Fatalf("err=%v, want ErrProfileNotHomologated", err)
	}
	if batches.batch != nil {
		t.Errorf("batch criado mesmo com gate bloqueando")
	}
	if jobs.created != 0 {
		t.Errorf("jobs criados (%d) mesmo com gate bloqueando", jobs.created)
	}
}

func TestApplyBulk_GatePassesWhenHomologated(t *testing.T) {
	modelID := uuid.New()
	prof := sampleProfileWithModel(&modelID)
	dev := sampleDevice(modelID)

	svc, batches, jobs := newSvcWithGate(t, stubGate{homologated: true}, prof, dev)

	res, err := svc.ApplyBulk(context.Background(), BulkRequest{
		ProfileID:   prof.ID,
		DeviceIDs:   []uuid.UUID{dev.ID},
		RequestedBy: uuid.New(),
	})
	if err != nil {
		t.Fatalf("ApplyBulk: %v", err)
	}
	if batches.batch == nil {
		t.Errorf("batch não foi criado")
	}
	if jobs.created != 1 {
		t.Errorf("jobs criados=%d, esperava 1", jobs.created)
	}
	if res.Total != 1 {
		t.Errorf("res.Total=%d, esperava 1", res.Total)
	}
}

func TestApplyBulk_GenericProfileBypassesGate(t *testing.T) {
	// profile sem model_id (genérico) — gate não deveria checar.
	prof := sampleProfileWithModel(nil)
	dev := sampleDevice(uuid.New())

	// gate diz "não homologado" — mas como é genérico, não deve ser consultado.
	svc, batches, jobs := newSvcWithGate(t, stubGate{homologated: false}, prof, dev)

	if _, err := svc.ApplyBulk(context.Background(), BulkRequest{
		ProfileID:   prof.ID,
		DeviceIDs:   []uuid.UUID{dev.ID},
		RequestedBy: uuid.New(),
	}); err != nil {
		t.Fatalf("ApplyBulk genérico falhou: %v", err)
	}
	if batches.batch == nil {
		t.Errorf("batch não foi criado para profile genérico")
	}
	if jobs.created != 1 {
		t.Errorf("jobs criados=%d, esperava 1", jobs.created)
	}
}

func TestApplyBulk_RejectsDevicesOfDifferentModel(t *testing.T) {
	// profile homologado para modelo X; device alvo pertence ao modelo Y.
	// Mesmo com gate liberando, o filtro per-device deve registrar job-fail.
	profModelID := uuid.New()
	prof := sampleProfileWithModel(&profModelID)

	otherModelID := uuid.New() // modelo diferente
	dev := sampleDevice(otherModelID)

	svc, batches, jobs := newSvcWithGate(t, stubGate{homologated: true}, prof, dev)

	res, err := svc.ApplyBulk(context.Background(), BulkRequest{
		ProfileID:   prof.ID,
		DeviceIDs:   []uuid.UUID{dev.ID},
		RequestedBy: uuid.New(),
	})
	if err != nil {
		t.Fatalf("ApplyBulk: %v", err)
	}
	if batches.batch == nil {
		t.Errorf("batch não foi criado")
	}
	// Job deveria ter sido criado em estado failed por mismatch de modelo.
	if jobs.created != 1 {
		t.Errorf("jobs criados=%d, esperava 1 (mesmo que falho)", jobs.created)
	}
	if jobs.jobs[0].Status != prov.JobFailed {
		t.Errorf("job status=%q, esperava failed (mismatch de modelo)", jobs.jobs[0].Status)
	}
	if res.Total != 1 {
		t.Errorf("res.Total=%d", res.Total)
	}
}

func TestApplyBulk_AllowsSameModelDevices(t *testing.T) {
	modelID := uuid.New()
	prof := sampleProfileWithModel(&modelID)
	dev := sampleDevice(modelID) // mesmo modelo do profile

	svc, batches, jobs := newSvcWithGate(t, stubGate{homologated: true}, prof, dev)

	if _, err := svc.ApplyBulk(context.Background(), BulkRequest{
		ProfileID:   prof.ID,
		DeviceIDs:   []uuid.UUID{dev.ID},
		RequestedBy: uuid.New(),
	}); err != nil {
		t.Fatal(err)
	}
	if batches.batch == nil || jobs.created != 1 {
		t.Errorf("apply happy path falhou: batch=%v jobs=%d", batches.batch, jobs.created)
	}
}

func TestApplyBulk_NoGateMeansNoBlock(t *testing.T) {
	// Service criado sem WithHomologationGate — comportamento legado.
	modelID := uuid.New()
	prof := sampleProfileWithModel(&modelID)
	dev := sampleDevice(modelID)

	svc, batches, jobs := newSvcWithGate(t, nil, prof, dev)

	if _, err := svc.ApplyBulk(context.Background(), BulkRequest{
		ProfileID:   prof.ID,
		DeviceIDs:   []uuid.UUID{dev.ID},
		RequestedBy: uuid.New(),
	}); err != nil {
		t.Fatalf("ApplyBulk: %v", err)
	}
	if batches.batch == nil || jobs.created != 1 {
		t.Errorf("sem gate, ApplyBulk deveria criar batch+job — got batch=%v jobs=%d", batches.batch, jobs.created)
	}
}
