# SentinelACS — Deploy & Operações

Runbook operacional para o servidor de produção.

## Ambiente alvo

| Item | Valor |
|---|---|
| **IP público** | `177.72.177.102` |
| **Usuário Linux** | `celinet` (sudo) |
| **Distribuição** | Debian 13 (trixie), kernel 6.12.x amd64 |
| **Diretório raiz** | `/opt/sentinelacs/` |
| **Usuário system** | `sentinel` (rodam containers e cron de backup) |
| **Image registry** | `ghcr.io/celinet/sentineltr069` |
| **Domínio público** | a definir (ex: `acs.empresa.com.br`) |

## ⚠️ Antes de tudo — security checklist

A senha do `celinet` foi compartilhada em texto claro durante o setup. **Trocar imediatamente após o primeiro login**:

```bash
ssh celinet@177.72.177.102
# (use a senha provisória)
passwd celinet
```

O bootstrap **desabilita SSH com senha** automaticamente — mas só depois que ele detectar uma SSH key em `~/.ssh/authorized_keys`. Faça isso **antes** de rodar o bootstrap (passo 2 abaixo) ou você não conseguirá voltar.

---

## Setup inicial (1ª vez — máquina limpa)

**Recomendado: tudo via GitHub Actions** (workflow `provision.yml`). Você só toca os Settings do repo — bootstrap, install de Docker, hardening de SSH, geração de secrets, deploy e seed do admin acontecem dentro de um único workflow run.

### Pré-requisitos antes de rodar a workflow

#### 1. Gerar uma deploy SSH key (no seu workstation, 1×)

```bash
ssh-keygen -t ed25519 -f /tmp/sentinelacs_deploy_key -C "deploy@sentinelacs" -N ""
cat /tmp/sentinelacs_deploy_key       # privada → secret SSH_KEY
cat /tmp/sentinelacs_deploy_key.pub   # pública → secret SSH_PUBLIC_KEY
```

(Se preferir não gravar a privada localmente, dá pra gerar via `gh secret set SSH_KEY < <(ssh-keygen -t ed25519 -f - <<< $'\n')` — mas o método acima é mais simples.)

#### 2. Criar Personal Access Token para o GHCR

1. https://github.com/settings/tokens/new
2. Escopo: **`read:packages`** (apenas)
3. Expira em 90 dias (rotacionar via mesmo workflow)
4. Copiar o token — vai virar secret `GHCR_PAT`

#### 3. Configurar Secrets/Variables no GitHub

**Settings → Environments → New environment** chamado `production`. Considere marcar **"Required reviewers"** se quiser aprovação manual em deploys.

**Settings → Secrets and variables → Actions:**

| Tipo | Nome | Valor | Quando usar |
|---|---|---|---|
| Secret | `BOOTSTRAP_SSH_PASSWORD` | `celinet382474` (a senha provisória) | **APAGUE depois do provision** |
| Secret | `SSH_KEY` | conteúdo de `/tmp/sentinelacs_deploy_key` | sempre |
| Secret | `SSH_PUBLIC_KEY` | conteúdo de `/tmp/sentinelacs_deploy_key.pub` | apenas no provision |
| Secret | `GHCR_PAT` | PAT do passo 2 | sempre |
| Secret | `TELEGRAM_BOT_TOKEN` | (opcional) | notificação de deploy |
| Secret | `TELEGRAM_DEPLOY_CHAT` | (opcional) | idem |
| Variable | `SSH_HOST` | `177.72.177.102` | sempre |
| Variable | `SSH_USER` | `celinet` | sempre |
| Variable | `PUBLIC_HOST` | `acs.empresa.com.br` | sempre |
| Variable | `ACME_EMAIL` | seu email para Let's Encrypt | sempre |
| Variable | `GHCR_OWNER` | `celinet` (ou seu user GitHub) | sempre |
| Variable | `ADMIN_EMAIL` | email do admin inicial (ex: `weverton@empresa.com.br`) | seed |

#### 4. Verificar que a imagem Docker já foi publicada

A workflow `provision` puxa `ghcr.io/<GHCR_OWNER>/sentineltr069:latest`. Para ela existir, **a workflow CI tem que ter rodado pelo menos 1× com sucesso na branch main**. Faça um push qualquer (até um whitespace mudando o README serve) e aguarde o CI verde.

Confirme em **Code → Packages** do repo que `sentineltr069:latest` existe.

### Rodar o provision

