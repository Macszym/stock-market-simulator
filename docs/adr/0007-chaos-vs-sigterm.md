# 0007. POST /chaos and SIGTERM are separate shutdown paths

## Context

The task spec defines two related but semantically distinct shutdown scenarios:

- **Operator-initiated shutdown.** A redeploy, a `docker compose down`, a Kubernetes pod eviction, or `Ctrl+C` during local development. The orchestrator wants the process to drain in-flight requests, release database connections, and exit cleanly so the next instance can take over without dropping work.
- **Chaos-test-initiated crash.** `POST /chaos` is the spec's instruction to "kill an instance that serves this request", paired with the non-functional requirement that "killing 1 instance doesn't kill the product". The point is to simulate an unannounced node failure and prove the load balancer and the surviving replicas absorb the disruption invisibly to clients.

Collapsing these into one path breaks one of them. If both go through `os.Exit(1)`, every rolling deploy drops in-flight requests for no reason. If both go through `httpSrv.Shutdown`, `/chaos` becomes a polite drain rather than a crash, and the chaos test stops exercising the failover machinery.

There is also a load-balancer interaction that constrains the `/chaos` shape. Caddy's `lb_try_duration` retries a failed upstream attempt on a sibling replica, but only when the failure surfaces as a connection error. Any HTTP status (2xx, 4xx, 5xx) cancels that retry. If `/chaos` exited before writing a response, Caddy would treat the dropped connection as a transport failure, retry the same `POST /chaos` on a sibling replica, and the loop would chain across the entire cluster - one client call killing every instance in turn.

## Decision

Two paths, two implementations, one binary.

**SIGTERM and SIGINT** route through `signal.NotifyContext` in `cmd/server/main.go`. The notifying context is wired into a `select` that, on cancellation, calls `httpSrv.Shutdown(shutdownCtx)` with a 5-second budget. Defers run in order (`stop()`, `pool.Close()`), the process returns from `run()`, and `main` exits with status 0. This is the operator-facing exit; if anything fails along the way, `run` returns the error and `main` logs it before exiting 1.

**POST /chaos** is handled by `Server.handleChaos` in `internal/api/handlers.go`. The handler writes `202 Accepted` with body `{"message":"shutting down"}`, then spawns a goroutine that sleeps for `chaosShutdownDelay` (100 ms, enough for the response to leave the socket) before calling the injected `chaos func()` closure. In production that closure logs `slog.Warn("chaos endpoint invoked, exiting")` and calls `os.Exit(1)` directly. No deferred function runs; the connection pool is left for Postgres to reap; in-flight requests on this replica are cut. That is the chaos.

The closure is injected through `api.NewServer(svc, logger, chaos func())`. Tests pass a fake closure that increments a counter, so the handler test suite exercises the chaos path without killing the test runner.

The 202 status code is not arbitrary. Returning any HTTP response (even a 5xx) prevents Caddy from interpreting the disconnection as a transport failure and stops the cluster-wide cascade. 202 is the most accurate of the available codes: the server has accepted the request and committed to acting on it, but the action is asynchronous to the response.

## Consequences

- The two intents are visibly separate in the code. The SIGTERM path lives in `cmd/server/main.go`; the chaos path lives in `internal/api/handlers.go`. Neither obscures the other, and a reader chasing one path is not pulled through the other on the way.
- The 202-with-body shape is load-balancer-mandated, not stylistic. Documenting it (here and in the README design-decisions section) keeps a future maintainer from "simplifying" the handler to write nothing and exit, which would re-introduce the cascade-kill bug.
- Exit codes carry meaning: 0 = graceful shutdown, 1 = chaos exit or unexpected runtime error. Operators reading container exit codes can distinguish a clean drain from anything else, including the chaos test.
- The `chaos func()` injection keeps the handler unit-testable. Without it, every test of `/chaos` would have to fork a subprocess to avoid taking the test runner down with it.
- End-to-end coverage for the full shape lives in `tests/e2e/chaos_test.sh`: 50 buys, `POST /chaos`, 50 more buys after the killed replica is gone, then assert all 100 buys land in the audit log. The script verifies that the response status is 202 before continuing, so a regression to a different shape would fail loudly rather than silently degrade the chaos behaviour.
