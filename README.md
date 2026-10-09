# PaperPerps

Paper-trade perpetual futures on live Hyperliquid market data. There's no login and no
wallet, and every visitor starts with $1,000 of mock USD. All orders are simulated; nothing
touches a chain.

- Product and architecture: [docs/plan.md](docs/plan.md)
- Deploying (Railway + paperperps.lol): [docs/deploy.md](docs/deploy.md)

## Layout

```
server/   Go backend: API, websocket, trading engine; serves the built frontend
  cmd/paperperps/        entry point
  internal/config/       env configuration
  internal/db/           Postgres pool + embedded goose migrations
  internal/leader/       advisory lock so only one engine runs
  internal/hyperliquid/  read-only Hyperliquid info + websocket client
  internal/market/       live market state (books, trades, tickers), candle cache
  internal/hub/          topic fan-out to browser websockets
  internal/httpapi/      HTTP routes, /ws, health check, SPA serving
web/      React + TypeScript + Vite frontend
docs/     plan and deploy docs
```

## Local development

Requires Go 1.26+, Node 22+ and Docker (Docker Desktop must be running).

```sh
make setup     # creates .env, installs web deps, starts Postgres on :5433
make server    # Go server on :8080 (applies migrations on start)
make web       # in another terminal: Vite on :5173, proxies /api to :8080
```

Open http://localhost:5173.

```sh
make test      # go vet + go test (incl. DB integration tests) + web lint/build
make docker    # build the production image
make help      # all targets
```

The DB integration tests need `TEST_DATABASE_URL`, which `.env` sets for the local
`paperperps_test` database. Without it they are skipped.

PaperPerps is not affiliated with Hyperliquid.
