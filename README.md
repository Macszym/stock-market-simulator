# stock-market-simulator

REST API simulating a simplified stock exchange with a single bank as the sole liquidity source. Built for the Remitly internship recruitment task.

[![CI](https://github.com/Macszym/stock-market-simulator/actions/workflows/ci.yml/badge.svg)](https://github.com/Macszym/stock-market-simulator/actions/workflows/ci.yml)

## Quick Start

```bash
./scripts/run.sh
```

By default this launches Caddy + 3 app replicas + Postgres. To override the host port or replica count:

```bash
./scripts/run.sh 9090        # 3 replicas on :9090
./scripts/run.sh 9090 5      # 5 replicas on :9090
```

In another terminal:

```bash
curl http://localhost:8080/healthz
curl -X POST http://localhost:8080/stocks \
  -d '{"stocks":[{"name":"AAPL","quantity":100}]}'
curl http://localhost:8080/stocks
```

Requires Docker with Compose v2 (Docker Desktop 4.30+ or equivalent). On Windows, run the shell scripts from Git Bash or WSL2.

## Architecture

```mermaid
graph LR
    Client --> Caddy[Caddy<br/>load balancer]
    Caddy --> A1[app replica 1]
    Caddy --> A2[app replica 2]
    Caddy --> AN[app replica N]
    A1 --> DB[(Postgres)]
    A2 --> DB
    AN --> DB
```

The Go API itself is layered with explicit boundaries:

- `internal/domain` - pure data types (`Stock`, `Wallet`, `AuditEntry`, `OperationType`) and sentinel errors. No IO, no DB, no HTTP.
- `internal/storage` - Postgres-backed persistence (`Postgres` struct with pgxpool). Maps DB-specific concerns (`pgx.ErrNoRows`, transactions) to domain errors.
- `internal/service` - application use cases. Owns input validation. Defines the `Repository` interface (consumer-defined) so tests can supply a fake without importing `storage`.
- `internal/api` - HTTP layer. Owns wire shapes (envelope DTOs that diverge from domain), error mapping, and route registration via Go 1.22+ pattern matching.
- `cmd/server` - composition root. Wires `storage → service → api`, runs goose migrations on startup, handles graceful shutdown.
- `migrations` - embedded SQL files (`//go:embed *.sql`); shipped inside the binary, applied at startup.

## High Availability

The compose stack runs N app replicas (default 3) behind Caddy as a reverse proxy and load balancer. The replica count is parametrised via `APP_REPLICAS` and the host port via `PORT`; only Caddy publishes a port to the host.

Caddy uses DNS-based service discovery: it queries the Docker DNS resolver every 5 seconds for A-records of the `app` service, which Compose returns once per running replica. When `POST /chaos` kills a replica:

1. Docker's `restart: unless-stopped` policy re-spawns the container.
2. Caddy's passive health check (`fail_duration 10s`, `max_fails 1`) parks the dead replica after the first failed request.
3. `lb_try_duration 5s` retries the failed upstream attempt within the same client request, so the failover is invisible to the client.
4. The DNS refresh window picks up the restarted replica without reloading Caddy.

Postgres is intentionally **not** replicated. Real database HA (streaming replication, failover orchestration, split-brain resolution) is well beyond the scope of this recruitment task; the rationale is in [ADR 0001](docs/adr/0001-ha-via-shared-db.md), and the LB design is in [ADR 0002](docs/adr/0002-caddy-dns-discovery.md).

## API

All endpoints accept and return JSON. Wallet IDs and stock names are arbitrary strings.

| Method | Path | Status | Notes |
|---|---|---|---|
| `GET`  | `/healthz` | ✓ | Liveness probe, returns `{"status":"ok"}` |
| `GET`  | `/stocks` | ✓ | Returns `{"stocks":[{"name":"...","quantity":N}]}` |
| `POST` | `/stocks` | ✓ | Body `{"stocks":[...]}`. Replaces the entire bank. Returns 200 with no body |
| `GET`  | `/wallets/{wallet_id}` | ✓ | Returns `{"id":"...","stocks":[...]}` or 404 if missing |
| `GET`  | `/wallets/{wallet_id}/stocks/{stock_name}` | ✓ | Returns a bare JSON number (e.g. `99`); 0 if the wallet has no holding for that stock |
| `GET`  | `/log` | ✓ | Returns `{"log":[{"type":"buy","wallet_id":"...","stock_name":"..."}]}`, ordered by occurrence |
| `POST` | `/wallets/{wallet_id}/stocks/{stock_name}` | ✓ | Body `{"type":"buy"\|"sell"}`. One unit moves between bank and wallet, audit log entry written in the same DB transaction. Auto-creates the wallet on first buy. Returns 200 with empty body on success |
| `POST` | `/chaos` | ✓ | Returns 202 with `{"message":"shutting down"}` and triggers graceful self-shutdown - same code path as SIGTERM. Used to exercise HA failover |

### Example

```bash
# Set the bank
curl -X POST http://localhost:8080/stocks \
  -d '{"stocks":[{"name":"AAPL","quantity":100},{"name":"MSFT","quantity":50}]}'

# Inspect the bank
curl http://localhost:8080/stocks
# {"stocks":[{"name":"AAPL","quantity":100},{"name":"MSFT","quantity":50}]}

# A missing wallet
curl -i http://localhost:8080/wallets/does-not-exist
# HTTP/1.1 404 Not Found
# {"error":"wallet not found","code":"WALLET_NOT_FOUND"}

# Buy and sell
curl -X POST http://localhost:8080/wallets/w1/stocks/AAPL -d '{"type":"buy"}'
curl -X POST http://localhost:8080/wallets/w1/stocks/AAPL -d '{"type":"sell"}'

# Audit log captures successful operations only
curl http://localhost:8080/log
# {"log":[{"type":"buy","wallet_id":"w1","stock_name":"AAPL"},{"type":"sell","wallet_id":"w1","stock_name":"AAPL"}]}

# Trigger a chaos shutdown (instance restarts under compose's restart policy)
curl -X POST http://localhost:8080/chaos
# HTTP/1.1 202 Accepted
# {"message":"shutting down"}
```

## API Errors

All error responses share the same shape: `{"error":"human message","code":"STABLE_ENUM"}`. The code is the contract; the message is informational.

| Status | Code | Trigger |
|---|---|---|
| 400 | `INVALID_REQUEST` | Malformed JSON or missing required field |
| 400 | `INVALID_OPERATION` | `type` outside `{"buy","sell"}` |
| 400 | `INVALID_QUANTITY` | Negative quantity in `POST /stocks` |
| 400 | `EMPTY_STOCK_NAME` | Empty `name` in `POST /stocks` |
| 400 | `DUPLICATE_STOCK_NAME` | Same `name` listed twice in `POST /stocks` |
| 400 | `INSUFFICIENT_BANK_STOCK` | Buy when bank has 0 of that stock |
| 400 | `INSUFFICIENT_WALLET_STOCK` | Sell when wallet has 0 of that stock |
| 404 | `WALLET_NOT_FOUND` | `GET /wallets/{id}` or `/wallets/{id}/stocks/{name}` for an unknown wallet |
| 404 | `STOCK_NOT_FOUND` | Buy or sell of a stock not present in the bank |
| 500 | `INTERNAL_ERROR` | Catch-all; the original error is logged server-side |

## Design Decisions

- **Single Postgres as source of truth.** Wallet state, bank state and audit log live in one DB so buy/sell + audit can be one transaction - atomicity without 2-phase commit or eventual consistency.
- **`POST /stocks` rewrites the bank with `TRUNCATE` + `INSERT` in a transaction.** Faster than `DELETE` (no per-row triggers, less WAL), naturally matches "overwrite" semantics, transactional rollback if the inserts fail.
- **No FK between `wallet_stocks.stock_name` and `bank_stocks(name)`.** A FK with `CASCADE` would wipe wallets on `POST /stocks`; without `CASCADE` the `TRUNCATE` would fail. Bank is treated as a fluctuating catalog, wallet_stocks as ownership history.
- **`audit_log.id BIGINT GENERATED ALWAYS AS IDENTITY`.** SQL standard since Postgres 10. `ALWAYS` blocks application-supplied IDs (requires explicit `OVERRIDING SYSTEM VALUE`), eliminating a class of bugs where dump/restore desynchronises sequences.
- **Goose migrations embedded in the binary** (`//go:embed *.sql` + `goose.SetBaseFS`). The distroless runtime ships migrations inside the binary; nobody can swap SQL files post-deploy.
- **Hybrid JSON tagging.** `Stock` and `Wallet` carry JSON tags inline (their wire shape is 1:1 with the domain). `AuditEntry` has no tags - its response shape (3 fields, lowercase `type`) diverges, so the HTTP layer owns a dedicated DTO. Envelope shapes (`{"stocks":[...]}`, `{"log":[...]}`) live in the api package as request/response wrappers, not as domain concepts.
- **`Repository` interface defined in `internal/service`.** Consumer-defined interfaces are idiomatic Go: the service declares what it needs, storage need not export an interface, and tests can drop in an in-memory fake without importing storage.
- **Sentinel errors in `internal/domain` + `mapError` in `internal/api`.** Cross-layer errors are sentinels (testable with `errors.Is`); the HTTP layer owns status code and stable-enum mapping. Server-side logging is gated on status >= 500 so 4xx client mistakes do not flood logs.
- **Buy/sell and the audit log share one transaction.** The state mutation (UPDATE on `bank_stocks` + UPSERT on `wallet_stocks`) and the `INSERT` into `audit_log` live inside one `pool.Begin`/`Commit`. Either the operation and the audit row both persist, or neither does. This is the load-bearing guarantee that lets the audit log be trusted as a record of what actually happened - the reason Postgres was picked as the only datastore in the first place.
- **Atomic decrement uses `UPDATE ... WHERE quantity > 0`, not `SELECT ... FOR UPDATE` followed by application-level checks.** The conditional update is atomic at the SQL level; zero rows affected means depletion. A `SELECT FOR UPDATE` is still issued first, but only to distinguish "stock unknown to the bank" (404) from "stock exists but is depleted" (400). Read-Committed isolation is sufficient because the per-row lock and conditional update together prevent lost updates without forcing the retry loops a `Serializable` level would require.
- **Sell never auto-creates a wallet.** Buy treats the wallet as auto-created on first acquisition (matching the spec). Sell on a missing wallet returns 400 `INSUFFICIENT_WALLET_STOCK` - the same code as sell on an empty holding. There is no meaningful "first sale" without prior state, so the symmetry with buy is intentionally broken.
- **`POST /chaos` triggers the same shutdown path as SIGTERM.** The handler returns 202 immediately, then a goroutine sleeps 100 ms (so the response leaves the socket) and calls the cancel function returned by `signal.NotifyContext`. The main goroutine's `<-ctx.Done()` branch runs `httpSrv.Shutdown` exactly as it would on SIGTERM - one shutdown code path, exercised two ways.
- **Caddy as the load balancer, not nginx or Traefik.** Caddy's Caddyfile is dense enough to express the whole LB config (dynamic upstreams, round-robin, retry window, passive health) in 12 lines, with no template engine and no startup script. nginx would require a config template rendered at boot and a manual reload on rescale; Traefik is heavier than the requirements warrant. Caddy v2 is also one of the few mainstream proxies whose Caddyfile directly supports DNS-based dynamic upstreams without a plugin.
- **DNS-based service discovery instead of statically named replicas.** Static names (`app1`, `app2`, ...) require `container_name` per replica, which Compose forbids together with `deploy.replicas`. Resolving the `app` service name via Docker's embedded DNS gives one A-record per live replica; Caddy re-queries every 5 seconds, so scale-up and post-chaos restarts need no Caddy reload. Trade-off: Caddy disables active health checks under dynamic upstreams, so resilience leans on passive health (`fail_duration`) plus per-request retry (`lb_try_duration`). See [ADR 0002](docs/adr/0002-caddy-dns-discovery.md).
- **No HA for Postgres.** The same DB is reachable from every app replica, which keeps the audit-log invariant trivially correct (every successful buy/sell still commits in one transaction with its audit row, regardless of which replica handled the call) but leaves Postgres as a single point of failure. Treating real DB HA seriously means streaming replication, a cluster manager, and a connection router - all out of scope here. See [ADR 0001](docs/adr/0001-ha-via-shared-db.md).

## Development

```bash
make                    # show all targets
make run                # run server locally without Docker
make build              # build static binary into bin/server
make lint               # run golangci-lint
make test               # unit tests with race detector
make test-integration   # integration tests against Postgres in testcontainers
make test-e2e           # end-to-end chaos resilience test (needs Docker, curl, jq)
make compose-up         # full stack (Caddy + N app replicas + Postgres)
make compose-down
make compose-logs       # tail logs from the running stack
make scale REPLICAS=5   # scale app to 5 replicas (default 3)
make db-shell           # psql against the running compose Postgres
```

Go 1.26+ for local builds. Docker required for the compose stack and integration tests.

## Testing

Two loops:

- **Unit tests** (`make test`) - service layer with an in-memory fake repository; finishes in milliseconds. Run on every commit.
- **Integration tests** (`make test-integration`) - repository against a real Postgres 18.3 launched by testcontainers-go, schema applied via the same embedded goose migrations the binary uses. Container start is the bulk of the time (~7s cold, ~1s warm); each test uses `TRUNCATE ... RESTART IDENTITY CASCADE` for a deterministic state without paying container startup between tests.
- **End-to-end chaos test** (`make test-e2e`) - `tests/e2e/chaos_test.sh` brings up the full HA stack via `docker compose`, places 50 buys, fires `POST /chaos`, places another 50 buys after the killed replica is gone, and asserts the audit log carries all 100 entries while the bank is fully drained. Requires Docker, `curl` and `jq`.

All three suites run in CI; e2e is gated on integration passing.

### Concurrency tests

`tests/integration/concurrency_test.go` runs two scenarios under the `-race` detector:

- **Race for limited bank stock.** The bank holds 50 of one symbol, 100 goroutines each call `BuyStock` for that symbol into a distinct wallet. Postgres' per-row `SELECT FOR UPDATE` plus conditional `UPDATE WHERE quantity > 0` must serialize the contention so that exactly 50 buys succeed, exactly 50 fail with `INSUFFICIENT_BANK_STOCK`, the bank ends at 0, the wallets together hold 50 units, and the audit log carries exactly 50 BUY entries.
- **Mixed buy/sell preserves the total.** The bank holds 100 of one symbol and a preloaded wallet holds another 100 of the same symbol. 100 concurrent buys (each into a fresh wallet) and 100 concurrent sells (all from the preloaded wallet) run interleaved. After the dust settles, `bank.quantity + sum(wallet_stocks.quantity)` must equal 200 - no stock created, none destroyed - and the audit log row count must equal the number of successful operations, broken down correctly between BUY and SELL.

These two tests are the closest we get to proving the FinTech contract: stock cannot evaporate, cannot multiply, and the audit log mirrors reality exactly.

## License

MIT - see [LICENSE](LICENSE).
