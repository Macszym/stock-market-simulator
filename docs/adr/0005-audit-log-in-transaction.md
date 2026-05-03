# 0005. Audit log written inside the buy/sell transaction

## Context

The task spec says the audit log "should log only successful operations". Read literally, this is a precision claim: the log carries each operation that took effect, no more and no less. Two failure modes break that claim:

- **Audit row without state change.** A successful insert into `audit_log` followed by a failed state update (or a process crash between them) leaves the log claiming an operation that never happened. A client reading `GET /log` sees a buy of `AAPL` for `w1`; `GET /wallets/w1` shows no `AAPL`. The audit lies.
- **State change without audit row.** A successful state update followed by a failed audit insert leaves an operation invisible to `GET /log`. Reconciling stock movements after the fact becomes guesswork.

Either failure mode breaks the spec's audit-log contract directly. The two writes have to commit or fail together.

Postgres holds bank state, wallet state, and the audit log in one database (see ADR 0003), so the cheapest way to bind them is a single transaction.

## Decision

Both `BuyStock` and `SellStock` in `internal/storage/postgres.go` open a transaction with `pool.Begin`, perform every required mutation, and commit at the end. The audit insert sits inside that transaction, before `Commit`. Buy in SQL terms:

```sql
-- Wallet creation lives outside the transaction so that hitting an unknown
-- stock (404) or a depleted bank (400) still leaves the wallet behind, per
-- the spec's "if the wallet doesn't exist this operation should create it".
INSERT INTO wallets (id) VALUES ($2) ON CONFLICT (id) DO NOTHING;

BEGIN;
  SELECT quantity FROM bank_stocks WHERE name = $1 FOR UPDATE;             -- 404 vs 400 split
  UPDATE bank_stocks  SET quantity = quantity - 1 WHERE name = $1 AND quantity > 0;
  INSERT INTO wallet_stocks (wallet_id, stock_name, quantity) VALUES ($2, $1, 1)
    ON CONFLICT (wallet_id, stock_name) DO UPDATE SET quantity = wallet_stocks.quantity + 1;
  INSERT INTO audit_log (operation, wallet_id, stock_name) VALUES ('BUY', $2, $1);
COMMIT;
```

There is no "publish to audit afterwards" step. A `defer tx.Rollback(ctx)` covers any error path before the explicit `Commit`; rollback after a successful commit is a no-op in pgx, which keeps the defer safe rather than conditional.

Both functions acquire locks in the same order: the `bank_stocks` row first (via `SELECT ... FOR UPDATE`), the `wallet_stocks` row second. Two concurrent buys on the same stock serialise on the bank row; a buy and a sell on the same stock take the same path. The classical deadlock setup (transaction A holding lock X waiting for lock Y while transaction B holds Y waiting for X) cannot arise.

The default Postgres isolation level (Read Committed) is sufficient. The atomic decrement uses `UPDATE ... WHERE quantity > 0`, which is a conditional update at the SQL level; combined with the per-row `SELECT ... FOR UPDATE` issued only to distinguish "stock unknown" (404) from "stock depleted" (400), it eliminates lost updates without forcing the retry loops that `Serializable` would require on serialisation failure.

## Consequences

- The audit log is the trusted record of successful operations. `count(audit_log)` equals the number of successful buy/sell calls; this is verified directly under contention by `tests/integration/concurrency_test.go` (100 concurrent buys against a bank of 50 must produce exactly 50 audit rows) and end-to-end by `tests/e2e/chaos_test.sh`.
- Transactional latency includes the audit insert, so `audit_log` is on the hot path. At the throughput this task targets the cost is invisible; at higher throughput the table would need an index review (currently only the implicit primary key) and possibly a partition scheme on `id`.
- A future requirement to forward audit events to an external system (Kafka, S3, an event bus) cannot piggy-back on the same `Begin/Commit`. The standard answer is the transactional outbox pattern: write the outbox row inside the same transaction as the state change, ship it asynchronously, keep the in-database invariant intact.
- The spec cap of 10 000 entries means there is no urgency on archival or pagination; `GET /log` returns the full table. If the cap is lifted, the same-transaction guarantee is unaffected, but the read endpoint will need pagination and the table will need explicit indexing on `(wallet_id)` and probably `(stock_name)`.
