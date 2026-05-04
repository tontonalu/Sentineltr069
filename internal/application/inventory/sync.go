// Package inventory contém os casos de uso de inventário — sync com GenieACS,
// linkagem a customers, etc.
package inventory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "github.com/celinet/sentinel-acs/internal/domain/inventory"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
	"github.com/celinet/sentinel-acs/internal/platform/logger"
)

// Paths canônicos lidos no sync. Usamos virtual params primeiro (criados no
// Pré-req-A da Fase 2); se não existirem, caímos para TR-098 ou TR-181.
//
// Manter como var (e não const) para permitir override em testes.
var SyncProjection = []string{
	// Metadados GenieACS
	"_id",
	"_lastInform",
	"_lastBoot",
	"_tags",

	// DeviceID — sempre presente, vem do Inform
	"DeviceID.Manufacturer",
	"DeviceID.OUI",
	"DeviceID.ProductClass",
	"DeviceID.SerialNumber",

	// TR-098 (legado)
	"InternetGatewayDevice.DeviceInfo.Manufacturer",
	"InternetGatewayDevice.DeviceInfo.ModelName",
	"InternetGatewayDevice.DeviceInfo.SoftwareVersion",
	"InternetGatewayDevice.DeviceInfo.SerialNumber",
	"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username",
	"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress",
	"InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.1.MACAddress",

	// TR-181 (atual)
	"Device.DeviceInfo.Manufacturer",
	"Device.DeviceInfo.ModelName",
	"Device.DeviceInfo.SoftwareVersion",
	"Device.DeviceInfo.SerialNumber",
	"Device.PPP.Interface.1.Username",
	"Device.IP.Interface.1.IPv4Address.1.IPAddress",

	// Virtual Parameters (canônicos definidos no GenieACS)
	"VirtualParameters.Manufacturer",
	"VirtualParameters.ModelName",
	"VirtualParameters.SoftwareVersion",
	"VirtualParameters.SerialNumber",
	"VirtualParameters.MAC",
	"VirtualParameters.PPPoEUsername",
	"VirtualParameters.WANIP",
}

// SyncService coordena a sincronização periódica com GenieACS NBI.
//
// Responsabilidades:
//   - Buscar todos os devices do NBI (ou os recentes)
//   - Mapear vendor/modelo de cada um (auto-criando se ainda não existir)
//   - Linkar customer via PPPoE login
//   - Upsert em devices + recálculo de status
type SyncService struct {
	devices   domain.DeviceRepository
	customers domain.CustomerRepository
	vendors   domain.VendorRepository
	models    domain.DeviceModelRepository
	genie     *genieacs.Client
	threshold time.Duration // limite para considerar device offline
}

func NewSyncService(
	d domain.DeviceRepository,
	c domain.CustomerRepository,
	v domain.VendorRepository,
	m domain.DeviceModelRepository,
	g *genieacs.Client,
	threshold time.Duration,
) *SyncService {
	if threshold <= 0 {
		threshold = 30 * time.Minute
	}
	return &SyncService{
		devices: d, customers: c, vendors: v, models: m,
		genie: g, threshold: threshold,
	}
}

// SyncResult traz métricas para logging/telemetria.
type SyncResult struct {
	Total          int
	Created        int
	Updated        int
	Errors         int
	LinkedCustomer int
	Duration       time.Duration
}

// Tick executa um ciclo completo de sync. Idempotente — pode ser chamado
// quantas vezes o caller quiser.
func (s *SyncService) Tick(ctx context.Context) (SyncResult, error) {
	start := time.Now()
	log := logger.FromContext(ctx)

	devices, err := s.genie.QueryDevices(ctx, genieacs.Query{
		Projection: SyncProjection,
	})
	if err != nil {
		return SyncResult{Duration: time.Since(start)}, fmt.Errorf("sync: query genieacs: %w", err)
	}

	res := SyncResult{Total: len(devices)}

	// Caches por execução: vendors/models já criados não são re-buscados.
	vendorCache := map[string]uuid.UUID{}
	modelCache := map[string]uuid.UUID{}

	for _, d := range devices {
		if err := s.syncDevice(ctx, d, vendorCache, modelCache, &res); err != nil {
			res.Errors++
			log.Warn("sync device failed", "device_id", d.ID, "err", err)
		}
	}

	// Reconciliar status: devices não tocados nesta volta podem virar offline.
	// Para simplicidade nesta primeira versão, deixamos um job separado fazer
	// isso (ou recalculamos no GET). Aqui só atualizamos o que veio do NBI.

	res.Duration = time.Since(start)
	log.Info("sync done",
		"total", res.Total, "created", res.Created, "updated", res.Updated,
		"errors", res.Errors, "linked", res.LinkedCustomer, "duration_ms", res.Duration.Milliseconds())
	return res, nil
}

