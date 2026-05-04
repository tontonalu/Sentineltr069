-- +goose Up
-- +goose StatementBegin

-- Permission para excluir devices (do Postgres + ACS upstream).
INSERT INTO permissions (resource, action, description) VALUES
    ('device', 'delete', 'Excluir device do inventário e do ACS upstream')
ON CONFLICT (resource, action) DO NOTHING;

-- superadmin: recebe a nova permission (a regra catch-all da 00001 já cobre,
-- mas em bancos existentes a tabela já está populada — re-aplicar é seguro).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'superadmin' AND p.resource = 'device' AND p.action = 'delete'
ON CONFLICT DO NOTHING;

-- operator: também recebe (operator já tem todas as actions de device).
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id
  FROM roles r, permissions p
 WHERE r.name = 'operator' AND p.resource = 'device' AND p.action = 'delete'
ON CONFLICT DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM permissions WHERE resource = 'device' AND action = 'delete';

-- +goose StatementEnd
