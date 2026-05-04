package provisioning

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Config — registro singleton (id sempre 1) com a configuração TR-069/CWMP que
// o syncer aplica em todo CPE via preset+provision do GenieACS.
//
// CWMPUrl é a URL que vai dentro de Device.ManagementServer.URL — é a porta
// 7547 pública alcançável pelos CPEs, não a NBI 7557 que o backend consome.
type Config struct {
	CWMPUrl         string
	InformIntervalS int
	DefaultCRUser   string
	DefaultCRPass   string
	PresetName      string
	LastSyncedAt    *time.Time
	LastSyncError   string
	UpdatedAt       time.Time
	UpdatedBy       *uuid.UUID
}

// Validate — sanity checks antes de persistir/sincronizar.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.CWMPUrl) == "" {
		return ErrCWMPUrlRequired
	}
	u := strings.ToLower(c.CWMPUrl)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ErrCWMPUrlScheme
	}
	if c.InformIntervalS < 60 || c.InformIntervalS > 86400 {
		return ErrInformIntervalRange
	}
	if strings.TrimSpace(c.PresetName) == "" {
		return ErrPresetNameRequired
	}
	return nil
}

// ConfigRepository — singleton: Get devolve sempre o mesmo registro (id=1),
// criado pela migration 00006. Update sobrescreve os campos editáveis.
type ConfigRepository interface {
	Get(ctx context.Context) (*Config, error)
	Update(ctx context.Context, c *Config) error
	MarkSynced(ctx context.Context, syncedAt time.Time, syncErr string) error
}

var (
	ErrCWMPUrlRequired     = errors.New("provisioning: CWMP URL é obrigatória")
	ErrCWMPUrlScheme       = errors.New("provisioning: CWMP URL precisa começar com http:// ou https://")
	ErrInformIntervalRange = errors.New("provisioning: intervalo de Inform deve estar entre 60 e 86400 segundos")
	ErrPresetNameRequired  = errors.New("provisioning: nome do preset é obrigatório")
)
