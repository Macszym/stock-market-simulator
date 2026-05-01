#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<EOF
Usage: $(basename "$0") [PORT] [APP_REPLICAS]

  PORT          Host port published by Caddy (default: 8080)
  APP_REPLICAS  Number of app instances behind the load balancer (default: 3)

Examples:
  $(basename "$0")             # 3 replicas on :8080
  $(basename "$0") 9090        # 3 replicas on :9090
  $(basename "$0") 9090 5      # 5 replicas on :9090
EOF
}

case "${1:-}" in
    -h|--help) usage; exit 0 ;;
esac

PORT="${1:-8080}"
APP_REPLICAS="${2:-3}"

if ! [[ "$PORT" =~ ^[1-9][0-9]*$ ]] || (( PORT > 65535 )); then
    echo "error: PORT must be an integer in 1-65535, got: $PORT" >&2
    exit 1
fi

if ! [[ "$APP_REPLICAS" =~ ^[1-9][0-9]*$ ]]; then
    echo "error: APP_REPLICAS must be a positive integer, got: $APP_REPLICAS" >&2
    exit 1
fi

cd "$(dirname "$0")/.."

export PORT APP_REPLICAS

exec docker compose -f deploy/docker-compose.yml up --build
