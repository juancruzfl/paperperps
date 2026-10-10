# Operations runbook

How to watch, recover and back up a running PaperPerps. Setting up the deployment itself
is in [deploy.md](deploy.md); the design is in [plan.md](plan.md).

## Endpoints to watch

| Endpoint | Use | Healthy |
|---|---|---|
| `GET /healthz` | Railway's deploy health check. Only gates deploys. | `200` when HTTP is up and the database answers. Standbys pass too. |
| `GET /api/health` | Uptime monitors and people. | `200` with `"ok": true` when the database answers **and** prices are live. `503` otherwise, with `problems` listing why. |
| `GET /api/status` | Quick look. | `"engine": "leader"` on exactly one instance. |

`/api/health` also reports:
- **`feed`:** whether the feed is connected, seconds since the last Hyperliquid message,
  and which markets are stale.
- **`engine`:** whether it's running, accounts in memory, resting and trigger orders,
  event queue depth, dropped events, and the last funding hour settled.

## Alerts to set up

Railway doesn't watch application health for you, so set up two alerts:

1. **Uptime monitor on `/api/health`** (for example UptimeRobot or Better Stack, both
   with free tiers). Check every minute and alert after 2–3 failures. This catches the
   app being down, the database being unreachable, and the Hyperliquid feed going stale.
2. **Log alerts**, if your log tool supports them. The server logs structured JSON with an
   `alert` field:

   | `alert` value | Level | Meaning |
   |---|---|---|
   | `market_data_stale` | ERROR | The feed is disconnected or quiet for 60s+, or a market went stale. Trading on stale markets is halted. |
   | `market_data_stale_resolved` | INFO | Recovered. |

   Other ERROR lines worth a look: `engine: risk check`, `engine: funding`,
   `engine: fill resting order`, `engine: equity snapshots`.

## Incidents

**Prices stale (`market_data_stale`)**
- The engine already refuses orders and skips fills, liquidations, triggers and funding
  on stale markets. That's the safe state, so there's nothing to undo.
- Check [Hyperliquid's status](https://hyperliquid.xyz) and API. The server reconnects on
  its own with backoff up to 30s.
- If only testnet is affected and you run `HL_NETWORK=testnet`, switch to `mainnet` and
  redeploy.

**No instance is leader** (`/api/status` shows `standby` everywhere for more than a minute)
- Orders get `503 engine_unavailable`.
- A dead leader's lock is released when its database connection drops: immediately on a
  clean exit or crash, and within about 25s if its machine vanished (TCP keepalives on the
  lock session).
- If it's stuck longer, look for a leftover lock holder:
  ```sql
  SELECT pid, client_addr, backend_start, state
  FROM pg_stat_activity
  WHERE pid IN (SELECT pid FROM pg_locks WHERE locktype = 'advisory');
  ```
  and end it with `SELECT pg_terminate_backend(<pid>);`.

**Database unreachable**
- `/healthz` fails, so Railway stops routing to the instance and restarts it.
- The leader exits when its lock connection drops. Engine state is rebuilt from Postgres
  on start, so nothing in memory is lost that wasn't committed.

**A deploy**
- Expect 2–3 seconds without order entry while the engine lock moves to the new instance.
  Clients reconnect on their own.
- Practice it any time with `make drill`.

## Backups

- **Railway:** check the Postgres service's settings for scheduled backups on your plan,
  and turn them on if they're available.
- **Independently of Railway:**
  ```sh
  DATABASE_URL='<Railway Postgres DATABASE_PUBLIC_URL>' make backup   # → backups/paperperps-<time>.dump
  ```
  The script verifies the dump is readable. Run it before risky changes (migrations,
  config switches), and on a schedule somewhere that keeps the files off Railway.
- **Restore** into an empty database, then point `DATABASE_URL` at it:
  ```sh
  pg_restore --no-owner --no-acl --dbname "$TARGET_URL" backups/paperperps-<time>.dump
  ```
  A full dump-and-restore round trip was verified locally on 2026-10-09.

## Capacity

Measured locally on 2026-10-09 with `make loadtest`: one instance, Postgres in a container
on the same machine.

| | Result |
|---|---|
| Accounts / resting orders | 300 / 6,000, no errors |
| Market orders at 20/s | p50 18ms, p99 58ms |
| Limit orders placed back to back, 20 clients at once | ~100/s, p50 199ms |
| Websocket clients | 300, 415 msgs/s delivered, none dropped |
| `/api/me`, `/api/leaderboard` | p50 7ms / 1ms |

- **The limit is order throughput (~100/s):** the engine commits every command to Postgres
  before replying, one at a time. That's roughly 500 people each placing an order every
  five seconds.
- **To about 2.7× it** (measured ~270/s), add `options=-c%20synchronous_commit%3Doff` to
  `DATABASE_URL`. Postgres then confirms commits before flushing them to disk. If the
  *database* crashes, the last fraction of a second of trades can be lost; an app crash
  loses nothing. That's a reasonable trade for paper money if it's ever needed.
- **Equity snapshots** are kept 35 days and pruned daily. Each account keeps its last
  older snapshot, since that is a leaderboard baseline.

## Useful commands

```sh
make e2e        # browser smoke tests: full stack + fake Hyperliquid + Playwright (also in CI)
make loadtest   # load an isolated instance (see server/cmd/loadtest for flags)
make drill      # redeploy + crash drill against a throwaway database
make backup     # pg_dump with DATABASE_URL
```

## Config switches

| Variable | Effect | Notes |
|---|---|---|
| `HL_NETWORK` | `mainnet` (default) or `testnet` market data | Restart needed. Positions keep their entry prices, so expect a PnL jump. |
| `LIQUIDATION_MODE` | `forfeit` (default) or `market` | Applies to the next liquidation. |
| `MARKETS` | Comma-separated coins | A removed coin that people still hold becomes close-only (reduce-only orders, hidden from the market list) until it's no longer held at a restart. |
| `RESET_COOLDOWN`, `LEADERBOARD_MIN_TRADES`, `LEADERBOARD_MIN_AGE` | Rules | Restart needed. |

## Promises in the Terms and Privacy Policy

`/terms` and `/privacy` (text in `web/src/components/LegalPages.tsx`; operator, contact,
governing law and effective date in `web/src/lib/legal.ts`) commit to the following. Keep
them true, or update the documents and their effective date first.

| Promise | What keeps it true |
|---|---|
| **legal@paperperps.lol reaches someone** | Set up email forwarding for it at Porkbun (Domain → Email Forwarding) when the domain is bought. |
| **No cookies, analytics, ads or tracking** | Adding any analytics, error reporting or third-party script means updating the Privacy Policy first. |
| **IP addresses are not stored or logged by us** | The request log (`logRequests`) records method, path, status and duration only. Don't add IPs or headers to it. |
| **Self-serve deletion is immediate** | `DELETE /api/me` cascades over every table that references `accounts`. A new table with account data needs `ON DELETE CASCADE` too; `TestDeleteAccountRemovesEverything` checks the list. |
| **Deleted data leaves backups within 30 days** | Don't keep `make backup` dumps, or Railway backups, longer than 30 days. |
| **Equity snapshots are kept 35 days** | `engine.SnapshotRetention`. |
| **Service providers: Railway and Porkbun** | Name any new provider that handles requests or data in the Privacy Policy. |
