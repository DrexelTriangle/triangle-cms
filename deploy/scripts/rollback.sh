#!/usr/bin/env bash
set -Eeuo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

require_file "${COMPOSE_FILE}"
require_file "${ENV_FILE}"
acquire_deploy_lock

current_slot="$(active_slot)"
validate_slot "${current_slot}"
target_slot="${1:-$(opposite_slot "${current_slot}")}"
validate_slot "${target_slot}"

if [[ "${target_slot}" == "${current_slot}" ]]; then
  echo "slot ${target_slot} is already active"
  exit 0
fi

echo "rolling back from ${current_slot} to ${target_slot}"
wait_for_slot "${target_slot}"
if ! transactional_switch_slot "${target_slot}"; then
  echo "rollback switch failed; active include should still describe ${current_slot}" >&2
  exit 1
fi

if ! public_smoke_test; then
  echo "rollback smoke tests failed; switching back to ${current_slot}" >&2
  if ! transactional_switch_slot "${current_slot}"; then
    echo "CRITICAL: rollback smoke tests failed and automatic restoration to ${current_slot} failed; operator intervention is required" >&2
    exit 20
  fi
  exit 1
fi

echo "rollback complete: ${target_slot} is active"
