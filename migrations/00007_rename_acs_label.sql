-- +goose Up
-- +goose StatementBegin

-- Atualiza a descrição da permission para usar "ACS upstream" em vez de
-- "GenieACS" — alinhado à decisão de não fingerprintar o engine na UI.
UPDATE permissions
   SET description = 'Editar config TR-069 e sincronizar com ACS upstream'
 WHERE resource = 'provisioning_config'
   AND action   = 'manage';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

UPDATE permissions
   SET description = 'Editar config TR-069 e sincronizar com GenieACS'
 WHERE resource = 'provisioning_config'
   AND action   = 'manage';

-- +goose StatementEnd
