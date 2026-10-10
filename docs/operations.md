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
   | `admin_action` | WARN | Someone changed an account through the admin API. If it wasn't you, see [Admin key compromised](#admin-key-compromised). |
   | `admin_auth_failed` | WARN | A request carried admin signature headers but failed the check (wrong key, expired, replayed). A few from your own clock drift are harmless; a stream of them means someone is trying. |
   | `admin_enabled` | WARN | Logged at startup when `ADMIN_PUBLIC_KEY` is set. |

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

## Admin tool

`paperperps-admin` moderates accounts from your machine: rename offensive handles, hide
accounts from the leaderboard, suspend, force-reset or delete them, and read the audit log.
It calls `/api/admin/*` on the live server.

### How it's secured

- **Off unless configured.** The routes are only registered when `ADMIN_PUBLIC_KEY` is set.
- **Signed requests, not a password.** Every request is signed with an Ed25519 private key
  that only exists on your machine. Railway holds only the public key, which can verify
  signatures but not make them, so leaking Railway's variables doesn't grant admin access.
  The signature covers the method, host, path and query, body, a timestamp and a random nonce.
- **No replays.** Signatures expire after 60 seconds, and each nonce works once
  (`admin_nonces` table, shared by every instance).
- **Invisible to probing.** A missing, wrong, expired or replayed signature gets exactly the
  same 404 as any unknown `/api/` path, also for wrong HTTP methods.
  `TestAdminRoutesAreHidden` checks every route in `adminRoutes`, including future ones.
- **Rate limited** per IP (1/s, bursts of 20) before the signature is checked.
- **Small blast radius.** There's no "set balance", SQL console or bulk action. Each call
  names one account.
- **Audited.** Every change writes an `admin_actions` row (action, account ID, handle at the
  time, reason) in the same transaction as the change, and logs `alert=admin_action`. A
  reason is required.
- **No browser UI**, so a bug in the site can't steal or use admin credentials.

The real perimeter is your accounts: keep 2FA on Railway and GitHub (whoever can push to
`main` or edit Railway can run any code in production) and protect `main` with required CI.

### Set up

```sh
make admin                       # builds bin/paperperps-admin
./bin/paperperps-admin keygen    # writes ~/.config/paperperps/admin.key (mode 600), prints the public key
```

Set `ADMIN_PUBLIC_KEY` on the Railway service to the printed value and redeploy. Back up the
private key in your password manager. To keep it off disk entirely, store it there only and
run commands with `PAPERPERPS_ADMIN_KEY="$(pass show paperperps/admin)" ./bin/paperperps-admin …`
(any password manager CLI works), then delete the key file.

The CLI talks to `https://paperperps.lol` unless `PAPERPERPS_URL` says otherwise. It refuses
plain `http://` except for localhost. Your clock must be within a minute of the server's.

### Commands

```sh
paperperps-admin find narwhal                          # search handles (or an exact account ID)
paperperps-admin flagged                               # existing handles the profanity filter rejects
paperperps-admin show swift-narwhal-024                # balance, PnL, recent fills, past admin actions
paperperps-admin rename swift-narwhal-024 --reason "slur in handle"          # random new handle
paperperps-admin rename swift-narwhal-024 trader42 --reason "impersonation"  # or a chosen one
paperperps-admin hide swift-narwhal-024 --reason "wash trading"              # off the leaderboard
paperperps-admin suspend swift-narwhal-024 --reason "multi-accounting"
paperperps-admin reset swift-narwhal-024 --reason "exploited a pricing bug"
paperperps-admin delete swift-narwhal-024 --reason "illegal content"         # asks you to type the handle
paperperps-admin log --limit 20
```

`unhide` and `unsuspend` undo `hide` and `suspend`. Add `--json` to `find`, `show` and `log`
for raw output.

What each action does to the account:

| Action | Effect |
|---|---|
| rename | New handle. The user can change it again unless suspended. |
| hide | Off every leaderboard. Their "show me" toggle can't bring it back; they see a note saying a moderator hid them. |
| suspend | Open orders that could add to a position are cancelled. From then on only reduce-only orders: they can close what they hold but not open anything, reset, or change their handle. Also off the leaderboard. A banner tells them, with the contact address. |
| reset | A fresh run, like a self-reset, ignoring the cooldown and suspension. The ended run's PnL still counts toward their score. |
| delete | Same as self-serve deletion. The audit log keeps the account ID, handle and reason. |

Suspend, reset and delete go through the trading engine; if it's restarting you get a 503,
so try again in a few seconds.

### Admin key compromised

1. Delete `ADMIN_PUBLIC_KEY` in Railway (or replace it with a new key's) and redeploy. Old
   signatures stop working as soon as the new container is up.
2. Read the damage: `SELECT * FROM admin_actions ORDER BY id DESC` with `railway connect`,
   or `paperperps-admin log` with the new key.
3. Undo what you can (`unsuspend`, `unhide`, `rename`). Resets and deletions can't be undone
   except from a backup.
4. Work out how the key leaked before making a new one.

## Link previews and grouped books

- **Profile previews.** `/u/<handle>` pages are served with that trader's Open Graph and
  Twitter tags, and `GET /api/traders/<handle>/card.png` renders their card server-side
  (`internal/sharecard`, about 80 ms; cached 5 minutes). Visibility is checked on every
  request, so hiding takes effect at once. Chat apps cache previews on their side.
- **Upstream connections.** Besides the main Hyperliquid feed, the server keeps three more
  websocket connections for the order book groupings (4, 3 and 2 significant figures).
  They're display-only: if one drops, that grouping freezes until it reconnects, and
  trading, health and the stale-feed alert are unaffected.

## Handle filter

New handles, and admin renames, go through a profanity filter
(`server/internal/accounts/profanity.go`). It sees through digits for letters (`sh1t`),
separators (`f-u-c-k`) and stretched letters (`fuuuck`). Short terms that occur inside
ordinary words (ass, rape, spic) are blocked only as whole words, so "classic", "grape" and
"spice" still work. Users get `offensive_handle` with a plain explanation.

The filter only checks handles when they're set. After adding terms, or once after the
first deploy with the filter, run `paperperps-admin flagged` and rename what it lists.
`TestOffensiveAllows` lists words that must keep working; add to it when you add a term.

## Dependency scans

`.github/workflows/security.yml` runs `govulncheck` on the server and `npm audit` on the
frontend's shipped dependencies, on every push and pull request and every Monday. GitHub
emails you when the weekly run fails: update the named module (`go get -u <module>` in
`server/`, or `npm update <package>` in `web/`) and redeploy.

When the finding is in the Go standard library (`crypto/tls`, `net/http`…), the fix is a
Go patch release: raise the `go` line in `server/go.mod` to the fixed version (govulncheck
prints it as "Fixed in"). CI and the Docker build then use at least that version, and
update your local Go too (`sudo dnf upgrade golang` on Fedora).

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
| **Profiles show only leaderboard accounts, and no balances, orders or keys** | `accounts.PublicAccount` requires opt-in and no moderation; `TestTraderProfile` checks hiding and the fields left out. New profile fields need a line in the Privacy Policy's "What's public". |
| **Moderation records outlive deleted accounts** | `admin_actions` has no foreign key to `accounts` on purpose. It keeps only account ID, handle, action and reason; keep it that way (no balances, no trade data). |
| **Service providers: Railway and Porkbun** | Name any new provider that handles requests or data in the Privacy Policy. |
