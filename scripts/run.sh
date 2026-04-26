#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

exec docker compose -f deploy/docker-compose.yml up --build
