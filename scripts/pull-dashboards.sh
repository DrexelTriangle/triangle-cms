#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] && { set -a; . ./.env; set +a; }
# Dashboards are provisioned from disk, but allowUiUpdates lets them be edited
# in the Grafana UI. This pulls those edits back into the repo so the next
# fresh Grafana keeps them; without it, UI changes live only in Grafana's
# database and are lost with the volume.
GF_URL="${GF_URL:-http://127.0.0.1:3000}"
# GRAFANA_ADMIN_* are what the Compose files use; GF_USER/GF_PASS stay as
# fallbacks so an existing local .env keeps working.
GF_USER="${GRAFANA_ADMIN_USER:-${GF_USER:?set GRAFANA_ADMIN_USER (in .env or env)}}"
GF_PASS="${GRAFANA_ADMIN_PASSWORD:-${GF_PASS:?set GRAFANA_ADMIN_PASSWORD (in .env or env)}}"
OUT_DIR="observability/grafana/provisioning/dashboards/json"
mkdir -p "$OUT_DIR"
curl -s -u "$GF_USER:$GF_PASS" "$GF_URL/api/search?type=dash-db" \
  | jq -r '.[].uid' \
  | while read -r uid; do
      curl -s -u "$GF_USER:$GF_PASS" "$GF_URL/api/dashboards/uid/$uid" \
        | jq '.dashboard | .id = null' \
        > "$OUT_DIR/$uid.json"
      echo "pulled $uid -> $OUT_DIR/$uid.json"
    done
