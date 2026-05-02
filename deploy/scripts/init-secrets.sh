#!/usr/bin/env bash
#
# init-secrets.sh — gera /opt/sentinelacs/config/.env com secrets aleatórios.
#
# Roda 1×, depois nunca mais — re-rodar gera novos secrets e quebra logins
# existentes (sessões, TOTP). Para rotacionar, ver runbook em deploy/README.md.
#
# IDEMPOTENTE no sentido fraco: se .env existe, NÃO sobrescreve (sai com aviso).
#
# Modos de operação:
#   • Interativo: roda sem args, perguntas no stdin.
#   • Não-interativo: passa via env (CI/CD):
#       PUBLIC_HOST=... ACME_EMAIL=... GHCR_OWNER=... \
#       NONINTERACTIVE=1 init-secrets.sh
#
# Variáveis externas (todas opcionais — interativo pergunta o que faltar):
#   PUBLIC_HOST           — domínio público (ex: acs.empresa.com.br)
#   ACME_EMAIL            — email para Let's Encrypt
#   GHCR_OWNER            — owner do repo GitHub (ex: celinet)
#   NONINTERACTIVE        — se "1", não pergunta; usa defaults para vazios
#

set -euo pipefail

ENV_FILE=/opt/sentinelacs/config/.env
SECRETS_DIR=/opt/sentinelacs/secrets

if [[ -f "$ENV_FILE" ]]; then
    echo "[init-secrets] $ENV_FILE já existe — NÃO sobrescrevendo."
    echo "  para rotacionar, edite o arquivo manualmente."
    exit 0
fi

# Helper: 32 bytes random base64.
rand() { openssl rand -base64 32 | tr -d '\n'; }

# ──────────── inputs (env tem precedência sobre prompt) ────────────
NONINTERACTIVE="${NONINTERACTIVE:-0}"

if [[ "$NONINTERACTIVE" != "1" ]]; then
    [[ -z "${PUBLIC_HOST:-}" ]] && read -rp "Domínio público (ex: acs.empresa.com.br) [vazio = http only]: " PUBLIC_HOST
    [[ -z "${ACME_EMAIL:-}" ]]  && read -rp "Email para Let's Encrypt: " ACME_EMAIL
    [[ -z "${GHCR_OWNER:-}" ]]  && read -rp "Owner GitHub (para imagem ghcr.io/<owner>/sentineltr069): " GHCR_OWNER
fi

PUBLIC_HOST="${PUBLIC_HOST:-localhost}"
ACME_EMAIL="${ACME_EMAIL:-admin@$PUBLIC_HOST}"
GHCR_OWNER="${GHCR_OWNER:-celinet}"

POSTGRES_PASSWORD=$(rand)
REDIS_PASSWORD=$(rand)
APP_SESSION_SECRET=$(rand)

cat > "$ENV_FILE" <<EOF
# ──────── App ────────
APP_ENV=production
APP_PORT=8080
APP_BASE_URL=https://$PUBLIC_HOST
APP_SESSION_SECRET=$APP_SESSION_SECRET
APP_AGE_KEY_FILE=$SECRETS_DIR/app.age.key
APP_SHUTDOWN=15s

# ──────── DB ────────
POSTGRES_DB=sentinelacs
POSTGRES_USER=sentinel
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
DATABASE_URL=postgres://sentinel:$POSTGRES_PASSWORD@postgres:5432/sentinelacs?sslmode=disable

# ──────── Redis ────────
REDIS_PASSWORD=$REDIS_PASSWORD
REDIS_URL=redis://default:$REDIS_PASSWORD@redis:6379/0

# ──────── GenieACS NBI (interno na rede docker) ────────
GENIEACS_NBI_URL=http://genieacs-nbi:7557
GENIEACS_FS_URL=http://genieacs-fs:7567
GENIEACS_AUTH_USER=
GENIEACS_AUTH_PASS=

# ──────── Voalle (preencher quando OAuth estiver liberado) ────────
VOALLE_BASE_URL=
VOALLE_CLIENT_ID=
VOALLE_CLIENT_SECRET=
VOALLE_TIMEOUT=30s
VOALLE_SYNC_INTERVAL=5m

# ──────── Notifiers (preencha conforme canais) ────────
NOTIFIER_WHATSAPP_BASE_URL=
NOTIFIER_WHATSAPP_API_KEY=
NOTIFIER_WHATSAPP_INSTANCE=
NOTIFIER_TELEGRAM_BOT_TOKEN=
NOTIFIER_SMTP_HOST=
NOTIFIER_SMTP_PORT=587
NOTIFIER_SMTP_USERNAME=
NOTIFIER_SMTP_PASSWORD=
NOTIFIER_SMTP_FROM_ADDRESS=
NOTIFIER_SMTP_FROM_NAME=SentinelACS Alerts

# ──────── Logging ────────
LOG_LEVEL=info
LOG_FORMAT=json

# ──────── Compose / Traefik ────────
PUBLIC_HOST=$PUBLIC_HOST
TRAEFIK_ACME_EMAIL=$ACME_EMAIL
GHCR_OWNER=$GHCR_OWNER
IMAGE_TAG=ghcr.io/$GHCR_OWNER/sentineltr069:latest

# ──────── Backup (preencha se for usar B2) ────────
BACKUP_B2_BUCKET=
BACKUP_B2_KEY_ID=
BACKUP_B2_APPLICATION_KEY=
BACKUP_RETENTION_DAYS=30
EOF

chmod 0640 "$ENV_FILE"

cat <<EOF

[init-secrets] $ENV_FILE criado.

  Domínio público: $PUBLIC_HOST
  Email ACME:      $ACME_EMAIL
  Imagem:          ghcr.io/$GHCR_OWNER/sentineltr069:latest

PRÓXIMO: edite o arquivo se for popular Voalle/notifiers/B2 agora,
         depois rode: /opt/sentinelacs/scripts/deploy.sh

⚠️  NUNCA commit este arquivo. Faça backup do diretório secrets/.
EOF
