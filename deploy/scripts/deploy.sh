#!/usr/bin/env bash
set -Eeuo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

usage() {
  echo "usage: $0 <full-commit-sha>" >&2
}

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

CMS_IMAGE_TAG="$1"
if [[ ! "${CMS_IMAGE_TAG}" =~ ^[0-9a-fA-F]{40}$ ]]; then
  echo "CMS_IMAGE_TAG must be a full 40-character commit SHA" >&2
  exit 2
fi
export CMS_IMAGE_TAG

require_file "${COMPOSE_FILE}"
acquire_deploy_lock
deployment_preflight

current_slot="$(active_slot)"
validate_slot "${current_slot}"
next_slot="$(opposite_slot "${current_slot}")"

echo "active slot: ${current_slot}"
echo "deploying ${CMS_IMAGE_TAG} to inactive slot: ${next_slot}"

compose pull "backend-${next_slot}" "frontend-${next_slot}"
compose up -d --no-deps "backend-${next_slot}" "frontend-${next_slot}"

wait_for_slot "${next_slot}"

echo "switching nginx to ${next_slot}"
if ! transactional_switch_slot "${next_slot}"; then
  echo "switch failed; active include should still describe ${current_slot}" >&2
  exit 1
fi

if ! public_smoke_test; then
  echo "post-switch smoke tests failed; switching back to ${current_slot}" >&2
  if ! transactional_switch_slot "${current_slot}"; then
    echo "CRITICAL: post-switch smoke tests failed and automatic restoration to ${current_slot} failed; operator intervention is required" >&2
    exit 20
  fi
  exit 1
fi

echo "deployment complete: ${next_slot} is active"
echo "previous slot kept running for rollback: ${current_slot}"
