#!/usr/bin/env bash
#
# bootstrap.sh — provisiona um servidor Debian 13 (trixie) limpo para o SentinelACS.
#
# IDEMPOTENTE: executar 2× não quebra nada. Validações em cada etapa.
#
# O QUE INSTALA:
#   - Docker engine (repo oficial Docker, não Snap) + compose plugin
#   - ufw (firewall), fail2ban, age (cifragem de backup), unattended-upgrades
#   - Usuário 'sentinel' (system user) dono dos containers e dados
#
# O QUE CONFIGURA:
#   - Firewall: 22 (SSH), 80/443 (web), 7547 + 7567 (TR-069 + GenieACS-FS).
#               Postgres/Redis/Mongo/NBI ficam na rede docker interna — não abertos.
#   - SSH hardening: PermitRootLogin=no, PasswordAuthentication=no.
#                    Roda APÓS validar que key auth funciona — sem trancar você fora.
#   - /opt/sentinelacs com subdirs (config, secrets, backup) + perms corretas.
#   - Auto-update de patches de segurança via unattended-upgrades.
#
# USO:
#   1. ssh celinet@SERVER  (com a senha provisória)
#   2. sudo bash bootstrap.sh
#   3. seguir instruções no fim para terminar o init-secrets.sh
#
# Requer: rodar como root (use sudo).

set -euo pipefail

# ──────────── Cores e helpers ────────────
RED=$'\033[0;31m'; GRN=$'\033[0;32m'; YLW=$'\033[1;33m'; CLR=$'\033[0m'
log()  { echo "${GRN}[bootstrap]${CLR} $*"; }
warn() { echo "${YLW}[warn]${CLR}      $*"; }
err()  { echo "${RED}[erro]${CLR}      $*" >&2; }
die()  { err "$*"; exit 1; }

# ──────────── Pré-checks ────────────
[[ $EUID -eq 0 ]] || die "rode como root: sudo $0"
[[ -f /etc/debian_version ]] || die "este script é específico Debian 13"

DEB_VER=$(cut -d. -f1 < /etc/debian_version 2>/dev/null || echo 0)
if [[ "$DEB_VER" -lt 13 ]]; then
    warn "esperava Debian 13, achei versão $(cat /etc/debian_version) — prosseguindo, mas pacotes podem divergir"
fi

# ──────────── Sentinel user ────────────
# UID/GID 9001 fixo casa com o user 'app' (UID 9001) dentro do container,
# para que volumes do tipo bind funcionem sem cross-permissions hell.
# Se sentinel já existe com UID diferente (instalação antiga), migramos
# o UID/GID e ajustamos ownership de tudo em /opt/sentinelacs/.
SENTINEL_USER=sentinel
SENTINEL_HOME=/opt/sentinelacs
SENTINEL_UID=9001
SENTINEL_GID=9001

if ! getent group "$SENTINEL_USER" &>/dev/null; then
    groupadd --system --gid "$SENTINEL_GID" "$SENTINEL_USER"
elif [[ "$(getent group "$SENTINEL_USER" | cut -d: -f3)" != "$SENTINEL_GID" ]]; then
    log "ajustando GID de $SENTINEL_USER para $SENTINEL_GID"
    groupmod --gid "$SENTINEL_GID" "$SENTINEL_USER"
fi

if ! id -u "$SENTINEL_USER" &>/dev/null; then
    log "criando usuário system '$SENTINEL_USER' (UID $SENTINEL_UID)"
    useradd --system --uid "$SENTINEL_UID" --gid "$SENTINEL_USER" \
            --create-home --home-dir "$SENTINEL_HOME" \
            --shell /usr/sbin/nologin "$SENTINEL_USER"
elif [[ "$(id -u "$SENTINEL_USER")" != "$SENTINEL_UID" ]]; then
    OLD_UID=$(id -u "$SENTINEL_USER")
    log "ajustando UID de $SENTINEL_USER de $OLD_UID para $SENTINEL_UID"
    usermod --uid "$SENTINEL_UID" "$SENTINEL_USER"
    # Migra ownership de qualquer file/dir que pertencia ao UID antigo.
    find /opt/sentinelacs -uid "$OLD_UID" -exec chown "$SENTINEL_UID" {} + 2>/dev/null || true
else
    log "usuário '$SENTINEL_USER' já existe com UID correto"
fi

