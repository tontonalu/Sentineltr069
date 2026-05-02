#!/usr/bin/env bash
#
# deploy.sh — atualiza a stack para a imagem mais nova (ou TAG específica).
#
# Sequência segura:
#   1. Salva SHA atual em .last-good-sha (caso já esteja rodando ok).
#   2. docker compose pull (puxa imagem nova).
#   3. Roda migrations em container efêmero (`migrate -cmd up`).
#   4. docker compose up -d (zero-downtime para Traefik via labels).
#   5. Healthcheck loop: 30 tentativas × 2s = 60s.
#   6. Se healthcheck falhar → rollback automático (.last-good-sha).
#   7. Notifica Telegram se configurado.
#
# IDEMPOTENTE: re-rodar com a mesma imagem é no-op (compose detecta).
#
# Uso:
#   /opt/sentinelacs/scripts/deploy.sh                 # latest
#   /opt/sentinelacs/scripts/deploy.sh sha-abc12345    # tag específica
#

set -euo pipefail

# ──────────────────────────── helpers ────────────────────────────
notify_telegram() {
    local msg="$1"
    [[ -z "${NOTIFIER_TELEGRAM_BOT_TOKEN:-}" ]] && return 0
    [[ -z "${TELEGRAM_DEPLOY_CHAT:-}" ]]       && return 0
    curl -fsSL --max-time 5 \
        -d "chat_id=$TELEGRAM_DEPLOY_CHAT" \
        -d "text=$msg" \
        -d "parse_mode=Markdown" \
        "https://api.telegram.org/bot$NOTIFIER_TELEGRAM_BOT_TOKEN/sendMessage" >/dev/null || true
}

ROOT=/opt/sentinelacs
CFG="$ROOT/config/.env"
COMPOSE_BASE="$ROOT/docker-compose.yml"
COMPOSE_PROD="$ROOT/docker-compose.prod.yml"
STATE_DIR="$ROOT/state"
LAST_GOOD="$STATE_DIR/last-good-sha"

# docker compose faz stat(.) para resolver paths relativos no compose file.
# Quando invocado via 'sudo -u sentinel deploy.sh' a partir de /home/celinet
# (CWD herdado), sentinel não consegue ler o diretório → permission denied.
cd "$ROOT"

mkdir -p "$STATE_DIR"

[[ -f "$CFG" ]]          || { echo "config/.env ausente — rode init-secrets.sh primeiro"; exit 1; }
[[ -f "$COMPOSE_BASE" ]] || { echo "docker-compose.yml não está em $ROOT"; exit 1; }

source "$CFG"

TARGET_TAG="${1:-}"
if [[ -n "$TARGET_TAG" ]]; then
    # Override IMAGE_TAG via .env. GHCR_OWNER vem do .env populado pelo init-secrets.
    OWNER="${GHCR_OWNER:-celinet}"
    sed -i.bak "s|^IMAGE_TAG=.*|IMAGE_TAG=ghcr.io/$OWNER/sentineltr069:$TARGET_TAG|" "$CFG"
    source "$CFG"
fi
COMPOSE="docker compose --env-file $CFG -f $COMPOSE_BASE -f $COMPOSE_PROD"

# ──────────── Captura SHA corrente para rollback ────────────
CURRENT_SHA=""
if docker ps --format '{{.Image}}' | grep -q sentineltr069; then
    CURRENT_SHA=$(docker inspect sentinel-app --format '{{.Image}}' 2>/dev/null \
        | xargs -I{} docker inspect {} --format '{{index .RepoTags 0}}' 2>/dev/null \
        | sed 's/.*://' || echo "")
fi

echo "[deploy] tag alvo: $IMAGE_TAG"
echo "[deploy] tag atual: ${CURRENT_SHA:-(nenhuma)}"

# ──────────── Pull ────────────
echo "[deploy] puxando imagens"
$COMPOSE pull --quiet 2>&1 | tail -5

# ──────────── Migrações ────────────
echo "[deploy] rodando migrations"
$COMPOSE run --rm --entrypoint /usr/local/bin/migrate app -cmd up

# ──────────── Up ────────────
echo "[deploy] subindo stack"
$COMPOSE up -d --remove-orphans

# ──────────── Healthcheck loop ────────────
echo -n "[deploy] healthcheck "
HEALTH_OK=0
for i in {1..30}; do
    if curl -fsSL -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz | grep -q '^2'; then
        HEALTH_OK=1
        break
    fi
    echo -n "."
    sleep 2
done
echo

if [[ $HEALTH_OK -eq 0 ]]; then
    echo "[deploy] ❌ healthcheck FAILED após 60s — iniciando rollback"
    if [[ -n "$CURRENT_SHA" ]]; then
        "$ROOT/scripts/rollback.sh" "$CURRENT_SHA"
    elif [[ -f "$LAST_GOOD" ]]; then
        "$ROOT/scripts/rollback.sh" "$(cat "$LAST_GOOD")"
    else
        echo "[deploy] ⚠️  sem SHA conhecido para rollback — investigue manual"
    fi
    notify_telegram "❌ deploy falhou — rollback ativado" || true
    exit 1
fi

# ──────────── Tag bem-sucedida → grava .last-good-sha ────────────
NEW_SHA="${IMAGE_TAG##*:}"
echo "$NEW_SHA" > "$LAST_GOOD"
echo "[deploy] ✓ saudável — SHA $NEW_SHA registrado em $LAST_GOOD"

notify_telegram "✅ deploy ok — versão \`$NEW_SHA\`" || true

# ──────────── Limpa imagens antigas (mantém últimas 3) ────────────
echo "[deploy] limpando imagens antigas"
docker image prune -f --filter "until=72h" >/dev/null

echo "[deploy] feito."
