package homologation

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	hom "github.com/celinet/sentinel-acs/internal/domain/homologation"
	inv "github.com/celinet/sentinel-acs/internal/domain/inventory"
	tmpl "github.com/celinet/sentinel-acs/internal/domain/templates"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// Aliases para encurtar assinaturas dos fakes que implementam tplapp.{Profile,Parameter,History}Repo.
type (
	tmplDomainProfile      = tmpl.Profile
	tmplDomainParameter    = tmpl.Parameter
	tmplDomainHistoryEntry = tmpl.HistoryEntry
)

var tmplDomainErrNotFound = tmpl.ErrProfileNotFound

// ──────────── SessionRepo fake ────────────

type fakeSessionRepo struct {
	mu    sync.Mutex
	store map[uuid.UUID]*hom.Session
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{store: map[uuid.UUID]*hom.Session{}}
}

func (r *fakeSessionRepo) Save(_ context.Context, s *hom.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now()
	}
	// emula UNIQUE parcial: só uma sessão ativa por device
	if s.Status.IsActive() {
		for _, ex := range r.store {
			if ex.ID == s.ID {
				continue
			}
			if ex.LabDeviceID == s.LabDeviceID && ex.Status.IsActive() {
				return hom.ErrSessionAlreadyActive
			}
		}
	}
	cp := *s
	r.store[s.ID] = &cp
	return nil
}

func (r *fakeSessionRepo) GetByID(_ context.Context, id uuid.UUID) (*hom.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.store[id]
	if !ok {
		return nil, hom.ErrSessionNotFound
	}
	cp := *s
	return &cp, nil
}

func (r *fakeSessionRepo) List(_ context.Context, _ hom.SessionFilter) ([]hom.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]hom.Session, 0, len(r.store))
	for _, s := range r.store {
		out = append(out, *s)
	}
	return out, nil
}

func (r *fakeSessionRepo) UpdateStatus(_ context.Context, id uuid.UUID, st hom.SessionStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.store[id]
	if !ok {
		return hom.ErrSessionNotFound
	}
	s.Status = st
	if st == hom.SessionCompleted || st == hom.SessionAbandoned {
		t := time.Now()
		s.FinishedAt = &t
	}
	return nil
}

func (r *fakeSessionRepo) UpdateTreeSnapshot(_ context.Context, id uuid.UUID, snap []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.store[id]
	if !ok {
		return hom.ErrSessionNotFound
	}
	s.TreeSnapshot = snap
	return nil
}

func (r *fakeSessionRepo) SetGeneratedProfile(_ context.Context, id, pid uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.store[id]
	if !ok {
		return hom.ErrSessionNotFound
	}
	s.GeneratedProfileID = &pid
	return nil
}

func (r *fakeSessionRepo) PurgeOldSnapshots(_ context.Context, before time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.store {
		if (s.Status == hom.SessionCompleted || s.Status == hom.SessionAbandoned) &&
			s.FinishedAt != nil && s.FinishedAt.Before(before) && s.TreeSnapshot != nil {
			s.TreeSnapshot = nil
			n++
		}
	}
	return n, nil
}

