# 0001. High availability via replicated app and shared Postgres

Date: 2026-05-01

## Status

Accepted

## Context

The task spec requires the system to keep serving requests after `POST /chaos` kills an instance. That implies more than one instance of the application running concurrently, behind a load balancer that fails the killed instance over.

The cleanest answer at the architecture level would be: replicate everything that holds state, including the database. In practice, real Postgres HA needs streaming replication, a cluster manager (Patroni, repmgr, Stolon, or pg_auto_failover) to orchestrate failover, a connection router that follows the leader (PgBouncer + HAProxy, or a managed primary-standby endpoint), and an answer for split-brain. Each of those components is a non-trivial design choice with its own failure modes; building a credible version of any of them is a multi-week effort and obscures the patterns this task is meant to demonstrate (transactional invariants, atomic buy/sell, audit log integrity).

## Decision

Run N stateless app replicas (default 3) behind Caddy, all pointing at a single Postgres instance. The number of replicas is parametrised by `APP_REPLICAS` and the host port by `PORT`, both consumed by `deploy/docker-compose.yml` and the `scripts/run.sh` entry point.

`POST /chaos` writes a 202 so the load balancer treats the request as served (any HTTP status disables Caddy's `lb_try_duration` retry, preventing the kill from cascading to a sibling replica), then exits the process via `os.Exit(1)` after a short delay so the response leaves the socket. Docker's `restart: unless-stopped` policy resurrects the container; Caddy keeps serving requests off the surviving replicas in the meantime. The audit log invariant survives because every successful buy/sell already commits inside one Postgres transaction together with the audit row, regardless of which replica handled the call.

## Consequences

- Postgres remains a single point of failure. If the Postgres container goes down, the system goes down. This is documented as an explicit limitation rather than hidden, and the e2e test (`tests/e2e/chaos_test.sh`) only exercises app-replica failure.
- App-layer scaling is trivial: `APP_REPLICAS=N docker compose up -d` adds replicas live, Caddy picks them up via DNS service discovery within 5 seconds (see ADR 0002).
- Migrations are run by every replica on startup. Goose tracks the schema version in a table and skips already-applied migrations, so a race between replicas resolves to one applying the change and the rest moving on.
- A real production deployment would replace this with a managed Postgres (RDS, Cloud SQL, Supabase) plus a connection pooler. The application code does not need to change for that swap; only the connection string and the lifecycle of the DB container change.
- `/chaos` and SIGTERM use deliberately separate shutdown paths. SIGTERM is the operator-initiated graceful path (`httpSrv.Shutdown` drains in-flight requests, defers run, exit 0). `/chaos` is the chaos-test-initiated abrupt path (`os.Exit(1)`, no drain, defers skipped, exit 1). A single shared path was considered but conflated the two intents and would have masked the LB failover behaviour the chaos test is meant to exercise.
