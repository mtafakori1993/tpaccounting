#!/usr/bin/env sh
# Restores a backup created by scripts/backup.sh. THIS REPLACES ALL CURRENT DATA.
# Usage: scripts/restore.sh backups/tpaccounting-YYYYMMDD-HHMMSS.dump
set -eu
cd "$(dirname "$0")/.."
[ $# -eq 1 ] && [ -f "$1" ] || { echo "usage: $0 <backup.dump>"; exit 1; }
docker compose stop app
docker compose exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction' < "$1"
docker compose start app
echo "Restored $1"
