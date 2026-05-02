#!/usr/bin/env bash
#
# backup.sh — backup horário (PG) ou diário (Mongo), cifrado com age.
#
# Uso:
#   backup.sh pg       # pg_dump → gzip → age → /backup/pg/YYYY-MM-DD_HH.dump.age
#   backup.sh mongo    # mongodump → tar.gz → age → /backup/mongo/YYYY-MM-DD.tar.age
#   backup.sh restore-pg <arquivo.age>      # restore manual de PG
#   backup.sh restore-mongo <arquivo.age>   # restore manual de Mongo
#
# Off-site: se BACKUP_B2_BUCKET preenchido, faz upload para Backblaze B2
# (s3-compatible) usando rclone se disponível. Sem rclone, fica só local.
#
# Retenção: BACKUP_RETENTION_DAYS (default 30). Arquivos mais velhos são apagados.

set -euo pipefail

ROOT=/opt/sentinelacs
BACKUP_DIR="$ROOT/backup"
SECRETS_DIR="$ROOT/secrets"
CFG="$ROOT/config/.env"

source "$CFG"
RETENTION="${BACKUP_RETENTION_DAYS:-30}"

# Pubkey extraída da age key (linha "# public key: age1...").
AGE_PUB=$(grep -oE 'age1[0-9a-z]+' "$SECRETS_DIR/age.key" | head -1)
[[ -n "$AGE_PUB" ]] || { echo "backup: age public key não encontrada em $SECRETS_DIR/age.key"; exit 1; }

CMD="${1:-pg}"
shift || true

case "$CMD" in
    pg)
        TS=$(date +%Y-%m-%d_%H)
        OUT="$BACKUP_DIR/pg/${TS}.dump.age"
        mkdir -p "$BACKUP_DIR/pg"

        echo "[backup-pg] dumping → $OUT"
        # pg_dump custom format (compressível, restore seletivo).
        # Roda DENTRO do container postgres para usar o pg_dump da versão certa.
        docker exec sentinel-postgres \
            pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc \
        | gzip -9 \
        | age --encrypt -r "$AGE_PUB" -o "$OUT"

        echo "[backup-pg] tamanho: $(du -h "$OUT" | awk '{print $1}')"
        ;;

    mongo)
        TS=$(date +%Y-%m-%d)
        OUT="$BACKUP_DIR/mongo/${TS}.tar.age"
        mkdir -p "$BACKUP_DIR/mongo"

        echo "[backup-mongo] dumping → $OUT"
        docker exec sentinel-mongo \
            mongodump --quiet --archive --gzip --db genieacs \
        | age --encrypt -r "$AGE_PUB" -o "$OUT"

        echo "[backup-mongo] tamanho: $(du -h "$OUT" | awk '{print $1}')"
        ;;

    restore-pg)
        SRC="${1:-}"
        [[ -f "$SRC" ]] || { echo "informe arquivo .dump.age existente"; exit 1; }
        echo "[restore-pg] ⚠️  vai SOBRESCREVER o banco $POSTGRES_DB. Confirma? (yes/N)"
        read -r CONFIRM
        [[ "$CONFIRM" == "yes" ]] || { echo "abortado"; exit 1; }

        age --decrypt -i "$SECRETS_DIR/age.key" "$SRC" \
        | gunzip \
        | docker exec -i sentinel-postgres \
            pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner

        echo "[restore-pg] ✓"
        ;;

    restore-mongo)
        SRC="${1:-}"
        [[ -f "$SRC" ]] || { echo "informe arquivo .tar.age existente"; exit 1; }
        echo "[restore-mongo] ⚠️  vai SOBRESCREVER o db genieacs. Confirma? (yes/N)"
        read -r CONFIRM
        [[ "$CONFIRM" == "yes" ]] || { echo "abortado"; exit 1; }

        age --decrypt -i "$SECRETS_DIR/age.key" "$SRC" \
        | docker exec -i sentinel-mongo \
            mongorestore --quiet --archive --gzip --drop

        echo "[restore-mongo] ✓"
        ;;

    *)
        echo "uso: $0 {pg|mongo|restore-pg <file>|restore-mongo <file>}"
        exit 1
        ;;
esac

# ──────────── Off-site (B2/S3 via rclone) ────────────
# Configure rclone uma vez:  rclone config  →  remote=b2 (Backblaze)
if [[ "$CMD" == "pg" || "$CMD" == "mongo" ]]; then
    if [[ -n "${BACKUP_B2_BUCKET:-}" ]] && command -v rclone &>/dev/null; then
        echo "[backup] sync off-site → $BACKUP_B2_BUCKET"
        rclone --quiet copy "$BACKUP_DIR" "b2:$BACKUP_B2_BUCKET/sentinelacs/" || \
            echo "[backup] off-site falhou — investigar; backup local OK"
    fi

    # Retenção local
    find "$BACKUP_DIR/pg"    -name '*.dump.age' -mtime +$RETENTION -delete 2>/dev/null || true
    find "$BACKUP_DIR/mongo" -name '*.tar.age'  -mtime +$RETENTION -delete 2>/dev/null || true
fi
