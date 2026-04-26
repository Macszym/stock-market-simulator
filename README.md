# stock-market-simulator

REST API simulating a simplified stock exchange with a single bank as the sole liquidity source. Built for the Remitly internship recruitment task.

[![CI](https://github.com/Macszym/stock-market-simulator/actions/workflows/ci.yml/badge.svg)](https://github.com/Macszym/stock-market-simulator/actions/workflows/ci.yml)

## Quick Start

```bash
./scripts/run.sh
```

In another terminal:

```bash
curl http://localhost:8080/healthz
```

Requires Docker with Compose v2 (Docker Desktop 4.30+ or equivalent).

## Architecture

_TBD — documented as the system grows._

## API

Currently exposed:

- `GET /healthz` — liveness probe, returns `{"status":"ok"}`.

The full endpoint set (buy, sell, wallet queries, bank state, audit log, chaos) lands in subsequent commits.

## Design Decisions

_TBD — non-obvious choices (PostgreSQL as the single source of truth, distroless runtime, load-balanced HA, etc.) will be recorded here as they're implemented._

## Development

```bash
make            # show available targets
make run        # run server locally without Docker
make build      # build static binary into bin/server
make lint       # run golangci-lint
make test       # run all tests with race detector
make compose-up # start full stack (Postgres + app)
make compose-down
```

Go 1.26+ required for local builds. Docker required for the compose stack.

## Testing

```bash
make test
```

No tests yet; the suite grows alongside features.

## License

MIT — see [LICENSE](LICENSE).
