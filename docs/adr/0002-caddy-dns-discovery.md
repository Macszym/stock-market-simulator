# 0002. Caddy with DNS-based service discovery

Date: 2026-05-01

## Status

Accepted

## Context

The compose stack runs N app replicas (ADR 0001). The load balancer in front of them needs to:

1. Distribute traffic across all live replicas without requiring a config change when N is bumped.
2. Detect a replica killed by `POST /chaos` and stop sending traffic to it before the client sees a failure.
3. Pick up the restarted replica when Docker resurrects it, again without a config reload.

Two natural alternatives:

- **Static upstream list (`reverse_proxy app1:8080 app2:8080 ...`).** Requires `container_name` per replica and a templated Caddyfile, which couples the LB config to the replica count and breaks when `replicas: N` is bumped. Compose forbids `container_name` together with `deploy.replicas`, so this also forces giving up live scaling.
- **nginx + Docker template (`nginx.conf` rendered at startup).** Standard pattern, but reload semantics are coarser and the config DSL is heavier than what this stack actually needs.

## Decision

Use Caddy v2 in a separate compose service (`caddy:2-alpine`) with a `dynamic a` upstream block:

```caddyfile
:8080 {
    reverse_proxy {
        dynamic a app 8080 {
            refresh 5s
        }
        lb_policy round_robin
        lb_try_duration 5s
        lb_try_interval 250ms
        fail_duration 10s
        max_fails 1
        unhealthy_status 5xx
    }
}
```

`dynamic a` re-resolves the `app` DNS name every 5 seconds. Compose's embedded DNS returns one A-record per running replica, so scaling up or restarting a replica needs no Caddyfile edit and no Caddy reload.

Active health checks (`health_uri`) are intentionally **not** configured. Caddy disables them when dynamic upstreams are used (see [Caddy `reverse_proxy` docs](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)); declaring `health_uri` here would be a misleading no-op. Resilience comes from the combination of:

- `lb_try_duration` + `lb_try_interval`: in-request retry to a different upstream when a connect or response fails, so a single client request survives a dying replica.
- `fail_duration` + `max_fails`: passive health check parks a failing replica for 10 seconds.
- `refresh 5s`: stale or zombie A-records age out quickly when Docker rebuilds the network entry.

## Consequences

- The Caddyfile is a 12-line file that does not change when `APP_REPLICAS` changes.
- Failover is bounded by a few seconds in the worst case. End-to-end measurement in `tests/e2e/chaos_test.sh` and ad-hoc hammer tests (200 healthz hits during chaos) showed zero client-visible 5xx during a `/chaos` event.
- We accept the loss of active health checks. With a stateless `/healthz` and the application's own startup ordering (Postgres health-gated `depends_on`), the absence of active probing is acceptable for this stack; if the requirements ever shift to needing active probing, the migration path is to drop `dynamic a` in favour of statically-named replicas, or to put the active probe at a layer above Caddy (e.g. in a service mesh sidecar).
- Caddy's HTTP/2 and HTTP/3 listeners are skipped because the listen address is plain `:8080` (no TLS); this is fine here, where TLS termination would be done by the platform load balancer in a real deployment.
