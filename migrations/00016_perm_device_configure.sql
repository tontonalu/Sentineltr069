-- +goose Up
-- +goose StatementBegin

-- ──────────────── Permissão device.configure ────────────────
-- Gate da edição inline de campos individuais a partir da página
-- /devices/{id} (aba Wireless, Internet, LAN, etc). Cada submit cria um
-- provisioning_job single-parameter, então o usuário precisa explicitamente
-- desta permissão — separa "ver dados do CPE" de "alterá-los do dashboard".

INSERT INTO permissions (resource, action, description) VALUES
    ('device', 'configure', 'Editar configurações individuais do CPE (SSID, senha, PPPoE, etc) a partir da página de device')
ON CONFLICT (resource, action) DO NOTHING;

-- superadmin recebe tudo (já é seed em 00001 via roles, mas reforçamos
-- o vínculo ao novo permission_id explicitamente).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin'
   AND p.resource = 'device' AND p.action = 'configure'
ON CONFLICT DO NOTHING;

-- operator também recebe — quem opera o NOC tipicamente troca senha de Wi-Fi
-- e PPPoE diariamente. viewer NÃO recebe (mantém read-only).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND p.resource = 'device' AND p.action = 'configure'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM permissions WHERE resource = 'device' AND action = 'configure';

-- +goose StatementEnd
