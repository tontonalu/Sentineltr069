package provisioning

import (
	"context"
	"fmt"
	"time"

	prov "github.com/celinet/sentinel-acs/internal/domain/provisioning"
	"github.com/celinet/sentinel-acs/internal/infrastructure/genieacs"
)

// Syncer aplica a tr069_provisioning_config.Config no GenieACS criando/
// atualizando 1 provision (script JS) + 1 preset que aponta pro provision.
//
// Roda como side-effect do botão "Sincronizar" em /settings/provisioning.
// Idempotente: pode ser chamado N vezes sem efeito colateral além do
// last_synced_at.
type Syncer struct {
	repo prov.ConfigRepository
	acs  *genieacs.Client
}

func NewSyncer(repo prov.ConfigRepository, acs *genieacs.Client) *Syncer {
	return &Syncer{repo: repo, acs: acs}
}

// Sync executa upsert no GenieACS e atualiza last_synced_at/last_sync_error.
// Retorna o erro original (não envolvido) para o handler exibir.
func (s *Syncer) Sync(ctx context.Context) error {
	cfg, err := s.repo.Get(ctx)
	if err != nil {
		return fmt.Errorf("ler config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	script := buildProvisionScript(cfg)
	if err := s.acs.UpsertProvision(ctx, cfg.PresetName, script); err != nil {
		s.recordFailure(ctx, err)
		return err
	}

	preset := genieacs.PresetBody{
		Weight:       0,
		Precondition: "",
		Configurations: []map[string]interface{}{
			{"type": "provision", "name": cfg.PresetName},
		},
	}
	if err := s.acs.UpsertPreset(ctx, cfg.PresetName, preset); err != nil {
		s.recordFailure(ctx, err)
		return err
	}

	if err := s.repo.MarkSynced(ctx, time.Now().UTC(), ""); err != nil {
		return fmt.Errorf("registrar sync: %w", err)
	}
	return nil
}

func (s *Syncer) recordFailure(ctx context.Context, syncErr error) {
	_ = s.repo.MarkSynced(ctx, time.Now().UTC(), syncErr.Error())
}

// buildProvisionScript monta o JS aplicado pelo GenieACS em cada sessão CWMP.
// Usa Device.* (TR-181); o GenieACS normaliza CPEs TR-098/IGD via aliases
// internos para a maioria dos vendors suportados (Huawei, ZTE, Intelbras…).
//
// `now` é a built-in do runtime do GenieACS — força refresh imediato do
// path declarado.
func buildProvisionScript(c *prov.Config) string {
	script := fmt.Sprintf(`
const url = %q;
const interval = %d;
declare("Device.ManagementServer.URL",                    {value: now}, {value: url});
declare("Device.ManagementServer.PeriodicInformEnable",   {value: now}, {value: true});
declare("Device.ManagementServer.PeriodicInformInterval", {value: now}, {value: interval});
`, c.CWMPUrl, c.InformIntervalS)

	if c.DefaultCRUser != "" {
		script += fmt.Sprintf(`declare("Device.ManagementServer.ConnectionRequestUsername", {value: now}, {value: %q});`+"\n", c.DefaultCRUser)
	}
	if c.DefaultCRPass != "" {
		script += fmt.Sprintf(`declare("Device.ManagementServer.ConnectionRequestPassword", {value: now}, {value: %q});`+"\n", c.DefaultCRPass)
	}
	return script
}
