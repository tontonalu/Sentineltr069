package inventory

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
)

// In-memory fakes para testes de SyncService sem PG real.

type fakeDeviceRepo struct {
	mu      sync.Mutex
	byID    map[uuid.UUID]*domain.Device
	byGenie map[string]uuid.UUID
}

func newFakeDeviceRepo() *fakeDeviceRepo {
	return &fakeDeviceRepo{
		byID:    map[uuid.UUID]*domain.Device{},
		byGenie: map[string]uuid.UUID{},
	}
}

func (r *fakeDeviceRepo) Upsert(_ context.Context, d *domain.Device) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existingID, ok := r.byGenie[d.GenieACSID]; ok {
		d.ID = existingID
	} else if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	r.byGenie[d.GenieACSID] = d.ID
	now := time.Now()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	cpy := *d
	r.byID[d.ID] = &cpy
	return nil
}

func (r *fakeDeviceRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrDeviceNotFound
	}
	cpy := *d
	return &cpy, nil
}

func (r *fakeDeviceRepo) GetByGenieACSID(_ context.Context, genieacsID string) (*domain.Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byGenie[genieacsID]
	if !ok {
		return nil, domain.ErrDeviceNotFound
	}
	cpy := *r.byID[id]
	return &cpy, nil
}

func (r *fakeDeviceRepo) List(_ context.Context, _ domain.DeviceFilter, _ domain.Page) ([]domain.Device, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Device, 0, len(r.byID))
	for _, d := range r.byID {
		out = append(out, *d)
	}
	return out, len(out), nil
}

func (r *fakeDeviceRepo) LinkCustomer(_ context.Context, deviceID uuid.UUID, customerID *uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.byID[deviceID]
	if !ok {
		return domain.ErrDeviceNotFound
	}
	d.CustomerID = customerID
	return nil
}

func (r *fakeDeviceRepo) MarkInform(_ context.Context, genieacsID string, lastInform time.Time, lastBoot *time.Time, fwVersion string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byGenie[genieacsID]
	if !ok {
		return domain.ErrDeviceNotFound
	}
	d := r.byID[id]
	d.LastInformAt = &lastInform
	if lastBoot != nil {
		d.LastBootAt = lastBoot
	}
	if fwVersion != "" {
		d.FirmwareVersion = fwVersion
	}
	d.Status = domain.StatusOnline
	return nil
}

func (r *fakeDeviceRepo) Delete(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.byID[id]
	if !ok {
		return domain.ErrDeviceNotFound
	}
	delete(r.byID, id)
	delete(r.byGenie, d.GenieACSID)
	return nil
}

func (r *fakeDeviceRepo) SetHomologationLab(_ context.Context, id uuid.UUID, isLab bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.byID[id]
	if !ok {
		return domain.ErrDeviceNotFound
	}
	d.IsHomologationLab = isLab
	return nil
}

// ──────────── CustomerRepo fake ────────────

type fakeCustomerRepo struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*domain.Customer
	byPPPoE  map[string]uuid.UUID
	byExtKey map[string]uuid.UUID // "<source>|<id>"
}

func newFakeCustomerRepo(seed ...domain.Customer) *fakeCustomerRepo {
	r := &fakeCustomerRepo{
		byID:     map[uuid.UUID]*domain.Customer{},
		byPPPoE:  map[string]uuid.UUID{},
		byExtKey: map[string]uuid.UUID{},
	}
	for i := range seed {
		c := seed[i]
		if c.ID == uuid.Nil {
			c.ID = uuid.New()
		}
		r.byID[c.ID] = &c
		if c.PPPoELogin != "" {
			r.byPPPoE[c.PPPoELogin] = c.ID
		}
		if c.ExternalSource != "" && c.ExternalID != "" {
			r.byExtKey[c.ExternalSource+"|"+c.ExternalID] = c.ID
		}
	}
	return r
}

func (r *fakeCustomerRepo) Upsert(_ context.Context, c *domain.Customer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	cpy := *c
	r.byID[c.ID] = &cpy
	if c.PPPoELogin != "" {
		r.byPPPoE[c.PPPoELogin] = c.ID
	}
	return nil
}

func (r *fakeCustomerRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrCustomerNotFound
	}
	cpy := *c
	return &cpy, nil
}

