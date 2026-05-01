# 0003. PostgreSQL as the single source of truth

Date: 2026-05-01

## Status

Accepted

## Context

Buy and sell each have to update three rows together: the bank balance for the stock, the buyer's or seller's wallet entry, and a row in the audit log. The task spec is explicit that the audit log records only successful operations, which means an audit row without the matching state change (or the other way around) is a contract bug a reviewer will catch immediately.

Splitting state and audit between two stores is the obvious place to look for "scalability" or "separation of concerns". The cost is that the two stores then have to agree on partial failure, and getting them to agree means either two-phase commit (rare client support, painful operationally) or eventual consistency (a time window in which the audit log lies). Both add weight that a single transactional store does not, and at the scale and shape of this task there is nothing on the other side of the trade to justify paying for it.

Three options were considered:

- **Postgres for everything.** One `BEGIN ... COMMIT` covers the bank update, the wallet update, and the audit insert. The transaction itself is the atomicity guarantee.
- **Redis for state plus Postgres for audit.** Faster hot-state writes, but buy/sell now crosses two systems. A crash between the Redis write and the Postgres insert leaves the audit out of sync with reality.
- **SQLite for everything.** Same single-store guarantee, but SQLite serialises writers at the file level. With N application replicas behind a load balancer (see ADR 0001) every write contends for one writer lock, and HA via a shared file is fragile.

## Decision

PostgreSQL 18 holds bank state, wallet state, and the audit log. Buy and sell are implemented as one `BEGIN ... COMMIT` covering all three writes; the transaction shape itself is captured in ADR 0005. Read paths (`GET /stocks`, `GET /wallets/...`, `GET /log`) read directly from Postgres; there is no cache layer in front of it.

## Consequences

- The audit-log invariant becomes an SQL fact: a committed buy/sell carries its audit row, an uncommitted attempt rolls back the audit row alongside the state change.
- A single Postgres process is a single point of failure. The mitigation belongs to ADR 0001; short version is that real Postgres HA (streaming replication, failover, split-brain handling) is out of scope here, and the application code is written so the swap to a managed primary/standby endpoint is a connection-string change.
- All reads go to Postgres, including hot endpoints like `GET /stocks` and `GET /log`. At the load this task targets (a handful of small tables, expected throughput in the low hundreds of RPS at most) a cache layer would not pay for itself yet and would invite a class of staleness bugs around the audit endpoint.
- The schema, including the audit table, ships inside the binary as embedded migrations applied at startup; rationale lives in ADR 0006.
