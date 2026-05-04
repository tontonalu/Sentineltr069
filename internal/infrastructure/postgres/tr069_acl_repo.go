// tr069_acl_repo — adapter Postgres da lista de CIDRs autorizados na 7547.
package postgres

import (
	"context"
	"errors"
	"net/netip"

	"github.com/google/uuid"

	"github.com/celinet/sentinel-acs/internal/domain/provisioning"
)

type TR069ACLRepo struct{ pool Pool }

func NewTR069ACLRepo(pool Pool) *TR069ACLRepo { return &TR069ACLRepo{pool: pool} }

func (r *TR069ACLRepo) List(ctx context.Context) ([]provisioning.ACLCIDR, error) {
	const q = `
		SELECT id, cidr::text, description, created_by, created_at
		  FROM tr069_acl_cidrs
		 ORDER BY cidr ASC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []provisioning.ACLCIDR
	for rows.Next() {
		var (
			e       provisioning.ACLCIDR
			cidrStr string
		)
		if err := rows.Scan(&e.ID, &cidrStr, &e.Description, &e.CreatedBy, &e.CreatedAt); err != nil {
			return nil, err
		}
		p, err := netip.ParsePrefix(cidrStr)
		if err != nil {
			return nil, err
		}
		e.CIDR = p
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *TR069ACLRepo) Create(ctx context.Context, e *provisioning.ACLCIDR) error {
	const q = `
		INSERT INTO tr069_acl_cidrs (cidr, description, created_by)
		VALUES ($1::cidr, $2, $3)
		RETURNING id, created_at`
	err := r.pool.QueryRow(ctx, q, e.CIDR.String(), e.Description, e.CreatedBy).
		Scan(&e.ID, &e.CreatedAt)
	if isUniqueViolation(err, "tr069_acl_cidrs_cidr_key") {
		return provisioning.ErrCIDRDuplicate
	}
	return err
}

func (r *TR069ACLRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tr069_acl_cidrs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return provisioning.ErrCIDRNotFound
	}
	return nil
}

// Compile-time assertion: o repo satisfaz a interface do domínio.
var _ provisioning.ACLRepository = (*TR069ACLRepo)(nil)

// errAssertion silencia warning se for incluído em build tag futuro.
var _ = errors.Is
