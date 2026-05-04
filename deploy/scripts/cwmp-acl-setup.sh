#!/usr/bin/env bash
# cwmp-acl-setup — instala/atualiza o reconciler de ACL TR-069 no host.
#
# Idempotente. Roda como root (chamado via `sudo` pelo deploy.yml ou
# diretamente pelo bootstrap.sh).
#
# Faz:
#   1. Copia cwmp-acl-reconcile.{sh,path,service} para os caminhos do systemd
#   2. Cria /opt/sentinelacs/cwmp-acl com dono UID 9001 (sentinel/app)
#   3. Toca cidrs.txt vazio se não existir
#   4. systemctl daemon-reload + enable --now do .path-unit
#
# IMPORTANTE: se cidrs.txt já tem conteúdo, o path-unit NÃO dispara só
# por enable — só em PathChanged real. Assim ativar este script em um
# servidor com regras pré-existentes é seguro: a próxima atualização
# de CIDR pelo worker é que vai aplicar.

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
TARGET_BIN=/usr/local/bin/cwmp-acl-reconcile
TARGET_DIR=/etc/systemd/system

if [[ $EUID -ne 0 ]]; then
    echo "deve rodar como root (use sudo)"
    exit 1
fi

install -m 0755 "$SCRIPT_DIR/cwmp-acl-reconcile.sh"      "$TARGET_BIN"
install -m 0644 "$SCRIPT_DIR/cwmp-acl-reconcile.path"    "$TARGET_DIR/cwmp-acl-reconcile.path"
install -m 0644 "$SCRIPT_DIR/cwmp-acl-reconcile.service" "$TARGET_DIR/cwmp-acl-reconcile.service"

mkdir -p /opt/sentinelacs/cwmp-acl
chown 9001:9001 /opt/sentinelacs/cwmp-acl
chmod 0755 /opt/sentinelacs/cwmp-acl
if [[ ! -f /opt/sentinelacs/cwmp-acl/cidrs.txt ]]; then
    touch /opt/sentinelacs/cwmp-acl/cidrs.txt
    chown 9001:9001 /opt/sentinelacs/cwmp-acl/cidrs.txt
    chmod 0600 /opt/sentinelacs/cwmp-acl/cidrs.txt
fi

systemctl daemon-reload
systemctl enable --now cwmp-acl-reconcile.path
echo "cwmp-acl-setup: ok ($(systemctl is-active cwmp-acl-reconcile.path))"
