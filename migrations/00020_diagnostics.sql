-- +goose Up
-- +goose StatementBegin

-- ──────────────── diagnostics ────────────────
-- Carrega 1 linha por execução de teste (ping/traceroute) disparado pela UI.
-- O fluxo:
--   1. operador submete formulário → INSERT status='requested', request=jsonb
--      (host, count, size, timeout_ms, etc), deadline=NOW()+timeout_ms+30s
--   2. application/diagnostics.Service envia setParameterValues pro CPE
--      ativando IPPingDiagnostics/TraceRouteDiagnostics, status muda pra
--      'running' quando o GenieACS aceita a task
--   3. worker poller (1× a cada 10s) lê tree do CPE; quando
--      DiagnosticsState=Complete, escreve `result` jsonb e status='complete'
--   4. UI faz polling HTMX em /devices/{id}/diagnostics/{id}; quando
--      status sai de 'running' o fragmento mostra resultado final.
--
-- request/result são JSONB pra acomodar tipos diferentes (ping={host,count,
-- size_bytes,timeout_ms} vs traceroute={host,max_hops,size_bytes}; result
-- ping={success_count,failure_count,avg_ms,min_ms,max_ms} vs traceroute=
-- {hops:[{ip,host_name,rtts_ms:[]}]}). Nenhum schema rígido pra evitar
-- migrations toda vez que mudar um vendor.
CREATE TABLE diagnostics (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id     UUID        NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    type          TEXT        NOT NULL CHECK (type IN ('ping', 'traceroute')),
    status        TEXT        NOT NULL DEFAULT 'requested'
                              CHECK (status IN ('requested', 'running', 'complete', 'error', 'timeout')),
    request       JSONB       NOT NULL,
    result        JSONB,
    error         TEXT,
    requested_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    deadline      TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_diagnostics_device_at
    ON diagnostics (device_id, requested_at DESC);

-- Index parcial pra o poller — varre só status ativos. Quando o número de
-- diagnostics históricos cresce muito, esse índice mantém varredura O(1) em
-- cima da fila ativa.
CREATE INDEX idx_diagnostics_active
    ON diagnostics (deadline)
    WHERE status IN ('requested', 'running');

-- ──────────────── Permissão device.diagnose ────────────────
-- Necessária pra disparar testes — separada de device.configure porque um
-- diagnóstico é uma operação read-only sob o ponto de vista do CPE
-- (consulta latência/rota), enquanto configure escreve params persistentes.
INSERT INTO permissions (resource, action, description) VALUES
    ('device', 'diagnose', 'Disparar testes remotos (ping, traceroute) no CPE')
ON CONFLICT (resource, action) DO NOTHING;

-- superadmin já recebe tudo via seed inicial; reforçamos vínculo explícito.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
 WHERE r.name = 'superadmin'
   AND p.resource = 'device' AND p.action = 'diagnose'
ON CONFLICT DO NOTHING;

-- operator também recebe — diagnóstico é parte do workflow de NOC.
INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r, permissions p
 WHERE r.name = 'operator'
   AND p.resource = 'device' AND p.action = 'diagnose'
ON CONFLICT DO NOTHING;

-- viewer NÃO recebe — disparar diagnóstico gera carga real no CPE/GenieACS.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS diagnostics;

DELETE FROM permissions WHERE resource = 'device' AND action = 'diagnose';

-- +goose StatementEnd