func (r *fakeSessionRepo) ActiveByDevice(_ context.Context, deviceID uuid.UUID) (*hom.Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.store {
		if s.LabDeviceID == deviceID && s.Status.IsActive() {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}

// ──────────── MappingRepo fake ────────────

type fakeMappingRepo struct {
	mu    sync.Mutex
	store map[uuid.UUID]*hom.Mapping
}

func newFakeMappingRepo() *fakeMappingRepo {
	return &fakeMappingRepo{store: map[uuid.UUID]*hom.Mapping{}}
}

func (r *fakeMappingRepo) ListBySession(_ context.Context, sid uuid.UUID) ([]hom.Mapping, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []hom.Mapping
	for _, m := range r.store {
		if m.SessionID == sid {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (r *fakeMappingRepo) GetByID(_ context.Context, id uuid.UUID) (*hom.Mapping, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.store[id]
	if !ok {
		return nil, hom.ErrMappingNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *fakeMappingRepo) Create(_ context.Context, m *hom.Mapping) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ex := range r.store {
		if ex.SessionID == m.SessionID && ex.CanonicalKey == m.CanonicalKey {
			return hom.ErrMappingDuplicate
		}
	}
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	cp := *m
	r.store[m.ID] = &cp
	return nil
}

func (r *fakeMappingRepo) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.store[id]; !ok {
		return hom.ErrMappingNotFound
	}
	delete(r.store, id)
	return nil
}

func (r *fakeMappingRepo) UpdateTemplate(_ context.Context, id uuid.UUID, vt string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.store[id]
	if !ok {
		return hom.ErrMappingNotFound
	}
	m.ValueTemplate = vt
	return nil
}

func (r *fakeMappingRepo) UpdateReadResult(_ context.Context, id uuid.UUID, st hom.TestStatus, val, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.store[id]
	if !ok {
		return hom.ErrMappingNotFound
	}
	m.ReadStatus = st
	if val != "" {
		m.ReadValue = &val
	}
	if errMsg != "" {
		m.LastError = &errMsg
	}
	t := time.Now()
	m.TestedAt = &t
	return nil
}

func (r *fakeMappingRepo) UpdateWriteResult(_ context.Context, id uuid.UUID, st hom.TestStatus, val, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.store[id]
	if !ok {
		return hom.ErrMappingNotFound
	}
	m.WriteStatus = st
	if val != "" {
		m.WriteTestValue = &val
	}
	if errMsg != "" {
		m.LastError = &errMsg
	}
	t := time.Now()
	m.TestedAt = &t
	return nil
}

// ──────────── CanonicalKeyRepo fake ────────────

type fakeCanonicalRepo struct {
	keys []hom.CanonicalKey
}

func (r *fakeCanonicalRepo) List(_ context.Context, category string) ([]hom.CanonicalKey, error) {
	if category == "" {
		return append([]hom.CanonicalKey(nil), r.keys...), nil
	}
	var out []hom.CanonicalKey
	for _, k := range r.keys {
		if k.Category == category {
			out = append(out, k)
		}
	}
	return out, nil
}

func (r *fakeCanonicalRepo) GetByKey(_ context.Context, key string) (*hom.CanonicalKey, error) {
	for i := range r.keys {
		if r.keys[i].Key == key {
			cp := r.keys[i]
			return &cp, nil
		}
	}
	return nil, hom.ErrCanonicalKeyNotFound
}

func (r *fakeCanonicalRepo) GetByID(_ context.Context, id uuid.UUID) (*hom.CanonicalKey, error) {
	for i := range r.keys {
		if r.keys[i].ID == id {
			cp := r.keys[i]
			return &cp, nil
		}
	}
	return nil, hom.ErrCanonicalKeyNotFound
}

func (r *fakeCanonicalRepo) Create(_ context.Context, k *hom.CanonicalKey) error {
	if k.ID == uuid.Nil {
		k.ID = uuid.New()
	}
	r.keys = append(r.keys, *k)
	return nil
}

func (r *fakeCanonicalRepo) Update(_ context.Context, k *hom.CanonicalKey) error {
	for i := range r.keys {
		if r.keys[i].ID == k.ID {
			r.keys[i] = *k
			return nil
		}
	}
	return hom.ErrCanonicalKeyNotFound
}

func (r *fakeCanonicalRepo) Delete(_ context.Context, id uuid.UUID) error {
	for i := range r.keys {
		if r.keys[i].ID == id {
			r.keys = append(r.keys[:i], r.keys[i+1:]...)
			return nil
		}
	}
	return hom.ErrCanonicalKeyNotFound
}

// ──────────── ModelHomologationRepo fake ────────────

type fakeHomModelRepo struct {
	mu      sync.Mutex
	records []hom.ModelHomologation
}

func (r *fakeHomModelRepo) Create(_ context.Context, h *hom.ModelHomologation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	if h.HomologatedAt.IsZero() {
		h.HomologatedAt = time.Now()
	}
	if !h.Status.Valid() {
		h.Status = hom.StatusHomologated
	}
	r.records = append(r.records, *h)
	return nil
}

func (r *fakeHomModelRepo) IsHomologated(_ context.Context, modelID, profileID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.records {
		if h.ModelID == modelID && h.ProfileID == profileID && h.Status == hom.StatusHomologated {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeHomModelRepo) ListByModel(_ context.Context, modelID uuid.UUID) ([]hom.ModelHomologation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []hom.ModelHomologation
	for _, h := range r.records {
		if h.ModelID == modelID {
			out = append(out, h)
		}
	}
	return out, nil
}

func (r *fakeHomModelRepo) ListByProfile(_ context.Context, profileID uuid.UUID) ([]hom.ModelHomologation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []hom.ModelHomologation
	for _, h := range r.records {
		if h.ProfileID == profileID {
			out = append(out, h)
		}
	}
	return out, nil
}

func (r *fakeHomModelRepo) Deprecate(_ context.Context, id uuid.UUID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.records {
		if r.records[i].ID == id && r.records[i].Status == hom.StatusHomologated {
			t := time.Now()
			r.records[i].Status = hom.StatusDeprecated
			r.records[i].DeprecatedAt = &t
			r.records[i].DeprecatedReason = reason
			return nil
		}
	}
	return hom.ErrModelHomologationNotFound
}

// ──────────── DeviceRepository (parcial, só GetByID + SetHomologationLab) ────────────

type fakeDeviceRepo struct {
	mu    sync.Mutex
	store map[uuid.UUID]*inv.Device
}

func newFakeDeviceRepo() *fakeDeviceRepo {
	return &fakeDeviceRepo{store: map[uuid.UUID]*inv.Device{}}
}

func (r *fakeDeviceRepo) put(d inv.Device) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := d
	r.store[cp.ID] = &cp
}

func (r *fakeDeviceRepo) GetByID(_ context.Context, id uuid.UUID) (*inv.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.store[id]
	if !ok {
		return nil, inv.ErrDeviceNotFound
	}
	cp := *d
	return &cp, nil
}

func (r *fakeDeviceRepo) Upsert(_ context.Context, d *inv.Device) error {
	r.put(*d)
	return nil
}
func (r *fakeDeviceRepo) GetByGenieACSID(_ context.Context, _ string) (*inv.Device, error) {
	return nil, inv.ErrDeviceNotFound
}
func (r *fakeDeviceRepo) List(_ context.Context, _ inv.DeviceFilter, _ inv.Page) ([]inv.Device, int, error) {
	return nil, 0, nil
}
func (r *fakeDeviceRepo) LinkCustomer(_ context.Context, _ uuid.UUID, _ *uuid.UUID) error { return nil }
func (r *fakeDeviceRepo) MarkInform(_ context.Context, _ string, _ time.Time, _ *time.Time, _ string) error {
	return nil
}
func (r *fakeDeviceRepo) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.store[id]; !ok {
		return inv.ErrDeviceNotFound
	}
	delete(r.store, id)
	return nil
}
func (r *fakeDeviceRepo) SetHomologationLab(_ context.Context, id uuid.UUID, isLab bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.store[id]
	if !ok {
		return inv.ErrDeviceNotFound
	}
	d.IsHomologationLab = isLab
	return nil
}

// ──────────── DeviceModelRepository (parcial, só GetByID) ────────────

type fakeModelRepo struct {
	store map[uuid.UUID]inv.DeviceModel
}

func newFakeModelRepo() *fakeModelRepo {
	return &fakeModelRepo{store: map[uuid.UUID]inv.DeviceModel{}}
}

func (r *fakeModelRepo) put(m inv.DeviceModel) { r.store[m.ID] = m }

func (r *fakeModelRepo) GetByID(_ context.Context, id uuid.UUID) (*inv.DeviceModel, error) {
	m, ok := r.store[id]
	if !ok {
		return nil, inv.ErrModelNotFound
	}
	cp := m
	return &cp, nil
}
func (r *fakeModelRepo) Create(_ context.Context, _ *inv.DeviceModel) error { return nil }
func (r *fakeModelRepo) GetByVendorAndModel(_ context.Context, _ uuid.UUID, _ string) (*inv.DeviceModel, error) {
	return nil, inv.ErrModelNotFound
}
func (r *fakeModelRepo) ListByVendor(_ context.Context, _ uuid.UUID) ([]inv.DeviceModel, error) {
	return nil, nil
}
func (r *fakeModelRepo) List(_ context.Context) ([]inv.DeviceModel, error) { return nil, nil }

// ──────────── GenieACSPort fake ────────────

type fakeGenie struct {
	mu        sync.Mutex
	devicesBy map[string]*genieacs.Device
	setCalls  []genieacs.Parameter // log de cada SetParameterValues
	failOnSet bool
}

func newFakeGenie() *fakeGenie {
	return &fakeGenie{devicesBy: map[string]*genieacs.Device{}}
}

func (g *fakeGenie) putDevice(id string, raw map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.devicesBy[id] = &genieacs.Device{ID: id, Raw: raw}
}

func (g *fakeGenie) GetDevice(_ context.Context, id string) (*genieacs.Device, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	d, ok := g.devicesBy[id]
	if !ok {
		return nil, genieacs.ErrDeviceNotFound
	}
	cp := *d
	return &cp, nil
}

func (g *fakeGenie) Refresh(_ context.Context, _, _ string) (genieacs.TaskID, error) {
	return "task-refresh", nil
}

func (g *fakeGenie) GetParameterValues(_ context.Context, _ string, _ []string) (genieacs.TaskID, error) {
	return "task-get", nil
}

func (g *fakeGenie) SetParameterValues(_ context.Context, _ string, params []genieacs.Parameter) (genieacs.TaskID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failOnSet {
		return "", errFakeSet
	}
	g.setCalls = append(g.setCalls, params...)
	return "task-set", nil
}

var errFakeSet = errFake("fake genieacs SetParameterValues")

type errFake string

func (e errFake) Error() string { return string(e) }

// ──────────── tplapp.ProfileRepo / ParameterRepo / HistoryRepo fakes ────────────
// Necessários porque o homologation.Service.Complete delega Create do profile
// final para tplapp.Service. Construímos um Service real com estes fakes.

type fakeTplProfileRepo struct {
	mu    sync.Mutex
	store map[uuid.UUID]*tmplProfileEntry
}

// tmplProfileEntry usa o tipo do domain/templates — alias local para clareza.
type tmplProfileEntry = tmplDomainProfile

func newFakeTplProfileRepo() *fakeTplProfileRepo {
	return &fakeTplProfileRepo{store: map[uuid.UUID]*tmplProfileEntry{}}
}

func (r *fakeTplProfileRepo) Create(_ context.Context, p *tmplDomainProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	cp := *p
	r.store[p.ID] = &cp
	return nil
}
func (r *fakeTplProfileRepo) Update(_ context.Context, p *tmplDomainProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.store[p.ID]; !ok {
		return tmplDomainErrNotFound
	}
	cp := *p
	r.store[p.ID] = &cp
	return nil
}
func (r *fakeTplProfileRepo) GetByID(_ context.Context, id uuid.UUID) (*tmplDomainProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.store[id]
	if !ok {
		return nil, tmplDomainErrNotFound
	}
	cp := *p
	return &cp, nil
}
func (r *fakeTplProfileRepo) IncrementVersion(_ context.Context, id uuid.UUID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.store[id]
	if !ok {
		return 0, tmplDomainErrNotFound
	}
	p.Version++
	return p.Version, nil
}
func (r *fakeTplProfileRepo) SetActive(_ context.Context, id uuid.UUID, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.store[id]
	if !ok {
		return tmplDomainErrNotFound
	}
	p.IsActive = active
	return nil
}

type fakeTplParamRepo struct {
	mu    sync.Mutex
	store map[uuid.UUID][]tmplDomainParameter
}

func newFakeTplParamRepo() *fakeTplParamRepo {
	return &fakeTplParamRepo{store: map[uuid.UUID][]tmplDomainParameter{}}
}

func (r *fakeTplParamRepo) ListByProfile(_ context.Context, id uuid.UUID) ([]tmplDomainParameter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]tmplDomainParameter, len(r.store[id]))
	copy(cp, r.store[id])
	return cp, nil
}
func (r *fakeTplParamRepo) Replace(_ context.Context, id uuid.UUID, ps []tmplDomainParameter) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]tmplDomainParameter, len(ps))
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

type fakeTplHistory struct {
	mu      sync.Mutex
	entries []tmplDomainHistoryEntry
}

func (h *fakeTplHistory) Append(_ context.Context, e *tmplDomainHistoryEntry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	e.ID = int64(len(h.entries) + 1)
	h.entries = append(h.entries, *e)
	return nil
}
