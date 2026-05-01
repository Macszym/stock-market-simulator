# 0006. Goose migrations embedded in the binary

Date: 2026-05-01

## Status

Accepted

## Context

The task requires the system to start with a single command, and the runtime image is distroless (`gcr.io/distroless/static-debian13:nonroot`): no shell, no package manager, no migration CLI. The schema has to land in Postgres before the first request hits a handler, but the moving parts available at runtime are limited to whatever the application binary itself can do.

The alternatives considered:

- **`migrate` binary in a separate compose service.** A small init container that runs `migrate -path ... up` before the app starts, gated by `depends_on`. Reliable, but adds a second container with its own SQL mount and `depends_on` ordering, which inflates the operator-facing surface for a single-command demo.
- **golang-migrate as a library.** Similar shape to goose, comparable feature set. The deciding factor was that goose's `goose.SetBaseFS(embed.FS)` integrates with `//go:embed` in one line, while golang-migrate's `iofs` source driver needs more wiring.
- **Plain `pgx` running SQL on startup.** Smallest dependency footprint, but with N application replicas (see ADR 0001) the race-safe "apply this exactly once" logic becomes the project's job rather than a library's.

## Decision

`migrations/embed.go` ships every `.sql` file under `migrations/` inside the binary via `//go:embed *.sql`:

```go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

`cmd/server/main.go` calls `goose.SetBaseFS(migrations.FS)` followed by `goose.Up(db, ".")` during startup, before the HTTP listener is registered. The pgxpool pool is bridged to `*sql.DB` via `stdlib.OpenDBFromPool` (`pgx/v5/stdlib`) only for the migration step, with `defer db.Close()` releasing the wrapper while the underlying pool stays live for the application.

## Consequences

- One binary, one command, no external migration tooling. `./scripts/run.sh` brings the system up; the application is its own migrator.
- N replicas can start in parallel safely. Goose tracks the applied version in `goose_db_version` and acquires a Postgres session-level advisory lock (`pg_try_advisory_lock`) around the migration step; the first replica applies the change, the rest block, observe the bumped version, and exit with `no migrations to run`. Verified in compose: app-2 started 27 ms after app-1 and reported `current version: 1` without re-applying.
- Migration files cannot be swapped post-deploy. They are compiled into the binary; the only way to ship a new migration is a new build, which removes a class of "someone edited the SQL file in the data volume" incidents.
- Adding a migration is a two-step diff: drop a `migrations/00002_*.sql` file using goose's `-- +goose Up` / `-- +goose Down` convention, rebuild. CI does not need a separate migration job; the next deploy picks it up at startup.
- Rolling back is intentionally not part of the startup path. `goose down` exists as an operator action against a running database; encoding it in the boot sequence would let an accidentally-shipped revert wipe production state on container restart.
