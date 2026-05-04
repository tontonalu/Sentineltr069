// presets.go — operações sobre /presets, /provisions do NBI.
//
// O GenieACS modela "configuration intent" em duas camadas que precisam casar:
//   - Provision: script JS executado durante a sessão CWMP de cada CPE.
//     Acessa o objeto `args` (parametrizável) e chama declare(...) para
//     definir paths TR-069 (URL do ACS, intervalo de Inform, credenciais).
//   - Preset:    regra que diz "qual provision rodar e quando" — combina
//     precondition (filtro de CPEs) com a lista de provisions a aplicar.
//
// O NBI expõe PUT /presets/{name} e PUT /provisions/{name} como upsert
// idempotente — chamamos repetidamente para manter o estado em sync com a
// tabela tr069_provisioning_config.
package genieacs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// PresetBody — payload aceito por PUT /presets/{name}.
//
// Configurations é a lista ordenada de provisions a executar. O formato
// canônico do NBI é {"type":"provision", "name":"<provision-name>",
// "args":[...]}.  Precondition é um filtro Mongo-like sobre o device;
// string vazia = aplica em todo CPE.
type PresetBody struct {
	Channel        string                   `json:"channel,omitempty"`
	Weight         int                      `json:"weight,omitempty"`
	Precondition   string                   `json:"precondition"`
	Configurations []map[string]interface{} `json:"configurations"`
}

// UpsertProvision cria/atualiza um provision (script JS executado pelo CWMP).
// `script` é o corpo do script — deve ser JS válido aceito pelo runtime do
// GenieACS (ex.: usa `declare("Path", attrs, values)` e `args[N]`).
func (c *Client) UpsertProvision(ctx context.Context, name, script string) error {
	if name == "" {
		return fmt.Errorf("genieacs: provision name vazio")
	}
	u := fmt.Sprintf("%s/provisions/%s", c.baseURL, url.PathEscape(name))

	resp, err := c.do(ctx, http.MethodPut, u, bytes.NewReader([]byte(script)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return errFromResponse("upsert provision", resp)
	}
	return nil
}

// UpsertPreset cria/atualiza um preset.
func (c *Client) UpsertPreset(ctx context.Context, name string, body PresetBody) error {
	if name == "" {
		return fmt.Errorf("genieacs: preset name vazio")
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("genieacs: marshal preset: %w", err)
	}
	u := fmt.Sprintf("%s/presets/%s", c.baseURL, url.PathEscape(name))

	resp, err := c.do(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return errFromResponse("upsert preset", resp)
	}
	return nil
}

// DeletePreset remove um preset por nome — usado em rollback ou rename.
func (c *Client) DeletePreset(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("genieacs: preset name vazio")
	}
	u := fmt.Sprintf("%s/presets/%s", c.baseURL, url.PathEscape(name))
	resp, err := c.do(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return errFromResponse("delete preset", resp)
	}
	return nil
}
