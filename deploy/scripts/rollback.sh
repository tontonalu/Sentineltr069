#!/usr/bin/env bash
#
# rollback.sh — volta para um SHA específico (ou .last-good-sha por padrão).
#
# Uso:
#   /opt/sentinelacs/scripts/rollback.sh                 # usa .last-good-sha
#   /opt/sentinelacs/scripts/rollback.sh sha-abc12345    # tag específica
#
# IMPORTANTE: se a migração da versão atual fez ALTER destrutivo, rollback
# pode não restaurar dados. Por isso CP-9.5 também testa o caminho.
# Convenção: migrations devem ser backwards-compatible (add columns, never drop).

set -euo pipefail

ROOT=/opt/sentinelacs
CFG="$ROOT/config/.env"
COMPOSE_BASE="$ROOT/docker-compose.yml"
COMPOSE_PROD="$ROOT/docker-compose.prod.yml"
STATE_DIR="$ROOT/state"
LAST_GOOD="$STATE_DIR/last-good-sha"

TARGET_SHA="${1:-}"
if [[ -z "$TARGET_SHA" ]]; then
    [[ -f "$LAST_GOOD" ]] || { echo "rollback: nenhum SHA fornecido e $LAST_GOOD não existe"; exit 1; }
    TARGET_SHA="$(cat "$LAST_GOOD")"
fi

echo "[rollback] alvo: $TARGET_SHA"

# Atualiza .env com a tag alvo.
source "$CFG"
GHCR_OWNER="${GHCR_OWNER:-celinet}"
NEW_TAG="ghcr.io/$GHCR_OWNER/sentineltr069:$TARGET_SHA"

cp "$CFG" "$CFG.bak.$(date +%s)"
sed -i "s|^IMAGE_TAG=.*|IMAGE_TAG=$NEW_TAG|" "$CFG"

source "$CFG"
COMPOSE="docker compose --env-file $CFG -f $COMPOSE_BASE -f $COMPOSE_PROD"

echo "[rollback] puxando $NEW_TAG"
$COMPOSE pull app worker --quiet

# NOTA: NÃO rodamos migration down — destrutivo demais para automático.
# Se a migration da versão nova precisava ser revertida, faça manual.
echo "[rollback] subindo containers (sem migrate down)"
$COMPOSE up -d --no-deps app worker

# Healthcheck via docker inspect (HEALTHCHECK do Dockerfile bate dentro
# do container). Em prod a porta 8080 não é exposta — Traefik no caminho.
echo -n "[rollback] healthcheck "
HEALTH_OK=0
for i in {1..15}; do
    HEALTH=$(docker inspect --format='{{.State.Health.Status}}' sentinel-app 2>/dev/null || echo "missing")
    if [[ "$HEALTH" == "healthy" ]]; then
        HEALTH_OK=1
        break
    fi
    echo -n "."
    sleep 2
done
echo

if [[ $HEALTH_OK -eq 0 ]]; then
    echo "[rollback] ❌ rollback falhou — investigue manual: docker compose logs"
    exit 2
fi

echo "[rollback] ✓ recuperado para $TARGET_SHA"
