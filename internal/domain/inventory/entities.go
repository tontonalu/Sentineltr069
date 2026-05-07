// Package inventory contém o modelo de domínio de POPs, vendors, modelos,
// customers e devices (CPEs). Camada pura — sem deps de DB ou HTTP.
package inventory

import (
	"net"
	"time"

	"github.com/google/uuid"
)

// Status enumerado para devices.
const (
	StatusUnknown   = "unknown"
	StatusOnline    = "online"
	StatusOffline   = "offline"
	StatusNeverSeen = "never_seen"
)

// Status enumerado para customers.
const (
	CustomerActive    = "active"
	CustomerSuspended = "suspended"
	CustomerCancelled = "cancelled"
)

// TR data models suportados.
const (
	TR098 = "tr098"
	TR181 = "tr181"
)

// POP — Point of Presence (filial física do provedor).
type POP struct {
	ID        uuid.UUID
	Name      string
	City      string
	State     string
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Vendor — fabricante (Huawei, ZTE, ...). Slug é usado em templates e UI.
type Vendor struct {
	ID        uuid.UUID
	Slug      string
	Name      string
	CreatedAt time.Time
}

// DeviceModel — modelo específico de um vendor, com seu data model TR.
type DeviceModel struct {
	ID          uuid.UUID
	VendorID    uuid.UUID
	Model       string
	TRDataModel string // TR098 ou TR181
	Description string
	CreatedAt   time.Time
}

// IsTR098 / IsTR181 — usado pelos templates pra escolher path correto.
func (m DeviceModel) IsTR098() bool { return m.TRDataModel == TR098 }
func (m DeviceModel) IsTR181() bool { return m.TRDataModel == TR181 }

// Customer — espelho do cliente no ERP de origem (Voalle, IXC, etc).
// pppoe_login é a chave funcional para casar com Device.
type Customer struct {
	ID             uuid.UUID
	ExternalID     string
	ExternalSource string // 'voalle' | 'ixc' | 'manual'
	FullName       string
	Document       string
	PPPoELogin     string
	PlanName       string
	Address        string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// IsActive devolve true se o customer está apto a operar (não cancelado/suspenso).
func (c Customer) IsActive() bool { return c.Status == CustomerActive }

// Device — CPE espelhado do GenieACS, com vínculos para o domínio de negócio.
// genieacs_id é o _id no Mongo do GenieACS — chave dura de sincronização.
type Device struct {
	ID                 uuid.UUID
	GenieACSID         string
	SerialNumber       string
	MAC                string
	OUI                string
	ModelID            *uuid.UUID
	CustomerID         *uuid.UUID
	POPID              *uuid.UUID
	Status             string
	FirmwareVersion    string
	IPWAN              net.IP
	PPPoELogin         string // login PPPoE lido da árvore TR-069 (sem ligação com Customer)
	LastInformAt       *time.Time
	LastBootAt         *time.Time
	Tags               []string
	IsHomologationLab  bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ComputeStatus deriva online/offline a partir de last_inform_at e um threshold.
// Útil para o sync job e para a UI.
//
// Convenções:
//   - last_inform_at == nil  → never_seen
//   - last_inform_at < now-threshold → offline
//   - caso contrário → online
func ComputeStatus(lastInform *time.Time, now time.Time, threshold time.Duration) string {
	if lastInform == nil {
		return StatusNeverSeen
	}
	if now.Sub(*lastInform) > threshold {
		return StatusOffline
	}
	return StatusOnline
}
