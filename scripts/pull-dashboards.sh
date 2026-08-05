#!/usr/bin/env bash

set -euo pipefail
cd "$(dirname "$0")/.."
[ -f .env ] && { set -a; . ./.env; set +a; }
# Pulls dashboards out of a Grafana into the repo, so UI edits survive the
# instance they were made in.
#
# Delta no longer runs a Grafana (removed 2026-08-05), so GF_URL now points at
# whichever Grafana holds the dashboards -- in practice the central Triangle
# Grafana. Point it there and supply that instance's credentials:
#
#   GF_URL=https://<central-grafana> GRAFANA_ADMIN_USER=... \
#   GRAFANA_ADMIN_PASSWORD=... scripts/pull-dashboards.sh
#
# The default below is the old local address and will simply fail to connect
# now, which is the intended loud failure rather than a silent no-op.
#
# ALWAYS review `git diff` on the pulled JSON. The CMS dashboard resolves its
# datasources through the "prometheus_ds" and "loki_ds" template variables, and
# Grafana serializes whatever each panel resolved to -- so a pull can replace
# every "${prometheus_ds}" with a concrete UID and silently re-introduce the
# breakage those variables exist to prevent. Concrete UIDs are per-Grafana, so
# the committed result would render empty anywhere else.
GF_URL="${GF_URL:-http://127.0.0.1:3000}"
# GRAFANA_ADMIN_* are what the Compose files use; GF_USER/GF_PASS stay as
# fallbacks so an existing local .env keeps working.
GF_USER="${GRAFANA_ADMIN_USER:-${GF_USER:?set GRAFANA_ADMIN_USER (in .env or env)}}"
GF_PASS="${GRAFANA_ADMIN_PASSWORD:-${GF_PASS:?set GRAFANA_ADMIN_PASSWORD (in .env or env)}}"
OUT_DIR="observability/grafana/dashboards"
mkdir -p "$OUT_DIR"
curl -s -u "$GF_USER:$GF_PASS" "$GF_URL/api/search?type=dash-db" \
  | jq -r '.[].uid' \
  | while read -r uid; do
      curl -s -u "$GF_USER:$GF_PASS" "$GF_URL/api/dashboards/uid/$uid" \
        | jq '.dashboard | .id = null' \
        > "$OUT_DIR/$uid.json"
      echo "pulled $uid -> $OUT_DIR/$uid.json"
    done
