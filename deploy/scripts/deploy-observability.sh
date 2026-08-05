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

# nginx_test / nginx_reload / atomic_install_file, shared with the CMS deploy so
# both paths validate and reload Nginx exactly the same way.
# shellcheck source=deploy/scripts/common.sh
source "${SCRIPT_DIR}/common.sh"

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

# Deliberately NOT named `compose`: common.sh defines a compose() bound to
# compose.cms.yml and the CMS env file. Shadowing it would work only because of
# definition order, and anyone moving the `source` line above would silently
# point every call in this script at the CMS stack.
obs_compose() {
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
obs_compose up -d --remove-orphans

if (( changed )); then
  # `up -d` does NOT restart a container when only the CONTENTS of a mounted
  # config file changed -- the Compose spec is identical, so it sees nothing to
  # do and reports "Running". Every config-only change therefore needs an
  # explicit restart or it silently does not take effect. This is the single
  # easiest thing to get wrong here.
  echo "config changed; restarting services to pick it up"
  obs_compose restart
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
      obs_compose ps >&2 || true
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
#
# These have to RETRY, not check once. /-/healthy goes green as soon as the web
# server is listening, which is well before the rule manager has evaluated the
# rule files and before the notifier has resolved alertmanager:9093 through
# Docker DNS. Asserting immediately after a restart is a race, and it is the
# race that made this step fail with "no active alertmanager" on a stack that
# was in fact fine seconds later.
retry_until() {
  local label="$1"; shift
  until "$@"; do
    if (( SECONDS >= deadline )); then
      echo "timed out: ${label}" >&2
      obs_compose ps >&2 || true
      return 1
    fi
    sleep 3
  done
  echo "  ok   ${label}"
}

has_alert_rules() {
  local n
  n="$(curl -fsS --max-time 5 http://127.0.0.1:19090/api/v1/rules \
    | grep -o '"name":"[^"]*"' | wc -l)" || return 1
  (( n > 0 ))
}

has_active_alertmanager() {
  # Match the discovered URL, not the JSON keys: "activeAlertmanagers" and
  # "droppedAlertmanagers" are always present, but capitalised, so a
  # case-sensitive match on lowercase "alertmanager:9093" only hits a real
  # entry. Checking activeAlertmanagers is non-empty would otherwise need a
  # JSON parser this host is not guaranteed to have.
  curl -fsS --max-time 5 http://127.0.0.1:19090/api/v1/alertmanagers \
    | grep -q 'alertmanager:9093'
}

retry_until "alert rules loaded" has_alert_rules \
  || { echo "prometheus loaded no alerting rules -- check observability/prometheus/rules/" >&2; exit 1; }
retry_until "alertmanager attached" has_active_alertmanager \
  || { echo "prometheus has no active alertmanager; alerts would fire into nothing" >&2; exit 1; }

obs_compose ps --format '  {{.Service}}\t{{.Status}}'

# --- Nginx sites -------------------------------------------------------------
# The read-only Loki and Prometheus endpoints the central Grafana connects to.
# These are HOST config, not containers, so they deploy differently: the files
# are installed into a directory the runner OWNS, which a root-owned
# /etc/nginx/conf.d/triangle-observability.conf pulls in with a wildcard include.
#
# That indirection is the whole point. It keeps the runner's sudo rights at
# exactly the two commands the CMS deploy already needs -- `nginx -t` and
# `nginx -s reload` -- instead of granting it write access to /etc/nginx or a
# general "install this file as root" rule. It is the same shape as
# /etc/nginx/triangle-cms/, which the runner already owns for blue/green.
NGINX_SITES_DIR="${NGINX_SITES_DIR:-/etc/nginx/triangle-observability}"
NGINX_SITES=(triangle-loki.conf triangle-prometheus.conf)

deploy_nginx_sites() {
  local staging prior_dir changed=0 s src dst
  if [[ ! -d "${NGINX_SITES_DIR}" ]]; then
    cat >&2 <<EOF
${NGINX_SITES_DIR} does not exist.

This needs a one-time root bootstrap on the host (see deploy/README.md):

  sudo install -d -o triangle-runner -g triangle-runner -m 0750 ${NGINX_SITES_DIR}
  printf 'include %s/*.conf;\\n' "${NGINX_SITES_DIR}" \\
    | sudo tee /etc/nginx/conf.d/triangle-observability.conf >/dev/null
  sudo nginx -t && sudo nginx -s reload

Failing rather than skipping: an Nginx site that silently stopped tracking the
repo is how the datasource endpoints drift out from under the Grafana using them.
EOF
    return 1
  fi
  [[ -w "${NGINX_SITES_DIR}" ]] || {
    echo "${NGINX_SITES_DIR} is not writable by $(id -un); it must be owned by the runner" >&2
    return 1
  }

  for s in "${NGINX_SITES[@]}"; do
    [[ -f "${DEPLOY_DIR}/nginx/${s}" ]] || {
      echo "missing source: ${DEPLOY_DIR}/nginx/${s}" >&2; return 1; }
    cmp -s "${DEPLOY_DIR}/nginx/${s}" "${NGINX_SITES_DIR}/${s}" || changed=1
  done

  if (( ! changed )); then
    echo "  no nginx site change; skipping reload"
    return 0
  fi

  # Snapshot whatever is live so a config that fails validation can be undone
  # without a human. Nginx keeps serving the OLD config until a successful
  # reload, so a failed `nginx -t` here is harmless as long as we put the files
  # back -- the danger is leaving broken files on disk for the NEXT reload,
  # which could be the CMS deploy's.
  prior_dir="$(mktemp -d)"
  staging="$(mktemp -d "${NGINX_SITES_DIR}/.stage.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '${prior_dir}' '${staging}'" RETURN

  for s in "${NGINX_SITES[@]}"; do
    [[ -f "${NGINX_SITES_DIR}/${s}" ]] && cp -p "${NGINX_SITES_DIR}/${s}" "${prior_dir}/${s}"
  done

  echo "  installing nginx sites: ${NGINX_SITES[*]}"
  for s in "${NGINX_SITES[@]}"; do
    src="${DEPLOY_DIR}/nginx/${s}"
    dst="${NGINX_SITES_DIR}/${s}"
    cp "${src}" "${staging}/${s}"
    chmod 0644 "${staging}/${s}"
    mv -f "${staging}/${s}" "${dst}"
  done

  restore_prior_sites() {
    local t
    for t in "${NGINX_SITES[@]}"; do
      if [[ -f "${prior_dir}/${t}" ]]; then
        cp -p "${prior_dir}/${t}" "${NGINX_SITES_DIR}/${t}"
      else
        rm -f "${NGINX_SITES_DIR}/${t}"
      fi
    done
  }

  if ! nginx_test; then
    echo "nginx validation failed with the new observability sites; reverting" >&2
    restore_prior_sites
    if ! nginx_test; then
      echo "CRITICAL: reverted observability sites still fail nginx validation; operator intervention required" >&2
      return 10
    fi
    return 1
  fi

  if ! nginx_reload; then
    echo "nginx reload failed; reverting observability sites" >&2
    restore_prior_sites
    nginx_test && nginx_reload || {
      echo "CRITICAL: reload failed and restoration could not be reloaded; operator intervention required" >&2
      return 11
    }
    return 1
  fi

  echo "  ok   nginx sites installed and reloaded"
}

echo "deploying nginx sites"
deploy_nginx_sites

echo "observability deployment complete"
