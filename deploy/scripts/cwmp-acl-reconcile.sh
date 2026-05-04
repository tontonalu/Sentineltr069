#!/usr/bin/env bash
# cwmp-acl-reconcile — gera regras iptables a partir da lista de CIDRs
# escrita pelo worker do SentinelACS, e aplica de forma atômica.
#
# Fluxo:
#   1. Lê /opt/sentinelacs/cwmp-acl/cidrs.txt (uma faixa por linha)
#   2. Constrói um ruleset que:
#      - Substitui a chain CWMP_ACL inteira (iptables-restore -n)
#      - ACCEPT cada CIDR
#      - DROP no final (deny por padrão)
#   3. Garante o jump da INPUT chain para CWMP_ACL na porta 7547
#      (inserido idempotentemente como primeira regra)
#
# Lista vazia = chain só com DROP = deny-all (decisão do operador).
# Arquivo ausente = exit silencioso (worker ainda não fez o primeiro tick).

set -euo pipefail

ACL_FILE=/opt/sentinelacs/cwmp-acl/cidrs.txt
CWMP_PORT=7547
CHAIN=CWMP_ACL

[ -f "$ACL_FILE" ] || exit 0

# 1) Substitui a chain via iptables-restore -n. -n = no flush das chains
# que não estão no input — assim a INPUT e demais ficam intactas.
{
    echo "*filter"
    echo ":${CHAIN} - [0:0]"
    while IFS= read -r cidr; do
        # Permite linhas em branco e comentários (# ...)
        cidr=${cidr%%#*}
        cidr=${cidr// /}
        [ -z "$cidr" ] && continue
        echo "-A ${CHAIN} -s ${cidr} -j ACCEPT"
    done < "$ACL_FILE"
    echo "-A ${CHAIN} -j DROP"
    echo "COMMIT"
} | iptables-restore -n

# 2) Garante o jump na INPUT (inserido só uma vez).
if ! iptables -C INPUT -p tcp --dport "$CWMP_PORT" -j "$CHAIN" 2>/dev/null; then
    iptables -I INPUT 1 -p tcp --dport "$CWMP_PORT" -j "$CHAIN"
fi

logger -t cwmp-acl-reconcile "applied $(wc -l < "$ACL_FILE") CIDR(s)"
