#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RESET_DATA=0
if [[ "${1:-}" == "--reset-data" ]]; then
  RESET_DATA=1
fi

if command -v docker >/dev/null 2>&1; then
  :
else
  echo "docker is not installed or not on PATH" >&2
  exit 1
fi

if docker compose version >/dev/null 2>&1; then
  COMPOSE_CMD=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD=(docker-compose)
else
  echo "docker compose plugin (or docker-compose) is required" >&2
  exit 1
fi

if [[ "$RESET_DATA" -eq 1 ]]; then
  echo "Resetting compose services and volumes..."
  "${COMPOSE_CMD[@]}" down -v --remove-orphans
fi

echo "Starting mariadb, cms, loki, and promtail..."
"${COMPOSE_CMD[@]}" up -d --build --remove-orphans

echo
echo "Stack is up. Useful commands:"
echo "  ${COMPOSE_CMD[*]} ps"
echo "  ${COMPOSE_CMD[*]} logs -f cms"
echo "  ${COMPOSE_CMD[*]} logs -f promtail"
echo "  ${COMPOSE_CMD[*]} down"
echo "  ${COMPOSE_CMD[*]} down -v   # remove volumes (DB/Loki data)"
