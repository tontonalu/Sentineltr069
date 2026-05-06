package templates

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
)

// ──────────── Fakes ────────────

type fakeProfileRepo struct {
	store map[uuid.UUID]*tmpl.Profile
}

func newFakeProfileRepo() *fakeProfileRepo { return &fakeProfileRepo{store: map[uuid.UUID]*tmpl.Profile{}} }

func (r *fakeProfileRepo) Create(_ context.Context, p *tmpl.Profile) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	r.store[p.ID] = p
	return nil
}

func (r *fakeProfileRepo) Update(_ context.Context, p *tmpl.Profile) error {
	if _, ok := r.store[p.ID]; !ok {
		return tmpl.ErrProfileNotFound
	}
	r.store[p.ID] = p
	return nil
}

func (r *fakeProfileRepo) GetByID(_ context.Context, id uuid.UUID) (*tmpl.Profile, error) {
	p, ok := r.store[id]
	if !ok {
		return nil, tmpl.ErrProfileNotFound
	}
	cp := *p
	return &cp, nil
}

func (r *fakeProfileRepo) IncrementVersion(_ context.Context, id uuid.UUID) (int, error) {
	p, ok := r.store[id]
	if !ok {
		return 0, tmpl.ErrProfileNotFound
	}
	p.Version++
	return p.Version, nil
}

func (r *fakeProfileRepo) SetActive(_ context.Context, id uuid.UUID, active bool) error {
	p, ok := r.store[id]
	if !ok {
		return tmpl.ErrProfileNotFound
	}
	p.IsActive = active
	return nil
}

func (r *fakeProfileRepo) ListByModel(_ context.Context, modelID uuid.UUID) ([]tmpl.Profile, error) {
	var out []tmpl.Profile
	for _, p := range r.store {
		if p.ModelID != nil && *p.ModelID == modelID {
			out = append(out, *p)
		}
	}
	return out, nil
}

type fakeParamRepo struct {
	store map[uuid.UUID][]tmpl.Parameter
}

func newFakeParamRepo() *fakeParamRepo { return &fakeParamRepo{store: map[uuid.UUID][]tmpl.Parameter{}} }

func (r *fakeParamRepo) ListByProfile(_ context.Context, id uuid.UUID) ([]tmpl.Parameter, error) {
	cp := make([]tmpl.Parameter, len(r.store[id]))
	copy(cp, r.store[id])
	return cp, nil
}

func (r *fakeParamRepo) Replace(_ context.Context, id uuid.UUID, ps []tmpl.Parameter) error {
	cp := make([]tmpl.Parameter, len(ps))
	for i, p := range ps {
		if p.ID == uuid.Nil {
			p.ID = uuid.New()
		}
		p.ProfileID = id
		cp[i] = p
	}
	r.store[id] = cp
	return nil
}

type fakeHistory struct {
	entries []tmpl.HistoryEntry
}

func (h *fakeHistory) Append(_ context.Context, e *tmpl.HistoryEntry) error {
	e.ID = int64(len(h.entries) + 1)
	h.entries = append(h.entries, *e)
	return nil
}

// ──────────── Tests ────────────

func TestServiceCreateBumpsToVersion1AndAppendsHistory(t *testing.T) {
	pr, par, hr := newFakeProfileRepo(), newFakeParamRepo(), &fakeHistory{}
	svc := NewService(pr, par, hr)
	p, err := svc.Create(context.Background(), CreateInput{
		Name:     "wifi-default",
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "wifi.ssid.2g", TRPath: "Device.WiFi.SSID.1.SSID", ValueTemplate: "X", DataType: tmpl.DataTypeString},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Version != 1 {
		t.Fatalf("expected v1, got %d", p.Version)
	}
	if len(hr.entries) != 1 || hr.entries[0].Version != 1 {
		t.Fatalf("history not appended on create")
	}
}

func TestServiceCreateRejectsEmptyParams(t *testing.T) {
	svc := NewService(newFakeProfileRepo(), newFakeParamRepo(), &fakeHistory{})
	_, err := svc.Create(context.Background(), CreateInput{Name: "x"})
	if !errors.Is(err, tmpl.ErrEmptyParameters) {
		t.Fatalf("got %v", err)
	}
}

func TestServiceUpdateNoOpKeepsVersion(t *testing.T) {
	pr, par, hr := newFakeProfileRepo(), newFakeParamRepo(), &fakeHistory{}
	svc := NewService(pr, par, hr)
	created, _ := svc.Create(context.Background(), CreateInput{
		Name:     "wifi",
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "v", DataType: tmpl.DataTypeString},
		},
	})
	updated, err := svc.Update(context.Background(), UpdateInput{
		ID:       created.ID,
		Name:     created.Name,
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "v", DataType: tmpl.DataTypeString},
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 1 {
		t.Fatalf("expected v1 after no-op, got %d", updated.Version)
	}
	if len(hr.entries) != 1 {
		t.Fatalf("no-op não deve gerar history; got %d", len(hr.entries))
	}
}

func TestServiceUpdateChangedParamsBumpsVersion(t *testing.T) {
	pr, par, hr := newFakeProfileRepo(), newFakeParamRepo(), &fakeHistory{}
	svc := NewService(pr, par, hr)
	created, _ := svc.Create(context.Background(), CreateInput{
		Name:     "wifi",
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "v", DataType: tmpl.DataTypeString},
		},
	})
	updated, err := svc.Update(context.Background(), UpdateInput{
		ID:       created.ID,
		Name:     created.Name,
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "OUTRO", DataType: tmpl.DataTypeString},
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected v2, got %d", updated.Version)
	}
	if len(hr.entries) != 2 || hr.entries[1].Version != 2 {
		t.Fatalf("history must include v2")
	}
}

func TestServiceUpdateHeaderOnlyBumps(t *testing.T) {
	pr, par, hr := newFakeProfileRepo(), newFakeParamRepo(), &fakeHistory{}
	svc := NewService(pr, par, hr)
	created, _ := svc.Create(context.Background(), CreateInput{
		Name:     "wifi",
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "v", DataType: tmpl.DataTypeString},
		},
	})
	updated, err := svc.Update(context.Background(), UpdateInput{
		ID:          created.ID,
		Name:        "wifi-renamed",
		Description: "atualizada",
		IsActive:    true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "v", DataType: tmpl.DataTypeString},
		},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 || updated.Name != "wifi-renamed" {
		t.Fatalf("expected v2 + renamed, got v%d name=%q", updated.Version, updated.Name)
	}
}

func TestServiceValidateDuplicatesCanonicalKey(t *testing.T) {
	svc := NewService(newFakeProfileRepo(), newFakeParamRepo(), &fakeHistory{})
	_, err := svc.Create(context.Background(), CreateInput{
		Name:     "x",
		IsActive: true,
		Parameters: []tmpl.Parameter{
			{CanonicalKey: "k", TRPath: "p", ValueTemplate: "v", DataType: tmpl.DataTypeString},
			{CanonicalKey: "k", TRPath: "q", ValueTemplate: "v", DataType: tmpl.DataTypeString},
		},
	})
	if err == nil {
		t.Fatal("esperava erro de canonical_key duplicada")
	}
}