1. Vá em **Actions → Provision (1ª vez) → Run workflow**
2. Branch: `main`
3. Inputs:
   - `seed_admin`: ✅ true (cria admin no fim)
   - `reset_secrets`: ⛔ false (só marque se for re-provisionar e quiser zerar o `.env`)
4. Run workflow → aguarde ~5 min

A workflow vai:
1. Validar todos os secrets/vars
2. Usar `BOOTSTRAP_SSH_PASSWORD` para entrar pela primeira vez (única vez que essa senha é usada)
3. Instalar `SSH_PUBLIC_KEY` no `~/.ssh/authorized_keys`
4. Sincronizar compose files + scripts para `/opt/sentinelacs/`
5. Rodar `bootstrap.sh` (Docker, ufw, fail2ban, hardening — **desliga senha**)
6. Rodar `init-secrets.sh` não-interativo (gera `.env` com 3 secrets aleatórios)
7. `docker login ghcr.io` no servidor com `GHCR_PAT`
8. `deploy.sh latest` — pull + migrate + up + healthcheck
9. Seed do admin com senha aleatória → exibe no Job Summary

### Pós-provision (mandatório)

1. **Apague o secret `BOOTSTRAP_SSH_PASSWORD`** — não vai ser mais usado, manter é risco desnecessário.
2. **Troque a senha do `celinet`** no servidor — defesa em profundidade:
   ```bash
   ssh celinet@177.72.177.102 'passwd'
   ```
   (a senha provisória não funciona mais para SSH, mas pode existir em outros serviços do servidor.)
3. **Aponte DNS A** `PUBLIC_HOST → 177.72.177.102` (Traefik espera para emitir o cert).
4. **Anote a senha do admin** que apareceu no Job Summary do run; troque no 1º login.

### Workflow alternativa (sem GHA)

Se preferir bootstrap manual via workstation (caso o GitHub esteja indisponível, etc.):

```bash
# 1) ssh-copy-id 1×
ssh-copy-id celinet@177.72.177.102

# 2) Bootstrap
make bootstrap-remote

# 3) Init-secrets interativo
ssh celinet@177.72.177.102 'sudo -u sentinel /opt/sentinelacs/scripts/init-secrets.sh'

# 4) Login GHCR + deploy
ssh celinet@177.72.177.102 'sudo -u sentinel docker login ghcr.io'
make deploy-remote

# 5) Seed admin
ssh celinet@177.72.177.102 \
  'sudo -u sentinel docker compose --env-file /opt/sentinelacs/config/.env \
     -f /opt/sentinelacs/docker-compose.yml \
     run --rm --entrypoint /usr/local/bin/migrate \
     -e SEED_ADMIN_EMAIL=admin@empresa.com.br \
     -e SEED_ADMIN_PASSWORD=trocar-no-primeiro-login-X1 \
     app -cmd seed'
```

---

## Operação contínua

### Atualizar para uma nova versão

GitHub Actions cuida automaticamente em cada push para `main` (ver `.github/workflows/deploy.yml`).

Manual (do workstation):

```bash
make deploy-remote                  # latest
# ou
ssh celinet@177.72.177.102 \
    'sudo -u sentinel /opt/sentinelacs/scripts/deploy.sh sha-abc12345'
```

### Rollback manual

```bash
ssh celinet@177.72.177.102
sudo -u sentinel /opt/sentinelacs/scripts/rollback.sh                 # usa .last-good-sha
sudo -u sentinel /opt/sentinelacs/scripts/rollback.sh sha-abc12345    # versão específica
```

### Logs

```bash
make logs-remote                    # tail interativo
# ou diretamente:
ssh celinet@177.72.177.102 'journalctl -u docker -f'
```

Logs do GenieACS ficam em volumes Docker — `docker logs sentinel-genieacs-cwmp`.

### Healthcheck

```bash
curl -fsSL https://acs.empresa.com.br/healthz
# {"status":"ok","version":"...","postgres":"up","redis":"up","genieacs":"up"}
```

### Backup manual

```bash
ssh celinet@177.72.177.102
sudo -u sentinel /opt/sentinelacs/scripts/backup.sh pg
sudo -u sentinel /opt/sentinelacs/scripts/backup.sh mongo
```

Cron já roda automático (PG horário, Mongo diário).

### Restore (atenção — destrutivo)

```bash
ssh celinet@177.72.177.102
ls /opt/sentinelacs/backup/pg/                                          # escolha o dump
sudo -u sentinel /opt/sentinelacs/scripts/backup.sh restore-pg \
    /opt/sentinelacs/backup/pg/2026-05-02_15.dump.age
```

