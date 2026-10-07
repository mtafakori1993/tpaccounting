#!/usr/bin/env sh
# Creates a compressed PostgreSQL backup (custom format) in ./backups.
# Usage: scripts/backup.sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p backups
file="tpaccounting-$(date +%Y%m%d-%H%M%S).dump"
docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner' > "backups/$file"
echo "Database backup written to backups/$file"
echo "Attachments live in the 'attachments' volume; back them up with:"
echo "  docker run --rm -v tpaccounting_attachments:/data -v \"\$PWD/backups\":/b alpine tar czf /b/attachments-$(date +%Y%m%d).tgz -C /data ."