func (r *fakeCustomerRepo) GetByExternal(_ context.Context, source, externalID string) (*domain.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byExtKey[source+"|"+externalID]
	if !ok {
		return nil, domain.ErrCustomerNotFound
	}
	cpy := *r.byID[id]
	return &cpy, nil
}

func (r *fakeCustomerRepo) GetByPPPoELogin(_ context.Context, login string) (*domain.Customer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byPPPoE[login]
	if !ok {
		return nil, domain.ErrCustomerNotFound
	}
	cpy := *r.byID[id]
	return &cpy, nil
}

func (r *fakeCustomerRepo) List(_ context.Context, _ domain.Page) ([]domain.Customer, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Customer, 0, len(r.byID))
	for _, c := range r.byID {
		out = append(out, *c)
	}
	return out, len(out), nil
}

func (r *fakeCustomerRepo) SetStatus(_ context.Context, id uuid.UUID, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok {
		return domain.ErrCustomerNotFound
	}
	c.Status = status
	return nil
}

// ──────────── VendorRepo / DeviceModelRepo fakes ────────────

type fakeVendorRepo struct {
	mu      sync.Mutex
	bySlug  map[string]*domain.Vendor
	byID    map[uuid.UUID]*domain.Vendor
}

func newFakeVendorRepo() *fakeVendorRepo {
	return &fakeVendorRepo{
		bySlug: map[string]*domain.Vendor{},
		byID:   map[uuid.UUID]*domain.Vendor{},
	}
}

func (r *fakeVendorRepo) Create(_ context.Context, v *domain.Vendor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.bySlug[v.Slug]; ok {
		return domain.ErrSlugDuplicate
	}
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
	v.CreatedAt = time.Now()
	cpy := *v
	r.bySlug[v.Slug] = &cpy
	r.byID[v.ID] = &cpy
	return nil
}

func (r *fakeVendorRepo) GetBySlug(_ context.Context, slug string) (*domain.Vendor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.bySlug[slug]
	if !ok {
		return nil, domain.ErrVendorNotFound
	}
	cpy := *v
	return &cpy, nil
}

func (r *fakeVendorRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.Vendor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrVendorNotFound
	}
	cpy := *v
	return &cpy, nil
}

func (r *fakeVendorRepo) List(_ context.Context) ([]domain.Vendor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Vendor, 0, len(r.byID))
	for _, v := range r.byID {
		out = append(out, *v)
	}
	return out, nil
}

type fakeModelRepo struct {
	mu     sync.Mutex
	byKey  map[string]*domain.DeviceModel // "vendorID|model"
	byID   map[uuid.UUID]*domain.DeviceModel
}

func newFakeModelRepo() *fakeModelRepo {
	return &fakeModelRepo{
		byKey: map[string]*domain.DeviceModel{},
		byID:  map[uuid.UUID]*domain.DeviceModel{},
	}
}

func modelKey(vID uuid.UUID, model string) string {
	return vID.String() + "|" + strings.ToLower(model)
}

func (r *fakeModelRepo) Create(_ context.Context, m *domain.DeviceModel) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := modelKey(m.VendorID, m.Model)
	if _, ok := r.byKey[k]; ok {
		return domain.ErrModelDuplicate
	}
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	m.CreatedAt = time.Now()
	cpy := *m
	r.byKey[k] = &cpy
	r.byID[m.ID] = &cpy
	return nil
}

func (r *fakeModelRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.DeviceModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrModelNotFound
	}
	cpy := *m
	return &cpy, nil
}

func (r *fakeModelRepo) GetByVendorAndModel(_ context.Context, vID uuid.UUID, model string) (*domain.DeviceModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.byKey[modelKey(vID, model)]
	if !ok {
		return nil, domain.ErrModelNotFound
	}
	cpy := *m
	return &cpy, nil
}

func (r *fakeModelRepo) ListByVendor(_ context.Context, vID uuid.UUID) ([]domain.DeviceModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.DeviceModel
	prefix := vID.String() + "|"
	for k, m := range r.byKey {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *m)
		}
	}
	return out, nil
}

func (r *fakeModelRepo) List(_ context.Context) ([]domain.DeviceModel, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.DeviceModel
	for _, m := range r.byKey {
		out = append(out, *m)
	}
	return out, nil
}
