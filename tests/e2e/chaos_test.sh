#!/usr/bin/env bash
# End-to-end chaos resilience test for the HA stack.
#
# Brings up Caddy + N app replicas + Postgres, drives 50 buys, kills a
# replica via /chaos, drives another 50 buys, and asserts that:
#   - all 100 buys were recorded in the audit log,
#   - the bank reflects the final state (TEST stock fully drained).
#
# Exits 0 on success, non-zero on any failed step or assertion.
set -euo pipefail

readonly COMPOSE_FILE="deploy/docker-compose.yml"
readonly BASE_URL="http://localhost:8080"
readonly STOCK_NAME="TEST"
readonly INITIAL_QTY=100
readonly BUYS_BEFORE=50
readonly BUYS_AFTER=50
readonly HEALTH_TIMEOUT_SECONDS=30

cd "$(dirname "$0")/../.."

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
fail() { printf '[%s] FAIL: %s\n' "$(date +%H:%M:%S)" "$*" >&2; exit 1; }

require() {
    command -v "$1" >/dev/null 2>&1 || fail "missing dependency: $1"
}

require docker
require curl
require jq

cleanup() {
    log "cleanup: docker compose down -v"
    docker compose -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_health() {
    local deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))
    while (( $(date +%s) < deadline )); do
        if curl -fsS -m 2 "$BASE_URL/healthz" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
    done
    fail "healthz did not become ready within ${HEALTH_TIMEOUT_SECONDS}s"
}

# Issues a request and aborts the test if status is not 200.
# Caddy's lb_try_duration retries failed upstream attempts within the
# same client request, so transient failover during /chaos should not
# surface here.
http_post() {
    local path="$1"
    local body="$2"
    local label="$3"
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" -m 5 \
        -X POST -H "Content-Type: application/json" \
        --data "$body" "$BASE_URL$path")
    if [[ "$code" != "200" ]]; then
        fail "$label expected 200, got $code (path=$path)"
    fi
}

log "step 1/8: clean previous state"
docker compose -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true

log "step 2/8: bring up the stack"
docker compose -f "$COMPOSE_FILE" up -d --build >/dev/null

log "step 3/8: wait for Caddy + at least one app replica"
wait_for_health

log "step 4/8: seed the bank with $STOCK_NAME=$INITIAL_QTY"
http_post "/stocks" \
    "{\"stocks\":[{\"name\":\"$STOCK_NAME\",\"quantity\":$INITIAL_QTY}]}" \
    "seed bank"

log "step 5/8: $BUYS_BEFORE buys for wallet w1 (pre-chaos)"
for i in $(seq 1 "$BUYS_BEFORE"); do
    http_post "/wallets/w1/stocks/$STOCK_NAME" '{"type":"buy"}' "pre-chaos buy #$i"
done

log "step 6/8: fire /chaos"
chaos_code=$(curl -s -o /dev/null -w "%{http_code}" -m 5 \
    -X POST "$BASE_URL/chaos")
if [[ "$chaos_code" != "202" ]]; then
    fail "/chaos expected 202, got $chaos_code"
fi
# Give Caddy time to detect the dead replica via passive health check.
sleep 2
wait_for_health

log "step 7/8: $BUYS_AFTER buys for wallet w2 (post-chaos)"
for i in $(seq 1 "$BUYS_AFTER"); do
    http_post "/wallets/w2/stocks/$STOCK_NAME" '{"type":"buy"}' "post-chaos buy #$i"
done

log "step 8/8: assert audit log and bank state"

log_buys=$(curl -fsS "$BASE_URL/log" \
    | jq --arg s "$STOCK_NAME" \
        '[.log[] | select(.type=="buy" and .stock_name==$s)] | length')
expected_buys=$(( BUYS_BEFORE + BUYS_AFTER ))
if [[ "$log_buys" != "$expected_buys" ]]; then
    fail "audit log: expected $expected_buys buys for $STOCK_NAME, got $log_buys"
fi
log "  audit log: $log_buys buy entries for $STOCK_NAME (expected $expected_buys)"

bank_qty=$(curl -fsS "$BASE_URL/stocks" \
    | jq --arg s "$STOCK_NAME" \
        '.stocks[] | select(.name==$s) | .quantity')
if [[ "$bank_qty" != "0" ]]; then
    fail "bank: expected $STOCK_NAME=0, got $bank_qty"
fi
log "  bank: $STOCK_NAME=$bank_qty (expected 0)"

log "PASS: HA stack survived /chaos, all 100 buys recorded, bank drained"