# ──────────── Diretórios ────────────
log "criando árvore /opt/sentinelacs"
install -d -o "$SENTINEL_USER" -g "$SENTINEL_USER" -m 0750 \
    "$SENTINEL_HOME" \
    "$SENTINEL_HOME/config" \
    "$SENTINEL_HOME/secrets" \
    "$SENTINEL_HOME/backup" \
    "$SENTINEL_HOME/logs" \
    "$SENTINEL_HOME/scripts" \
    "$SENTINEL_HOME/state"

# Garantia: chown -R em re-execuções. O sync inicial do GHA Provision faz
# 'chown -R celinet:celinet /opt/sentinelacs' antes do bootstrap rodar
# (pra permitir rsync sem sudo). Sem este chown, dirs como state/ ficam
# como celinet e o deploy.sh (que roda como sentinel) não consegue escrever.
# Excluímos scripts/ porque ele é re-sincado depois com 'install' atômico.
log "normalizando ownership de /opt/sentinelacs/{config,secrets,backup,logs,state}"
chown -R "$SENTINEL_USER:$SENTINEL_USER" \
    "$SENTINEL_HOME/config" \
    "$SENTINEL_HOME/secrets" \
    "$SENTINEL_HOME/backup" \
    "$SENTINEL_HOME/logs" \
    "$SENTINEL_HOME/state"

# secrets/ é mais restrito — só o user lê.
chmod 0700 "$SENTINEL_HOME/secrets"

# ──────────── apt update + base packages ────────────
log "atualizando apt"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get -y -qq dist-upgrade

log "instalando pacotes base"
apt-get install -y -qq --no-install-recommends \
    ca-certificates curl gnupg lsb-release \
    ufw fail2ban age \
    unattended-upgrades apt-listchanges \
    jq htop tmux less rsync \
    cron logrotate

# ──────────── Docker ────────────
if ! command -v docker &>/dev/null; then
    log "instalando Docker engine (repo oficial)"
    install -m 0755 -d /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/debian/gpg \
        | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg

    DEB_CODENAME=$(. /etc/os-release && echo "$VERSION_CODENAME")
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
https://download.docker.com/linux/debian $DEB_CODENAME stable" \
        > /etc/apt/sources.list.d/docker.list

    apt-get update -qq
    apt-get install -y -qq --no-install-recommends \
        docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
else
    log "docker já instalado: $(docker --version)"
fi

systemctl enable --now docker

# Adiciona sentinel ao grupo docker — evita sudo nas operações de container.
usermod -aG docker "$SENTINEL_USER"

# ──────────── unattended-upgrades ────────────
log "habilitando unattended-upgrades (security only)"
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
dpkg-reconfigure -f noninteractive unattended-upgrades >/dev/null

# ──────────── ufw firewall ────────────
log "configurando firewall (ufw)"
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing

ufw allow 22/tcp   comment 'SSH'
ufw allow 80/tcp   comment 'HTTP (Traefik redireciona p/ HTTPS)'
ufw allow 443/tcp  comment 'HTTPS (Traefik)'
ufw allow 7547/tcp comment 'GenieACS CWMP — TR-069 dos CPEs'
ufw allow 7567/tcp comment 'GenieACS FS — download de firmware'

# Postgres/Redis/Mongo/NBI permanecem fechados — só rede docker interna.

ufw --force enable
log "firewall ativo: $(ufw status | grep -c '/tcp\b' || true) regras tcp"

# ──────────── fail2ban ────────────
log "habilitando fail2ban"
cat > /etc/fail2ban/jail.d/sentinel.conf <<'EOF'
[sshd]
enabled  = true
port     = ssh
filter   = sshd
logpath  = /var/log/auth.log
maxretry = 5
findtime = 10m
bantime  = 1h
EOF

systemctl enable --now fail2ban
systemctl restart fail2ban

# ──────────── SSH hardening ────────────
# CUIDADO: só desliga senha se já existe ao menos 1 chave em authorized_keys.
# Isso evita trancar o operador fora do servidor.
HARDEN_SSH=1
HOMES=$(getent passwd "$(whoami)" celinet 2>/dev/null | awk -F: '{print $6}' | sort -u)
HAS_KEYS=0
for HOME_DIR in $HOMES /root; do
    if [[ -s "$HOME_DIR/.ssh/authorized_keys" ]]; then
        HAS_KEYS=1
        log "encontrou authorized_keys em $HOME_DIR — pode desligar senha"
        break
    fi
done