func (s *SyncService) syncDevice(
	ctx context.Context,
	d genieacs.Device,
	vendorCache, modelCache map[string]uuid.UUID,
	res *SyncResult,
) error {
	if d.ID == "" {
		return errors.New("genieacs id vazio")
	}

	// Extração com fallback (virtual → DeviceID → TR-098 → TR-181)
	manufacturer := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.Manufacturer",
		"DeviceID.Manufacturer",
		"InternetGatewayDevice.DeviceInfo.Manufacturer",
		"Device.DeviceInfo.Manufacturer",
	)

	modelName := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.ModelName",
		"DeviceID.ProductClass",
		"InternetGatewayDevice.DeviceInfo.ModelName",
		"Device.DeviceInfo.ModelName",
	)

	serial := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.SerialNumber",
		"DeviceID.SerialNumber",
		"InternetGatewayDevice.DeviceInfo.SerialNumber",
		"Device.DeviceInfo.SerialNumber",
	)

	oui := genieacs.FirstNonEmpty(d.Raw, "DeviceID.OUI")

	fwVersion := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.SoftwareVersion",
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion",
		"Device.DeviceInfo.SoftwareVersion",
	)

	macAddr := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.MAC",
		"InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.1.MACAddress",
	)

	pppoeLogin := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.PPPoEUsername",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username",
		"Device.PPP.Interface.1.Username",
	)

	wanIP := genieacs.FirstNonEmpty(d.Raw,
		"VirtualParameters.WANIP",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress",
		"Device.IP.Interface.1.IPv4Address.1.IPAddress",
	)

	// Detecta data model: se houver qualquer campo TR-181, é tr181; senão tr098.
	trModel := domain.TR098
	if genieacs.FirstNonEmpty(d.Raw,
		"Device.DeviceInfo.Manufacturer",
		"Device.DeviceInfo.ModelName",
	) != "" {
		trModel = domain.TR181
	}

	// Resolve vendor/modelo
	var modelID *uuid.UUID
	if manufacturer != "" && modelName != "" {
		vID, err := s.resolveVendor(ctx, manufacturer, vendorCache)
		if err != nil {
			return fmt.Errorf("vendor %q: %w", manufacturer, err)
		}
		mID, err := s.resolveModel(ctx, vID, modelName, trModel, modelCache)
		if err != nil {
			return fmt.Errorf("model %q/%q: %w", manufacturer, modelName, err)
		}
		modelID = &mID
	}

	// Customer por PPPoE
	var customerID *uuid.UUID
	if pppoeLogin != "" {
		if c, err := s.customers.GetByPPPoELogin(ctx, pppoeLogin); err == nil {
			customerID = &c.ID
			res.LinkedCustomer++
		} else if !errors.Is(err, domain.ErrCustomerNotFound) {
			return fmt.Errorf("lookup customer %q: %w", pppoeLogin, err)
		}
	}

	// Status & timestamps
	var lastInform *time.Time
	if !d.LastInform.IsZero() {
		li := d.LastInform
		lastInform = &li
	}
	status := domain.ComputeStatus(lastInform, time.Now(), s.threshold)

	var ipWAN net.IP
	if wanIP != "" {
		ipWAN = net.ParseIP(wanIP)
	}

	// Detecta novo/existente para preservar vínculos manuais (POP, customer)
	isNew := false
	var preservePOP *uuid.UUID
	var preserveCustomer *uuid.UUID
	existing, err := s.devices.GetByGenieACSID(ctx, d.ID)
	switch {
	case errors.Is(err, domain.ErrDeviceNotFound):
		isNew = true
	case err != nil:
		return fmt.Errorf("lookup existing: %w", err)
	default:
		preservePOP = existing.POPID
		if customerID == nil {
			preserveCustomer = existing.CustomerID
		}
	}

	dev := &domain.Device{
		GenieACSID:      d.ID,
		SerialNumber:    serial,
		MAC:             macAddr,
		OUI:             oui,
		ModelID:         modelID,
		CustomerID:      coalesceUUID(customerID, preserveCustomer),
		POPID:           preservePOP,
		Status:          status,
		FirmwareVersion: fwVersion,
		IPWAN:           ipWAN,
		LastInformAt:    lastInform,
		LastBootAt:      d.LastBoot,
		Tags:            d.Tags,
	}

	if err := s.devices.Upsert(ctx, dev); err != nil {
		return fmt.Errorf("upsert: %w", err)
	}

	if isNew {
		res.Created++
	} else {
		res.Updated++
	}
	return nil
}

