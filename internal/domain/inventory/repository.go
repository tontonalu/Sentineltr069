package inventory

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Page — paginação genérica (mesma estrutura do identity, repetida para
// não criar coupling entre bounded contexts).
type Page struct {
	Offset int
	Limit  int
}

// DeviceFilter — filtros para listagem de devices.
// Campos zero-value são ignorados.
type DeviceFilter struct {
	POPID    *uuid.UUID
	VendorID *uuid.UUID
	ModelID  *uuid.UUID
	Status   string // online | offline | never_seen | unknown
	Search   string // procura por serial, MAC ou genieacs_id
	Tag      string // filtro por tag
}

// POPRepository
type POPRepository interface {
	Create(ctx context.Context, p *POP) error
	GetByID(ctx context.Context, id uuid.UUID) (*POP, error)
	List(ctx context.Context) ([]POP, error)
	Update(ctx context.Context, p *POP) error
	SetActive(ctx context.Context, id uuid.UUID, active bool) error
}

// VendorRepository
type VendorRepository interface {
	Create(ctx context.Context, v *Vendor) error
	GetBySlug(ctx context.Context, slug string) (*Vendor, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Vendor, error)
	List(ctx context.Context) ([]Vendor, error)
}

// DeviceModelRepository
type DeviceModelRepository interface {
	Create(ctx context.Context, m *DeviceModel) error
	GetByID(ctx context.Context, id uuid.UUID) (*DeviceModel, error)
	GetByVendorAndModel(ctx context.Context, vendorID uuid.UUID, model string) (*DeviceModel, error)
	ListByVendor(ctx context.Context, vendorID uuid.UUID) ([]DeviceModel, error)
	List(ctx context.Context) ([]DeviceModel, error)
}

// CustomerRepository — operações usadas pelo Plugin Voalle e pela UI.
type CustomerRepository interface {
	Upsert(ctx context.Context, c *Customer) error
	GetByID(ctx context.Context, id uuid.UUID) (*Customer, error)
	GetByExternal(ctx context.Context, source, externalID string) (*Customer, error)
	GetByPPPoELogin(ctx context.Context, login string) (*Customer, error)
	List(ctx context.Context, p Page) ([]Customer, int, error)
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
}

// DeviceRepository — devices espelhados do GenieACS.
type DeviceRepository interface {
	Upsert(ctx context.Context, d *Device) error
	GetByID(ctx context.Context, id uuid.UUID) (*Device, error)
	GetByGenieACSID(ctx context.Context, genieacsID string) (*Device, error)
	List(ctx context.Context, f DeviceFilter, p Page) ([]Device, int, error)

	// LinkCustomer associa o device a um customer (busca por PPPoE no sync).
	LinkCustomer(ctx context.Context, deviceID uuid.UUID, customerID *uuid.UUID) error

	// MarkInform — chamado pelo sync; atualiza last_inform_at + last_boot_at + status.
	MarkInform(ctx context.Context, genieacsID string, lastInform time.Time, lastBoot *time.Time, fwVersion string) error

	// Delete remove o device do Postgres. Retorna ErrDeviceNotFound se não existir.
	Delete(ctx context.Context, id uuid.UUID) error
}
