-- +goose Up
-- +goose StatementBegin

-- ──────────────── Profile homologation lock ────────────────
-- Marca config_profiles gerados via wizard de homologação como imutáveis.
-- Quando is_homologated = TRUE, qualquer Update no service de templates
-- é rejeitado com ErrProfileImmutable — versões antigas viram registro
-- histórico permanente ao qual ApplyBulk pode se referir com segurança.
--
-- Nova homologação para o mesmo modelo cria um Profile NOVO (ex.: nome
-- termina em _v2), preservando o anterior. is_active continua editável
-- (admin pode aposentar uma versão sem mexer no conteúdo).

ALTER TABLE config_profiles
    ADD COLUMN is_homologated BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill: profiles já vinculados a uma model_homologation com status
-- 'homologated' são marcados como homologados (foram criados antes desta
-- migration mas devem seguir o mesmo lock).
UPDATE config_profiles cp
   SET is_homologated = TRUE
  FROM model_homologations mh
 WHERE mh.profile_id = cp.id
   AND mh.status = 'homologated';

CREATE INDEX idx_profiles_homologated ON config_profiles(is_homologated)
    WHERE is_homologated = TRUE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_profiles_homologated;
ALTER TABLE config_profiles DROP COLUMN IF EXISTS is_homologated;

-- +goose StatementEnd
