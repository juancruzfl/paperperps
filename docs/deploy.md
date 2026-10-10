# Deploying to Railway + paperperps.lol

PaperPerps runs as **one Railway service** (`app`, built from the root `Dockerfile`) plus
**Railway Postgres**. The design reasoning is in [plan.md §8.1](plan.md#81-deployment-railway--porkbun).

Service settings are configured in the Railway dashboard. The repo deliberately has no
`railway.toml`: Railway has deprecated it, and it stops working for legacy services on
2026-12-01.

## 1. Create the project

1. In Railway: **New Project → Deploy from GitHub repo → `juancruzfl/paperperps`**.
   Railway finds the root `Dockerfile` and builds with it.
2. In the same project: **New → Database → PostgreSQL**.
3. Rename the GitHub service to `app` (optional, but the docs assume it).

## 2. Variables (`app` → Variables)

| Variable | Value |
|---|---|
| `DATABASE_URL` | `${{Postgres.DATABASE_URL}}` (a reference to the Postgres service) |
| `HL_NETWORK` | `mainnet` (the default; public read-only data, nothing is signed) |

Everything else has sensible defaults (see `.env.example`). Don't set `PORT` or `WEB_DIR`:
Railway sets `PORT`, and the image sets `WEB_DIR`. `RAILWAY_GIT_COMMIT_SHA` is provided
automatically and shows up as `version` in `/api/status`.

Migrations run automatically at startup.

## 3. Service settings (`app` → Settings)

| Setting | Value | Why |
|---|---|---|
| Healthcheck path | `/healthz` | Passes once HTTP is up and the DB is reachable. |
| Replicas | `1` | The trading engine is stateful. A Postgres lock stops two engines from running, but a second replica would just sit on standby. |
| Restart policy | On failure | The process exits on purpose if it loses the engine lock. |
| Draining / teardown time | ~15s, if offered | Gives the old container time to shut down cleanly and release the lock. |

## 4. Check the Railway URL first

**Networking → Generate Domain** gives you a `*.up.railway.app` URL. Open it and check:

- The trading screen shows a live chart and order book, and the footer reads **Connected ·
  Hyperliquid mainnet data**.
- `/api/status` shows `"engine": "leader"`.
- `https://<railway-url>/healthz` returns `{"ok":true}`.
- Deploy logs show `migration applied` and `engine lock acquired`.

## 5. Custom domain (Porkbun)

1. Buy `paperperps.lol` on Porkbun.
2. In Railway: `app` → Settings → Networking → **Custom Domain**. Add `paperperps.lol`, then
   add `www.paperperps.lol`. Railway shows a DNS target for each, plus any verification
   records it needs.
3. In Porkbun: **Domain Management → paperperps.lol → DNS**.
   - Delete Porkbun's default parking records (the `ALIAS`/`CNAME` for the root and `*`).
   - Root (`paperperps.lol`): add an **ALIAS** record with the host left blank and the
     answer set to the Railway target. Apex domains can't use CNAME.
   - `www`: add a **CNAME** to the Railway target. The app redirects `www` to the apex.
   - Add any TXT verification records Railway showed.
4. Set up **email forwarding** for `legal@paperperps.lol` (Porkbun → Email Forwarding) to an
   inbox you read. The Terms and Privacy Policy list it as the contact address.
5. Wait for Railway to show the domains as verified. It issues TLS certificates
   automatically. DNS can take anywhere from a few minutes to a few hours.

## Deploys

Each push to `main` triggers a Railway deploy (and CI on GitHub). During a deploy:

1. The new container starts, runs migrations (guarded by a lock), and passes `/healthz`
   while still on **standby**.
2. Railway switches traffic and sends SIGTERM to the old container, which releases the
   engine lock.
3. The new container takes the lock and becomes **leader**.

Order entry is unavailable for a few seconds during step 3.
