// provisioning_config_repo — adapter Postgres do singleton tr069_provisioning_config.
package postgres

import (
	"context"
	"time"

	"github.com/celinet/sentinel-acs/internal/domain/provisioning"
)

type ProvisioningConfigRepo struct{ pool Pool }

func NewProvisioningConfigRepo(pool Pool) *ProvisioningConfigRepo {
	return &ProvisioningConfigRepo{pool: pool}
}

func (r *ProvisioningConfigRepo) Get(ctx context.Context) (*provisioning.Config, error) {
	const q = `
		SELECT cwmp_url, inform_interval_s, default_cr_user, default_cr_pass,
		       preset_name, last_synced_at, last_sync_error, updated_at, updated_by
		  FROM tr069_provisioning_config
		 WHERE id = 1`
	var c provisioning.Config
	err := r.pool.QueryRow(ctx, q).Scan(
		&c.CWMPUrl, &c.InformIntervalS, &c.DefaultCRUser, &c.DefaultCRPass,
		&c.PresetName, &c.LastSyncedAt, &c.LastSyncError, &c.UpdatedAt, &c.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ProvisioningConfigRepo) Update(ctx context.Context, c *provisioning.Config) error {
	const q = `
		UPDATE tr069_provisioning_config SET
		    cwmp_url          = $1,
		    inform_interval_s = $2,
		    default_cr_user   = $3,
		    default_cr_pass   = $4,
		    preset_name       = $5,
		    updated_by        = $6
		 WHERE id = 1`
	_, err := r.pool.Exec(ctx, q,
		c.CWMPUrl, c.InformIntervalS, c.DefaultCRUser, c.DefaultCRPass,
		c.PresetName, c.UpdatedBy,
	)
	return err
}

func (r *ProvisioningConfigRepo) MarkSynced(ctx context.Context, syncedAt time.Time, syncErr string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE tr069_provisioning_config
		    SET last_synced_at = $1, last_sync_error = $2
		  WHERE id = 1`,
		syncedAt, syncErr)
	return err
}