if [[ $HAS_KEYS -eq 0 ]]; then
    warn "NENHUMA authorized_keys encontrada — NÃO vou desligar SSH-com-senha agora"
    warn "  faça do seu workstation:  ssh-copy-id celinet@<IP>"
    warn "  e re-rode este script (ou edite /etc/ssh/sshd_config manual)"
    HARDEN_SSH=0
fi

if [[ $HARDEN_SSH -eq 1 ]]; then
    log "endurecendo SSH (root off, senha off)"
    SSHD=/etc/ssh/sshd_config
    cp "$SSHD" "$SSHD.bak.$(date +%s)"

    sed -ri \
        -e 's/^#?PermitRootLogin .*/PermitRootLogin no/' \
        -e 's/^#?PasswordAuthentication .*/PasswordAuthentication no/' \
        -e 's/^#?ChallengeResponseAuthentication .*/ChallengeResponseAuthentication no/' \
        -e 's/^#?KbdInteractiveAuthentication .*/KbdInteractiveAuthentication no/' \
        -e 's/^#?UsePAM .*/UsePAM yes/' \
        "$SSHD"

    grep -q '^PermitRootLogin no' "$SSHD"      || echo "PermitRootLogin no"      >> "$SSHD"
    grep -q '^PasswordAuthentication no' "$SSHD" || echo "PasswordAuthentication no" >> "$SSHD"

    sshd -t   # valida config antes de reload — se quebrou, abortamos.
    systemctl reload ssh
    log "SSH hardened ✓ (backup em $SSHD.bak.*)"
fi

# ──────────── sudoers para automação de deploy ────────────
# celinet (operador remoto via SSH key) precisa rodar `sudo -u sentinel ...`
# e alguns sudos de root sem prompt — caso contrário, GitHub Actions trava
# pedindo senha em fluxo não-interativo.
#
# Risco: quem tem a SSH key do celinet vira root sem 2FA. É o mesmo risco
# que tínhamos com password auth — apenas trocamos o vetor (key vs password).
# Mitigação: rotacionar SSH key periodicamente; manter PAT do GHCR como
# secret separado; backup off-site em conta separada.
SUDOERS_DEPLOY=/etc/sudoers.d/celinet-deploy
if [[ ! -f "$SUDOERS_DEPLOY" ]]; then
    log "configurando NOPASSWD para celinet (deploy automation)"
    cat > "$SUDOERS_DEPLOY" <<'EOF'
# celinet pode rodar QUALQUER comando como qualquer usuário sem senha.
# Necessário para deploys automatizados via GitHub Actions (SSH key + sudo).
#
# MODELO DE AMEAÇA: a SSH key de celinet é a credencial primária. Se ela
# for comprometida, o atacante já consegue rodar bootstrap.sh como root
# (pelo provision flow). NOPASSWD não amplia o blast radius — apenas
# remove o atrito da automação.
#
# Mitigações: rotacionar SSH key periodicamente (Settings → Secrets → SSH_KEY),
# auditar access logs (/var/log/auth.log), backup off-site em conta separada.
celinet ALL=(ALL) NOPASSWD: ALL
EOF
    chmod 0440 "$SUDOERS_DEPLOY"
    visudo -cf "$SUDOERS_DEPLOY" >/dev/null || die "sudoers inválido — abortei"
    log "sudoers OK (celinet roda sudo sem prompt)"
else
    log "sudoers já existe em $SUDOERS_DEPLOY"
fi

# ──────────── age key para cifragem de backup ────────────
AGE_KEY="$SENTINEL_HOME/secrets/age.key"
if [[ ! -f "$AGE_KEY" ]]; then
    log "gerando age key (cifragem de backups)"
    sudo -u "$SENTINEL_USER" age-keygen -o "$AGE_KEY" 2>/dev/null
    chmod 0400 "$AGE_KEY"
    log "  pública: $(grep '# public key' "$AGE_KEY" | sed 's/.*: //')"
    log "  ATENÇÃO: faça backup de $AGE_KEY em local SEPARADO (sem ela, backups são irrecuperáveis)"
else
    log "age key já existe em $AGE_KEY"
fi

# ──────────── App crypto key (AES-GCM em hex — TOTP/secrets em runtime) ────────────
# NOTA: o nome "app.age.key" é histórico; o conteúdo é HEX 64-char (32 bytes
# AES key), NÃO formato age. O LoadKeyFromFile do app faz hex.DecodeString.
# (O 'age.key' acima — sem prefixo 'app.' — esse SIM é age, usado para backup.)
APP_AGE_KEY="$SENTINEL_HOME/secrets/app.age.key"
if [[ ! -f "$APP_AGE_KEY" ]]; then
    log "gerando app crypto key (32 bytes hex — AES-GCM)"
    openssl rand -hex 32 > "$APP_AGE_KEY"
    chmod 0400 "$APP_AGE_KEY"
