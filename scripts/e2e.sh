#!/usr/bin/env bash
# Browser smoke tests against a full local stack: a fake Hyperliquid (server/cmd/fakehl),
# the server on a throwaway database, and Playwright. Extra arguments go to Playwright.
#
#   scripts/e2e.sh            # needs the local Postgres from `make db-up` (:5433)
#   ADMIN_URL=postgres://... scripts/e2e.sh --headed
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
ADMIN_URL=${ADMIN_URL:-postgres://paperperps:paperperps@localhost:5433/paperperps?sslmode=disable}
DB=paperperps_e2e_$$
# Same server and credentials as ADMIN_URL, different database name.
DB_URL="${ADMIN_URL%%\?*}"
DB_URL="${DB_URL%/*}/$DB"
[[ $ADMIN_URL == *\?* ]] && DB_URL="$DB_URL?${ADMIN_URL#*\?}"
BIN=$(mktemp -d)
PIDS=()

cleanup() {
  for pid in "${PIDS[@]}"; do kill "$pid" 2>/dev/null || true; done
  psql "$ADMIN_URL" -qtAc "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null || true
}
trap cleanup EXIT

[ "${SKIP_WEB_BUILD:-}" ] || (cd "$ROOT/web" && npm run build >/dev/null)
(cd "$ROOT/server" && go build -o "$BIN/paperperps" ./cmd/paperperps && go build -o "$BIN/fakehl" ./cmd/fakehl)
psql "$ADMIN_URL" -qtAc "CREATE DATABASE $DB" >/dev/null

"$BIN/fakehl" -addr :9999 >"$BIN/fakehl.log" 2>&1 &
PIDS+=($!)
DATABASE_URL=$DB_URL PORT=8098 WEB_DIR="$ROOT/web/dist" MARKETS=BTC,ETH \
  HL_INFO_URL=http://localhost:9999/info HL_WS_URL=ws://localhost:9999/ws \
  "$BIN/paperperps" >"$BIN/server.log" 2>&1 &
PIDS+=($!)

for _ in $(seq 60); do
  curl -s localhost:8098/api/health | grep -q '"ok":true' && break
  sleep 0.5
done
curl -s localhost:8098/api/health | grep -q '"ok":true' || { echo "server not healthy; logs in $BIN"; cat "$BIN/server.log"; exit 1; }

cd "$ROOT/web"
E2E_BASE_URL=http://localhost:8098 E2E_FAKEHL=http://localhost:9999 npx playwright test "$@" || {
  echo "server log: $BIN/server.log"
  exit 1
}
