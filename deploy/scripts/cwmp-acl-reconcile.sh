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
#   3. Garante jump da DOCKER-USER chain para CWMP_ACL na porta interna
#      do container (idempotente)
#   4. Remove jumps legados em INPUT, ou em DOCKER-USER por porta
#      externa, se existirem (migration)
#
# Por que DOCKER-USER e não INPUT: o GenieACS roda em container Docker
# com porta publicada. Tráfego ao container atravessa
# nat/PREROUTING → filter/FORWARD → DOCKER-USER → DOCKER. NÃO toca INPUT.
# Logo, jump em INPUT é inócuo — todos os pacotes passam direto pra
# chain DOCKER e são aceitos.
#
# Por que filtramos pela porta INTERNA do container (7547) e não pelas
# portas externas (7547, 8080): em DOCKER-USER o pacote já passou pelo
# DNAT do PREROUTING — destination port foi reescrito pra 7547 (porta
# do container) independente da porta externa que foi usada. Filtrar
# por --dport 7547 aqui cobre todas as portas publicadas que mapeiam
# pra :7547 do container (ex: 7547:7547, 8080:7547).
#
# Lista vazia = chain só com DROP = deny-all (decisão do operador).
# Arquivo ausente = exit silencioso (worker ainda não fez o primeiro tick).

set -euo pipefail

ACL_FILE=/opt/sentinelacs/cwmp-acl/cidrs.txt
CONTAINER_CWMP_PORT=7547
CHAIN=CWMP_ACL

# Portas externas que historicamente tiveram regras de jump — usadas
# apenas para limpeza idempotente em INPUT (migration de versões
# anteriores do script) e em DOCKER-USER (migration da versão que
# tentava filtrar por porta externa, antes de descobrirmos o efeito do
# DNAT).
LEGACY_HOST_PORTS=(7547 8080)

[ -f "$ACL_FILE" ] || exit 0

# 1) Substitui a chain via iptables-restore -n. -n = no flush das chains
# que não estão no input — assim DOCKER-USER, FORWARD e demais ficam
# intactas (apenas CWMP_ACL é redefinida).
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

# 2) Garante o jump em DOCKER-USER na porta interna do container.
if ! iptables -C DOCKER-USER -p tcp --dport "$CONTAINER_CWMP_PORT" -j "$CHAIN" 2>/dev/null; then
    iptables -I DOCKER-USER 1 -p tcp --dport "$CONTAINER_CWMP_PORT" -j "$CHAIN"
fi

# 3) Limpa jumps legados (idempotente, no-op se nada existir):
#    - INPUT: versão antiga colocava jump aqui (caminho errado, Docker bypassa)
#    - DOCKER-USER por porta externa: versão de transição filtrava por
#      8080 antes de descobrirmos que após DNAT a porta é 7547
for port in "${LEGACY_HOST_PORTS[@]}"; do
    while iptables -C INPUT -p tcp --dport "$port" -j "$CHAIN" 2>/dev/null; do
        iptables -D INPUT -p tcp --dport "$port" -j "$CHAIN"
    done
    if [ "$port" != "$CONTAINER_CWMP_PORT" ]; then
        while iptables -C DOCKER-USER -p tcp --dport "$port" -j "$CHAIN" 2>/dev/null; do
            iptables -D DOCKER-USER -p tcp --dport "$port" -j "$CHAIN"
        done
    fi
done

logger -t cwmp-acl-reconcile "applied $(wc -l < "$ACL_FILE") CIDR(s) on container port :${CONTAINER_CWMP_PORT}"
