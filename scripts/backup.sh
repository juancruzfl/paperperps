#!/usr/bin/env bash
# Dumps the PaperPerps database to a timestamped file and checks the file is readable.
#
#   DATABASE_URL=postgres://... scripts/backup.sh [output-dir]
#
# On Railway use the Postgres service's *public* connection URL (Variables →
# DATABASE_PUBLIC_URL), since the private one only resolves inside Railway.
# Restore into an empty database with:
#   pg_restore --no-owner --no-acl --dbname "$TARGET_URL" <file>
set -euo pipefail
: "${DATABASE_URL:?set DATABASE_URL to the database to back up}"
dir=${1:-backups}
mkdir -p "$dir"
file="$dir/paperperps-$(date -u +%Y%m%dT%H%M%SZ).dump"
pg_dump --format=custom --no-owner --no-acl --file "$file" "$DATABASE_URL"
pg_restore --list "$file" >/dev/null # fails if the dump is unreadable
echo "$file ($(du -h "$file" | cut -f1))"
