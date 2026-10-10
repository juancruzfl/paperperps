#!/usr/bin/env bash
# Redeploy and crash drill. Runs three server instances against a throwaway database and
# checks that trading survives:
#   1. a graceful deploy: a new instance starts, the old one gets SIGTERM;
#   2. a crash: the leader is killed with SIGKILL and a standby takes over.
# After each handover the account's orders and position must be intact and trading must
# work through the new leader.
#
# Usage: scripts/drill-redeploy.sh   (needs the local Postgres from `make db-up`, on :5433)
set -euo pipefail

ADMIN_URL=${ADMIN_URL:-postgres://paperperps:paperperps@localhost:5433/paperperps?sslmode=disable}
DB=paperperps_drill_$$
DB_URL=${ADMIN_URL/\/paperperps\?/\/$DB?}
BIN=$(mktemp -d)/paperperps
LOGS=$(mktemp -d)
PIDS=()

cleanup() {
  for pid in "${PIDS[@]}"; do kill -9 "$pid" 2>/dev/null || true; done
  psql "$ADMIN_URL" -qtAc "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null
}
trap cleanup EXIT

fail() { echo "FAIL: $*"; echo "logs in $LOGS"; exit 1; }
ok() { echo "  ok: $*"; }

start() { # port → pid
  DATABASE_URL=$DB_URL PORT=$1 "$BIN" >"$LOGS/$1.log" 2>&1 &
  PIDS+=($!)
  echo $!
}

engine() { curl -s "localhost:$1/api/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["engine"])' 2>/dev/null || echo down; }

wait_leader() { # port → seconds waited
  local start=$SECONDS
  until [ "$(engine "$1")" = leader ]; do
    [ $((SECONDS - start)) -gt 30 ] && fail "port $1 never became leader"
    sleep 0.2
  done
  echo $((SECONDS - start))
}

api() { # method port path [body]
  curl -s -X "$1" "localhost:$2$3" -H "Authorization: Bearer $SECRET" ${4:+-d "$4"}
}

check_state() { # port
  local me
  me=$(api GET "$1" /api/me)
  python3 - "$me" <<'EOF' || fail "state lost after handover"
import json, sys
me = json.loads(sys.argv[1])
pos = [p for p in me["positions"] if float(p["size"]) != 0]
assert len(me["openOrders"]) == 1, me["openOrders"]
assert len(pos) == 1 and pos[0]["coin"] == "BTC", pos
EOF
  ok "port $1 sees the resting order and the open position"
  local res
  res=$(api POST "$1" /api/orders '{"coin":"ETH","side":"buy","type":"market","sz":"0.005"}')
  echo "$res" | grep -q '"status":"filled"' || fail "order through new leader failed: $res"
  api POST "$1" /api/orders '{"coin":"ETH","side":"sell","type":"market","sz":"0.005","reduceOnly":true}' >/dev/null
  ok "new leader on port $1 accepts orders"
}

echo "building…"
(cd "$(dirname "$0")/../server" && go build -o "$BIN" ./cmd/paperperps)
psql "$ADMIN_URL" -qtAc "CREATE DATABASE $DB" >/dev/null

echo "1. first instance"
A=$(start 8091)
wait_leader 8091 >/dev/null
until curl -s localhost:8091/api/health | grep -q '"ok":true'; do sleep 0.5; done
ok "8091 is leader with live prices"

SECRET=$(curl -s -X POST localhost:8091/api/accounts | python3 -c 'import json,sys; print(json.load(sys.stdin)["secret"])')
api POST 8091 /api/orders '{"coin":"BTC","side":"buy","type":"market","sz":"0.0002"}' | grep -q filled || fail "open position"
api POST 8091 /api/orders '{"coin":"BTC","side":"buy","type":"limit","sz":"0.0002","px":"50000"}' | grep -q '"status":"open"' || fail "resting order"
ok "account with a BTC position and a resting order"

echo "2. graceful deploy: new instance, then SIGTERM the old one"
B=$(start 8092)
sleep 2
[ "$(engine 8092)" = standby ] || fail "8092 should wait as standby"
curl -sf localhost:8092/healthz >/dev/null || fail "standby must pass /healthz (Railway gates on it)"
api POST 8092 /api/orders '{"coin":"ETH","side":"buy","type":"market","sz":"0.005"}' | grep -q engine_unavailable || fail "standby must refuse orders"
ok "8092 is a healthy standby that refuses orders with engine_unavailable"
kill -TERM "$A"
echo "  handover took $(wait_leader 8092)s"
check_state 8092

echo "3. crash: SIGKILL the leader"
C=$(start 8093)
sleep 2
[ "$(engine 8093)" = standby ] || fail "8093 should wait as standby"
kill -9 "$B"
echo "  takeover took $(wait_leader 8093)s"
check_state 8093

echo "4. funding is settled once per hour, whoever is leader"
dup=$(psql "${DB_URL%%\?*}" -qtAc "SELECT count(*) FROM (SELECT 1 FROM funding_payments GROUP BY account_id, coin, funding_hour HAVING count(*) > 1) d")
[ "$dup" = 0 ] || fail "duplicate funding payments"
ok "no duplicate funding payments"

echo "PASS"
