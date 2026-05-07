-- +goose Up
-- +goose StatementBegin

-- Persiste o login PPPoE lido pela sondagem do TR-069 diretamente no device.
-- Antes só salvávamos o customer_id (resolvido por lookup em customers); para
-- ISPs que ainda não têm customers sincronizados via ERP, o login era visível
-- só na aba Internet (e a listagem mostrava "—"). Coluna explícita garante
-- que a UI sempre tenha algo útil para identificar o assinante.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS pppoe_login TEXT;

-- Index funcional para search/filter por PPPoE no /devices.
CREATE INDEX IF NOT EXISTS idx_devices_pppoe_login_lower
    ON devices (LOWER(pppoe_login))
    WHERE pppoe_login IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_devices_pppoe_login_lower;
ALTER TABLE devices DROP COLUMN IF EXISTS pppoe_login;

-- +goose StatementEnd
