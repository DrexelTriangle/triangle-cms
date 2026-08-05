#!/usr/bin/env bash
# Deploy the observability stack (Prometheus, Alertmanager, blackbox, Loki,
# Promtail) as part of the normal Delta deployment, so it stops depending on
# someone remembering to run it by hand.
#
# Run from the repo root, after the CMS deploy has completed:
#   deploy/scripts/deploy-observability.sh
#
# WHY THIS COPIES INSTEAD OF RUNNING IN PLACE
# The runner's checkout is the only tree on Delta that contains observability/,
# but `actions/checkout` resets it on every deploy. A long-lived stack bind-
# mounting config out of it would have its mount sources yanked mid-run. So the
# files are synced to a stable directory the runner owns and Actions never
# touches, and Compose runs from there. That directory used to be maintained by
# hand; this script is what replaces the hand.
#
# IT MUST NEVER TOUCH THE CMS. compose.observability.yml is a separate Compose
# project, so `up -d` here cannot recreate or stop the CMS slots. The one real
# coupling is that Prometheus joins the CMS network, declared external, so the
# CMS stack has to exist first -- which is why this runs after deploy.sh rather
# than beside it.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_DIR="$(cd "${DEPLOY_DIR}/.." && pwd)"

# Default under the runner's own home: writable by it, and outside _work/ so
# actions/checkout never resets it. Overridable for a differently-laid-out host.
OBSERVABILITY_DIR="${OBSERVABILITY_DIR:-${HOME}/triangle-observability}"
COMPOSE_PROJECT="triangle-observability"
HEALTH_TIMEOUT="${OBSERVABILITY_HEALTH_TIMEOUT:-120}"

SRC_TREE="${REPO_DIR}/observability"
SRC_COMPOSE="${DEPLOY_DIR}/compose.observability.yml"
DST_COMPOSE_DIR="${OBSERVABILITY_DIR}/deploy"
DST_COMPOSE="${DST_COMPOSE_DIR}/compose.observability.yml"

for f in "${SRC_COMPOSE}"; do
  [[ -f "$f" ]] || { echo "required file not found: $f" >&2; exit 1; }
done
[[ -d "${SRC_TREE}" ]] || { echo "required directory not found: ${SRC_TREE}" >&2; exit 1; }

compose() {
  # No --env-file: since Grafana was removed the only variable left is
  # DISCORD_WEBHOOK_FILE, which carries a default, so this stack needs no
  # secrets at deploy time. The webhook itself is a root-owned file on the host
  # that only Docker reads.
  docker compose -p "${COMPOSE_PROJECT}" -f "${DST_COMPOSE}" "$@"
}

# Fingerprint of everything that gets mounted into a container. Used to decide
# whether a restart is needed at all, so an ordinary CMS deploy that changed no
# observability file costs nothing.
tree_fingerprint() {
  local dir="$1"
  [[ -d "$dir" ]] || { echo "absent"; return; }
  find "$dir" -type f -exec sha256sum {} + 2>/dev/null \
    | sed "s#${dir}/##" | sort -k2 | sha256sum | cut -d' ' -f1
}

before="$(tree_fingerprint "${OBSERVABILITY_DIR}/observability")"
before_compose="$( [[ -f "${DST_COMPOSE}" ]] && sha256sum "${DST_COMPOSE}" | cut -d' ' -f1 || echo absent )"

echo "syncing observability config -> ${OBSERVABILITY_DIR}"
mkdir -p "${DST_COMPOSE_DIR}"

# --inplace is load-bearing. Without it rsync writes a temp file and renames,
# which gives every synced file a NEW INODE -- and a running container's bind
# mount holds the old one, so it would keep serving stale config even after a
# restart picked up nothing. --delete keeps removed files from lingering, which
# is how a deleted alert rule actually stops being evaluated.
if command -v rsync >/dev/null 2>&1; then
  rsync -a --delete --inplace "${SRC_TREE}/" "${OBSERVABILITY_DIR}/observability/"
  rsync -a --inplace "${SRC_COMPOSE}" "${DST_COMPOSE}"
else
  # cp -f truncates and rewrites in place, preserving the inode, so it is safe
  # here for the same reason --inplace is. The rm -rf first is what stands in
  # for --delete.
  rm -rf "${OBSERVABILITY_DIR}/observability"
  mkdir -p "${OBSERVABILITY_DIR}/observability"
  cp -R "${SRC_TREE}/." "${OBSERVABILITY_DIR}/observability/"
  cp -f "${SRC_COMPOSE}" "${DST_COMPOSE}"
fi

after="$(tree_fingerprint "${OBSERVABILITY_DIR}/observability")"
after_compose="$(sha256sum "${DST_COMPOSE}" | cut -d' ' -f1)"

changed=0
[[ "${before}" != "${after}" ]] && changed=1
[[ "${before_compose}" != "${after_compose}" ]] && changed=1

echo "applying compose (project ${COMPOSE_PROJECT})"
# --remove-orphans is what retires a service deleted from the compose file --
# Grafana left this way. It is scoped to this project, so it cannot reach the
# CMS slots.
compose up -d --remove-orphans

if (( changed )); then
  # `up -d` does NOT restart a container when only the CONTENTS of a mounted
  # config file changed -- the Compose spec is identical, so it sees nothing to
  # do and reports "Running". Every config-only change therefore needs an
  # explicit restart or it silently does not take effect. This is the single
  # easiest thing to get wrong here.
  echo "config changed; restarting services to pick it up"
  compose restart
else
  echo "no observability config change; skipping restart"
fi

echo "verifying"
deadline=$(( SECONDS + HEALTH_TIMEOUT ))

wait_for() {
  local label="$1" url="$2"
  until curl -fsS -o /dev/null --max-time 5 "${url}"; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for ${label} (${url})" >&2
      compose ps >&2 || true
      return 1
    fi
    sleep 3
  done
  echo "  ok   ${label}"
}

wait_for "prometheus" "http://127.0.0.1:19090/-/healthy"
wait_for "alertmanager" "http://127.0.0.1:19093/-/healthy"

# A stack that is up but has silently dropped its alert rules is worse than one
# that is down, because it looks fine. Assert the rules actually loaded and that
# Prometheus is talking to Alertmanager, rather than trusting "container is
# running".
rule_count="$(curl -fsS --max-time 5 http://127.0.0.1:19090/api/v1/rules \
  | grep -o '"name":"[^"]*"' | wc -l)"
if (( rule_count == 0 )); then
  echo "prometheus loaded no alerting rules -- check observability/prometheus/rules/" >&2
  exit 1
fi

if ! curl -fsS --max-time 5 http://127.0.0.1:19090/api/v1/alertmanagers \
  | grep -q 'alertmanager'; then
  echo "prometheus has no active alertmanager; alerts would fire into nothing" >&2
  exit 1
fi
echo "  ok   alert rules loaded and alertmanager attached"

compose ps --format '  {{.Service}}\t{{.Status}}'
echo "observability deployment complete"
