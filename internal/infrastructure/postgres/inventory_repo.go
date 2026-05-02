// inventory_repo — adapter Postgres dos repositórios de inventário.
//
// Mantém o padrão de identity_repo: pgx direto, queries inline, mapeamento
// explícito para erros de domínio.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/celinet/sentinel-acs/internal/domain/inventory"
)

// ────────────────────── POPRepository ──────────────────────

type POPRepo struct{ pool Pool }

func NewPOPRepo(pool Pool) *POPRepo { return &POPRepo{pool: pool} }

func (r *POPRepo) Create(ctx context.Context, p *inventory.POP) error {
	const q = `
		INSERT INTO pops (id, name, city, state, is_active)
		VALUES (COALESCE($1, gen_random_uuid()), $2, NULLIF($3,''), NULLIF($4,''), $5)
		RETURNING id, created_at, updated_at`
	var idArg any
	if p.ID != uuid.Nil {
		idArg = p.ID
	}
	return r.pool.QueryRow(ctx, q, idArg, p.Name, p.City, p.State, p.IsActive).
		Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *POPRepo) GetByID(ctx context.Context, id uuid.UUID) (*inventory.POP, error) {
	const q = `SELECT id, name, COALESCE(city,''), COALESCE(state,''), is_active, created_at, updated_at
	             FROM pops WHERE id = $1`
	var p inventory.POP
	err := r.pool.QueryRow(ctx, q, id).Scan(&p.ID, &p.Name, &p.City, &p.State, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrPOPNotFound
	}
	return &p, err
}

func (r *POPRepo) List(ctx context.Context) ([]inventory.POP, error) {
	const q = `SELECT id, name, COALESCE(city,''), COALESCE(state,''), is_active, created_at, updated_at
	             FROM pops ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []inventory.POP
	for rows.Next() {
		var p inventory.POP
		if err := rows.Scan(&p.ID, &p.Name, &p.City, &p.State, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *POPRepo) Update(ctx context.Context, p *inventory.POP) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE pops SET name=$2, city=NULLIF($3,''), state=NULLIF($4,''), is_active=$5 WHERE id=$1`,
		p.ID, p.Name, p.City, p.State, p.IsActive)
	return err
}