**Sempre teste restore em ambiente isolado primeiro.** A app age key (`secrets/app.age.key`) precisa ser a mesma que foi usada para cifrar — sem ela, o backup é irrecuperável. Faça backup off-site dessa key também.

### Rotacionar secrets

A senha do banco / Redis / session_secret não pode mudar sozinho — invalida sessions e quebra conexão.

**Procedimento seguro de rotação:**

1. Edite `/opt/sentinelacs/config/.env` com os novos secrets
2. Atualize a string `DATABASE_URL`/`REDIS_URL` consistente com `POSTGRES_PASSWORD`/`REDIS_PASSWORD`
3. Recrie containers: `sudo -u sentinel /opt/sentinelacs/scripts/deploy.sh`
4. Para Postgres/Redis com volume já populado, é necessário primeiro alterar a senha **dentro** do banco antes de subir o app — caso contrário o app perde conexão. Procedure detalhado fica fora deste runbook (vai num doc separado quando necessário).

---

## GitHub Actions — workflows

| Workflow | Trigger | Quando rodar |
|---|---|---|
| `ci.yml` | push, PR | Sempre — lint + test + build da imagem |
| `provision.yml` | manual | **1×** para servidor fresco |
| `deploy.yml` | após CI verde em main, ou manual | Cada release |
| `seed-admin.yml` | manual | Quando precisar gerar nova senha de admin |

Para o resumo dos secrets/variables ver a tabela em [Pré-requisitos antes de rodar a workflow](#3-configurar-secretsvariables-no-github).

---

## Troubleshooting

### Containers não sobem

```bash
ssh celinet@177.72.177.102
sudo docker ps -a
sudo docker compose --env-file /opt/sentinelacs/config/.env \
    -f /opt/sentinelacs/docker-compose.yml -f /opt/sentinelacs/docker-compose.prod.yml \
    logs --tail 100
```

### Traefik não emite certificado

- DNS aponta para o servidor? `dig acs.empresa.com.br +short`
- Porta 80 acessível externamente? Firewall do datacenter pode bloquear.
- Logs do Traefik: `sudo docker logs sentinel-traefik`
- Rate-limit do Let's Encrypt: 5 falhas/hora — espere e tente novamente.

### Migrações falham

```bash
ssh celinet@177.72.177.102
sudo docker compose --env-file /opt/sentinelacs/config/.env \
    -f /opt/sentinelacs/docker-compose.yml \
    run --rm --entrypoint /usr/local/bin/migrate app -cmd status
```

### TimescaleDB não disponível

A migration `00004_init_telemetry.sql` requer a extension `timescaledb`. A imagem `timescale/timescaledb:latest-pg16` já vem com ela — se ainda assim falha, verifique:

```bash
sudo docker exec -it sentinel-postgres \
    psql -U sentinel -d sentinelacs -c '\dx'
```

Se não aparece, recrie o container postgres (cuidado: isso re-inicializa o banco se o volume estiver vazio).

### Disk full

```bash
df -h
sudo docker system prune -af --volumes      # ⚠️ pode apagar dados se mal usado
```

Backups antigos: `/opt/sentinelacs/backup/` — checa retention em `.env`.

---

## Pós-MVP — itens a configurar quando tiver as credenciais

- **Voalle**: preencher `VOALLE_*` em `.env` → `deploy.sh` re-rodar.
- **WhatsApp** (Evolution API): subir Evolution em outro container ou servidor; preencher `NOTIFIER_WHATSAPP_*`.
- **Telegram**: criar bot via @BotFather; preencher `NOTIFIER_TELEGRAM_BOT_TOKEN`.
- **SMTP**: preencher `NOTIFIER_SMTP_*`.
- **Backblaze B2** (backup off-site): criar bucket + app key; configurar `rclone config` no servidor; preencher `BACKUP_B2_*`.

---

## Checklist de validação pós-deploy

- [ ] `https://acs.empresa.com.br/healthz` → HTTP 200 com `postgres:up`, `redis:up`, `genieacs:up`
- [ ] `https://acs.empresa.com.br/login` → página de login carrega (cert válido)
- [ ] Login com admin → 2FA enrollment funcionando
- [ ] `/devices` → vazio (esperado — nenhum CPE conectou ainda)
- [ ] `/templates` → consegue criar profile de teste
- [ ] `/alerts` → consegue criar regra com DSL JSON
- [ ] `journalctl -t sentinel-backup` → cron ativo
- [ ] Apontar 1 CPE para `tr069://177.72.177.102:7547` → aparece em `/devices` em ≤5 min