fi

# Validação defensiva: se o file existe mas tem conteúdo errado (ex:
# instalação antiga gerou via age-keygen), regenera. Hex puro = só 0-9a-f
# em 64 chars, sem '#' nem newlines internas.
if ! grep -qE '^[0-9a-f]{64}$' "$APP_AGE_KEY" 2>/dev/null; then
    warn "$APP_AGE_KEY existe mas não é hex 32B — REGENERANDO"
    openssl rand -hex 32 > "$APP_AGE_KEY"
    chmod 0400 "$APP_AGE_KEY"
fi

# Garantia idempotente: dono e perms dos secrets, independente de quem
# criou. Em runs antigos pode ter ficado celinet:celinet ou root:root.
chown -R "$SENTINEL_USER:$SENTINEL_USER" "$SENTINEL_HOME/secrets"
chmod 0750 "$SENTINEL_HOME/secrets"
find "$SENTINEL_HOME/secrets" -type f -exec chmod 0400 {} +

# ──────────── crontab para backup horário ────────────
CRON_FILE=/etc/cron.d/sentinelacs-backup
if [[ ! -f "$CRON_FILE" ]]; then
    log "instalando cron de backup horário"
    cat > "$CRON_FILE" <<EOF
# SentinelACS — backup horário (PG) + diário (Mongo).
# Saídas vão para syslog via logger.
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

15 *  * * * $SENTINEL_USER /opt/sentinelacs/scripts/backup.sh pg    2>&1 | logger -t sentinel-backup
30 3  * * * $SENTINEL_USER /opt/sentinelacs/scripts/backup.sh mongo 2>&1 | logger -t sentinel-backup
EOF
    chmod 0644 "$CRON_FILE"
fi

# ──────────── CWMP ACL reconciler (systemd + iptables) ────────────
# Worker grava /opt/sentinelacs/cwmp-acl/cidrs.txt; systemd path-unit
# detecta a mudança e dispara o reconciler que aplica iptables. Toda a
# lógica está em cwmp-acl-setup.sh — chamado tanto aqui (provisão nova)
# quanto pelo deploy.yml (servers já provisionados ganham via deploy).
SCRIPTS_DIR=$(dirname "$0")
if [ -f "$SCRIPTS_DIR/cwmp-acl-setup.sh" ]; then
    log "instalando CWMP ACL reconciler"
    bash "$SCRIPTS_DIR/cwmp-acl-setup.sh"
else
    warn "cwmp-acl-setup.sh ausente — bootstrap incompleto, deploy.yml repete"
fi

# ──────────── Resumo + próximos passos ────────────
cat <<EOF

${GRN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${CLR}
${GRN}  Bootstrap concluído.${CLR}
${GRN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${CLR}

Próximos passos (rode AGORA, na ordem):

  1. ${YLW}Trocar a senha do celinet${CLR} (a antiga pode ter vazado em logs):
       passwd celinet

  2. ${YLW}Configurar SSH key${CLR} do seu workstation (se ainda não fez):
       # NA SUA MÁQUINA:
       ssh-copy-id celinet@$(hostname -I | awk '{print $1}')
       # se faltou key auth, re-rode este bootstrap após o copy-id

  3. ${YLW}Inicializar secrets do app${CLR}:
       sudo -u $SENTINEL_USER /opt/sentinelacs/scripts/init-secrets.sh

  4. ${YLW}Login no GHCR${CLR} (para puxar a imagem do app):
       docker login ghcr.io -u <seu-github-user>

  5. ${YLW}Deploy inicial${CLR}:
       sudo -u $SENTINEL_USER /opt/sentinelacs/scripts/deploy.sh

  6. ${YLW}Apontar DNS${CLR} para este IP:
       A   acs.exemplo.com.br  ->  $(hostname -I | awk '{print $1}')

  7. Configurar GitHub Actions (Settings → Secrets → Actions):
       SSH_HOST=$(hostname -I | awk '{print $1}')
       SSH_USER=celinet
       SSH_KEY=<chave privada do par usado em ssh-copy-id>

Logs do bootstrap acima — qualquer linha em vermelho/amarelo merece atenção.
EOF