func (r *POPRepo) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE pops SET is_active=$2 WHERE id=$1`, id, active)
	return err
}

// ────────────────────── VendorRepository ──────────────────────

type VendorRepo struct{ pool Pool }

func NewVendorRepo(pool Pool) *VendorRepo { return &VendorRepo{pool: pool} }

func (r *VendorRepo) Create(ctx context.Context, v *inventory.Vendor) error {
	const q = `
		INSERT INTO vendors (id, slug, name)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3)
		RETURNING id, created_at`
	var idArg any
	if v.ID != uuid.Nil {
		idArg = v.ID
	}
	err := r.pool.QueryRow(ctx, q, idArg, v.Slug, v.Name).Scan(&v.ID, &v.CreatedAt)
	if isUniqueViolation(err, "vendors_slug_key") || isUniqueViolation(err, "vendors_name_key") {
		return inventory.ErrSlugDuplicate
	}
	return err
}

func (r *VendorRepo) GetBySlug(ctx context.Context, slug string) (*inventory.Vendor, error) {
	const q = `SELECT id, slug, name, created_at FROM vendors WHERE slug = $1`
	var v inventory.Vendor
	err := r.pool.QueryRow(ctx, q, slug).Scan(&v.ID, &v.Slug, &v.Name, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrVendorNotFound
	}
	return &v, err
}

func (r *VendorRepo) GetByID(ctx context.Context, id uuid.UUID) (*inventory.Vendor, error) {
	const q = `SELECT id, slug, name, created_at FROM vendors WHERE id = $1`
	var v inventory.Vendor
	err := r.pool.QueryRow(ctx, q, id).Scan(&v.ID, &v.Slug, &v.Name, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrVendorNotFound
	}
	return &v, err
}

func (r *VendorRepo) List(ctx context.Context) ([]inventory.Vendor, error) {
	const q = `SELECT id, slug, name, created_at FROM vendors ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []inventory.Vendor
	for rows.Next() {
		var v inventory.Vendor
		if err := rows.Scan(&v.ID, &v.Slug, &v.Name, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ────────────────────── DeviceModelRepository ──────────────────────

type DeviceModelRepo struct{ pool Pool }

func NewDeviceModelRepo(pool Pool) *DeviceModelRepo { return &DeviceModelRepo{pool: pool} }

func (r *DeviceModelRepo) Create(ctx context.Context, m *inventory.DeviceModel) error {
	const q = `
		INSERT INTO device_models (id, vendor_id, model, tr_data_model, description)
		VALUES (COALESCE($1, gen_random_uuid()), $2, $3, $4, NULLIF($5,''))
		RETURNING id, created_at`
	var idArg any
	if m.ID != uuid.Nil {
		idArg = m.ID
	}
	err := r.pool.QueryRow(ctx, q, idArg, m.VendorID, m.Model, m.TRDataModel, m.Description).
		Scan(&m.ID, &m.CreatedAt)
	if isUniqueViolation(err, "device_models_vendor_id_model_key") {
		return inventory.ErrModelDuplicate
	}
	return err
}

func (r *DeviceModelRepo) GetByID(ctx context.Context, id uuid.UUID) (*inventory.DeviceModel, error) {
	const q = `SELECT id, vendor_id, model, tr_data_model, COALESCE(description,''), created_at
	             FROM device_models WHERE id = $1`
	var m inventory.DeviceModel
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&m.ID, &m.VendorID, &m.Model, &m.TRDataModel, &m.Description, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrModelNotFound
	}
	return &m, err
}

func (r *DeviceModelRepo) GetByVendorAndModel(ctx context.Context, vendorID uuid.UUID, model string) (*inventory.DeviceModel, error) {
	const q = `SELECT id, vendor_id, model, tr_data_model, COALESCE(description,''), created_at
	             FROM device_models WHERE vendor_id = $1 AND model = $2`
	var m inventory.DeviceModel
	err := r.pool.QueryRow(ctx, q, vendorID, model).
		Scan(&m.ID, &m.VendorID, &m.Model, &m.TRDataModel, &m.Description, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrModelNotFound
	}
	return &m, err
}

func (r *DeviceModelRepo) ListByVendor(ctx context.Context, vendorID uuid.UUID) ([]inventory.DeviceModel, error) {
	const q = `SELECT id, vendor_id, model, tr_data_model, COALESCE(description,''), created_at
	             FROM device_models WHERE vendor_id = $1 ORDER BY model`
	rows, err := r.pool.Query(ctx, q, vendorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []inventory.DeviceModel
	for rows.Next() {
		var m inventory.DeviceModel
		if err := rows.Scan(&m.ID, &m.VendorID, &m.Model, &m.TRDataModel, &m.Description, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ────────────────────── CustomerRepository ──────────────────────

type CustomerRepo struct{ pool Pool }

func NewCustomerRepo(pool Pool) *CustomerRepo { return &CustomerRepo{pool: pool} }

// Upsert insere ou atualiza por (external_source, external_id) — usado pelo
// sync do plugin Voalle (CP-2.5). PPPoE login em conflito é tratado como
// erro de domínio (ErrPPPoEDuplicate).
func (r *CustomerRepo) Upsert(ctx context.Context, c *inventory.Customer) error {
	const q = `
		INSERT INTO customers (
		    id, external_id, external_source, full_name, document, pppoe_login,
		    plan_name, address, status
		) VALUES (
		    COALESCE($1, gen_random_uuid()), NULLIF($2,''), NULLIF($3,''), $4,
		    NULLIF($5,''), NULLIF($6,''), NULLIF($7,''), NULLIF($8,''), $9
		)
		ON CONFLICT (external_source, external_id) WHERE external_source IS NOT NULL AND external_id IS NOT NULL
		DO UPDATE SET
		    full_name   = EXCLUDED.full_name,
		    document    = EXCLUDED.document,
		    pppoe_login = EXCLUDED.pppoe_login,
		    plan_name   = EXCLUDED.plan_name,
		    address     = EXCLUDED.address,
		    status      = EXCLUDED.status
		RETURNING id, created_at, updated_at`
	var idArg any
	if c.ID != uuid.Nil {
		idArg = c.ID
	}
	err := r.pool.QueryRow(ctx, q,
		idArg, c.ExternalID, c.ExternalSource, c.FullName, c.Document,
		c.PPPoELogin, c.PlanName, c.Address, c.Status,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	if isUniqueViolation(err, "customers_pppoe_login_key") {
		return inventory.ErrPPPoEDuplicate
	}
	return err
}

func (r *CustomerRepo) GetByID(ctx context.Context, id uuid.UUID) (*inventory.Customer, error) {
	return r.scan(ctx, `WHERE id = $1`, id)
}

func (r *CustomerRepo) GetByExternal(ctx context.Context, source, externalID string) (*inventory.Customer, error) {
	return r.scan(ctx, `WHERE external_source = $1 AND external_id = $2`, source, externalID)
}

func (r *CustomerRepo) GetByPPPoELogin(ctx context.Context, login string) (*inventory.Customer, error) {
	return r.scan(ctx, `WHERE pppoe_login = $1`, login)
}

func (r *CustomerRepo) scan(ctx context.Context, where string, args ...any) (*inventory.Customer, error) {
	q := `SELECT id, COALESCE(external_id,''), COALESCE(external_source,''), full_name,
	             COALESCE(document,''), COALESCE(pppoe_login,''), COALESCE(plan_name,''),
	             COALESCE(address,''), status, created_at, updated_at
	        FROM customers ` + where
	var c inventory.Customer
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&c.ID, &c.ExternalID, &c.ExternalSource, &c.FullName, &c.Document,
		&c.PPPoELogin, &c.PlanName, &c.Address, &c.Status, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrCustomerNotFound
	}
	return &c, err
}

func (r *CustomerRepo) List(ctx context.Context, p inventory.Page) ([]inventory.Customer, int, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	const q = `
		SELECT id, COALESCE(external_id,''), COALESCE(external_source,''), full_name,
		       COALESCE(document,''), COALESCE(pppoe_login,''), COALESCE(plan_name,''),
		       COALESCE(address,''), status, created_at, updated_at,
		       COUNT(*) OVER() AS total
		  FROM customers ORDER BY full_name LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []inventory.Customer
	var total int
	for rows.Next() {
		var c inventory.Customer
		if err := rows.Scan(
			&c.ID, &c.ExternalID, &c.ExternalSource, &c.FullName, &c.Document,
			&c.PPPoELogin, &c.PlanName, &c.Address, &c.Status, &c.CreatedAt, &c.UpdatedAt, &total,
		); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (r *CustomerRepo) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE customers SET status=$2 WHERE id=$1`, id, status)
	return err
}

// ────────────────────── DeviceRepository ──────────────────────

type DeviceRepo struct{ pool Pool }

func NewDeviceRepo(pool Pool) *DeviceRepo { return &DeviceRepo{pool: pool} }

// Upsert insere ou atualiza por genieacs_id. Sync periódico chama isto
// em massa — única chave dura é o genieacs_id; demais campos podem estar
// vazios na primeira sincronização.
func (r *DeviceRepo) Upsert(ctx context.Context, d *inventory.Device) error {
	const q = `
		INSERT INTO devices (
		    id, genieacs_id, serial_number, mac, oui, model_id, customer_id, pop_id,
		    status, firmware_version, ip_wan, last_inform_at, last_boot_at, tags
		) VALUES (
		    COALESCE($1, gen_random_uuid()), $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''),
		    $6, $7, $8, $9, NULLIF($10,''), $11, $12, $13, $14
		)
		ON CONFLICT (genieacs_id) DO UPDATE SET
		    serial_number    = EXCLUDED.serial_number,
		    mac              = EXCLUDED.mac,
		    oui              = EXCLUDED.oui,
		    model_id         = COALESCE(EXCLUDED.model_id, devices.model_id),
		    customer_id      = COALESCE(EXCLUDED.customer_id, devices.customer_id),
		    pop_id           = COALESCE(EXCLUDED.pop_id, devices.pop_id),
		    status           = EXCLUDED.status,
		    firmware_version = EXCLUDED.firmware_version,
		    ip_wan           = EXCLUDED.ip_wan,
		    last_inform_at   = EXCLUDED.last_inform_at,
		    last_boot_at     = EXCLUDED.last_boot_at,
		    tags             = EXCLUDED.tags
		RETURNING id, created_at, updated_at`
	var idArg any
	if d.ID != uuid.Nil {
		idArg = d.ID
	}

	var ipArg any
	if d.IPWAN != nil {
		ipArg = d.IPWAN.String()
	}

	return r.pool.QueryRow(ctx, q,
		idArg, d.GenieACSID, d.SerialNumber, d.MAC, d.OUI,
		d.ModelID, d.CustomerID, d.POPID, d.Status, d.FirmwareVersion,
		ipArg, d.LastInformAt, d.LastBootAt, d.Tags,
	).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
}

func (r *DeviceRepo) GetByID(ctx context.Context, id uuid.UUID) (*inventory.Device, error) {
	return r.scanOne(ctx, `WHERE id = $1`, id)
}

func (r *DeviceRepo) GetByGenieACSID(ctx context.Context, genieacsID string) (*inventory.Device, error) {
	return r.scanOne(ctx, `WHERE genieacs_id = $1`, genieacsID)
}

const deviceColumns = `
	id, genieacs_id, COALESCE(serial_number,''), COALESCE(mac,''), COALESCE(oui,''),
	model_id, customer_id, pop_id, status, COALESCE(firmware_version,''),
	host(ip_wan), last_inform_at, last_boot_at, tags, created_at, updated_at`

func (r *DeviceRepo) scanOne(ctx context.Context, where string, args ...any) (*inventory.Device, error) {
	q := `SELECT ` + deviceColumns + ` FROM devices ` + where
	var d inventory.Device
	var ipStr *string
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&d.ID, &d.GenieACSID, &d.SerialNumber, &d.MAC, &d.OUI,
		&d.ModelID, &d.CustomerID, &d.POPID, &d.Status, &d.FirmwareVersion,
		&ipStr, &d.LastInformAt, &d.LastBootAt, &d.Tags, &d.CreatedAt, &d.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, inventory.ErrDeviceNotFound
	}
	if ipStr != nil && *ipStr != "" {
		d.IPWAN = net.ParseIP(*ipStr)
	}
	return &d, err
}

// List filtrável + paginado. Search busca em serial_number, mac e genieacs_id.
func (r *DeviceRepo) List(ctx context.Context, f inventory.DeviceFilter, p inventory.Page) ([]inventory.Device, int, error) {
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 50
	}

	var (
		conds []string
		args  []any
		idx   = 1
	)
	add := func(cond string, value any) {
		conds = append(conds, fmt.Sprintf(cond, idx))
		args = append(args, value)
		idx++
	}

	if f.POPID != nil {
		add("pop_id = $%d", *f.POPID)
	}
	if f.ModelID != nil {
		add("model_id = $%d", *f.ModelID)
	}
	if f.VendorID != nil {
		add("model_id IN (SELECT id FROM device_models WHERE vendor_id = $%d)", *f.VendorID)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Tag != "" {
		add("$%d = ANY(tags)", f.Tag)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		// 1 placeholder mas usado 3 vezes — duplicamos pra não confundir o ParseInt da posição.
		conds = append(conds, fmt.Sprintf("(genieacs_id ILIKE $%d OR serial_number ILIKE $%d OR mac ILIKE $%d)", idx, idx, idx))
		args = append(args, "%"+s+"%")
		idx++
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	q := `SELECT ` + deviceColumns + `, COUNT(*) OVER() AS total
	        FROM devices` + where + ` ORDER BY last_inform_at DESC NULLS LAST
	       LIMIT $` + strconv.Itoa(idx) + ` OFFSET $` + strconv.Itoa(idx+1)
	args = append(args, p.Limit, p.Offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("device list: %w", err)
	}
	defer rows.Close()

	var (
		out   []inventory.Device
		total int
	)
	for rows.Next() {
		var d inventory.Device
		var ipStr *string
		if err := rows.Scan(
			&d.ID, &d.GenieACSID, &d.SerialNumber, &d.MAC, &d.OUI,
			&d.ModelID, &d.CustomerID, &d.POPID, &d.Status, &d.FirmwareVersion,
			&ipStr, &d.LastInformAt, &d.LastBootAt, &d.Tags, &d.CreatedAt, &d.UpdatedAt, &total,
		); err != nil {
			return nil, 0, err
		}
		if ipStr != nil && *ipStr != "" {
			d.IPWAN = net.ParseIP(*ipStr)
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

func (r *DeviceRepo) LinkCustomer(ctx context.Context, deviceID uuid.UUID, customerID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE devices SET customer_id = $2 WHERE id = $1`, deviceID, customerID)
	return err
}

// MarkInform atualiza os campos de "última atividade" sem tocar nos vínculos.
// Status é recalculado pelo caller (sync job sabe o threshold configurado).
func (r *DeviceRepo) MarkInform(ctx context.Context, genieacsID string, lastInform time.Time, lastBoot *time.Time, fwVersion string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE devices SET
		    last_inform_at   = $2,
		    last_boot_at     = COALESCE($3, last_boot_at),
		    firmware_version = COALESCE(NULLIF($4,''), firmware_version),
		    status           = 'online'
		 WHERE genieacs_id = $1`, genieacsID, lastInform, lastBoot, fwVersion)
	return err
}