// DeleteDevice remove um device do ACS upstream e do Postgres. Sequência
// importante: GenieACS-primeiro, Postgres-depois. Se o app crashar entre os
// dois passos, o device some do GenieACS mas continua no Postgres — o
// próximo Tick não re-cria (não está mais no NBI) e o usuário pode reexcluir
// pelo botão sem ver erro. Inversão deixaria órfão no GenieACS.
//
// 404 do GenieACS é ignorado: o registro pode já ter sido apagado de lá em
// outra janela; nosso objetivo é convergir.
func (s *SyncService) DeleteDevice(ctx context.Context, deviceID uuid.UUID) error {
	dev, err := s.devices.GetByID(ctx, deviceID)
	if err != nil {
		return err
	}

	if err := s.genie.DeleteDevice(ctx, dev.GenieACSID); err != nil {
		var apiErr *genieacs.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			return fmt.Errorf("delete genieacs: %w", err)
		}
	}

	if err := s.devices.Delete(ctx, deviceID); err != nil {
		return fmt.Errorf("delete postgres: %w", err)
	}

	logger.FromContext(ctx).Info("device deleted",
		"device_id", deviceID, "genieacs_id", dev.GenieACSID)
	return nil
}

// resolveVendor faz find-or-create cacheado por slug.
func (s *SyncService) resolveVendor(ctx context.Context, name string, cache map[string]uuid.UUID) (uuid.UUID, error) {
	slug := slugify(name)
	if id, ok := cache[slug]; ok {
		return id, nil
	}

	v, err := s.vendors.GetBySlug(ctx, slug)
	if err == nil {
		cache[slug] = v.ID
		return v.ID, nil
	}
	if !errors.Is(err, domain.ErrVendorNotFound) {
		return uuid.Nil, err
	}

	newV := &domain.Vendor{Slug: slug, Name: name}
	if err := s.vendors.Create(ctx, newV); err != nil {
		// race condition entre instâncias: outra criou primeiro
		if errors.Is(err, domain.ErrSlugDuplicate) {
			if v2, err2 := s.vendors.GetBySlug(ctx, slug); err2 == nil {
				cache[slug] = v2.ID
				return v2.ID, nil
			}
		}
		return uuid.Nil, err
	}
	cache[slug] = newV.ID
	return newV.ID, nil
}

func (s *SyncService) resolveModel(ctx context.Context, vendorID uuid.UUID, model, trModel string, cache map[string]uuid.UUID) (uuid.UUID, error) {
	key := vendorID.String() + "|" + model
	if id, ok := cache[key]; ok {
		return id, nil
	}

	m, err := s.models.GetByVendorAndModel(ctx, vendorID, model)
	if err == nil {
		cache[key] = m.ID
		return m.ID, nil
	}
	if !errors.Is(err, domain.ErrModelNotFound) {
		return uuid.Nil, err
	}

	newM := &domain.DeviceModel{
		VendorID:    vendorID,
		Model:       model,
		TRDataModel: trModel,
	}
	if err := s.models.Create(ctx, newM); err != nil {
		if errors.Is(err, domain.ErrModelDuplicate) {
			if m2, err2 := s.models.GetByVendorAndModel(ctx, vendorID, model); err2 == nil {
				cache[key] = m2.ID
				return m2.ID, nil
			}
		}
		return uuid.Nil, err
	}
	cache[key] = newM.ID
	return newM.ID, nil
}

// slugify normaliza um nome de vendor para uso como slug.
// Não pretende ser perfeito — só evita caracteres problemáticos em URL/CSS.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prev := byte('-')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			b.WriteByte(c)
			prev = c
		case c == '-' || c == ' ' || c == '_' || c == '.':
			if prev != '-' {
				b.WriteByte('-')
				prev = '-'
			}
		}
	}
	out := b.String()
	out = strings.Trim(out, "-")
	if out == "" {
		out = "unknown"
	}
	return out
}

func coalesceUUID(a, b *uuid.UUID) *uuid.UUID {
	if a != nil {
		return a
	}
	return b
}
