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

# Tag the sidecar by directory content to avoid model reloads on unrelated deploys.
# Resolve from the trusted checkout, not the supplied CMS_IMAGE_TAG. Manual
# rollback retains this sidecar and requires a compatible embedding contract.
if embeddings_tag="$(git -C "${REPO_DIR}" rev-parse HEAD:embeddings 2>/dev/null)"; then
  CMS_EMBEDDINGS_TAG="${embeddings_tag}"
else
  echo "warning: could not derive the embeddings tag from git; falling back to the commit tag" >&2
  CMS_EMBEDDINGS_TAG="${CMS_IMAGE_TAG}"
fi
export CMS_EMBEDDINGS_TAG
echo "embeddings image tag: ${CMS_EMBEDDINGS_TAG}"

require_file "${COMPOSE_FILE}"
acquire_deploy_lock
deployment_preflight

current_slot="$(active_slot)"
validate_slot "${current_slot}"
next_slot="$(opposite_slot "${current_slot}")"

echo "active slot: ${current_slot}"
echo "deploying ${CMS_IMAGE_TAG} to inactive slot: ${next_slot}"

# Both slots share the sidecar. Start it separately because slots use --no-deps.
# Failure is non-fatal: search falls back to lexical results.
if ! compose pull embeddings; then
  echo "warning: could not pull the embeddings sidecar; semantic search may be unavailable" >&2
fi
if compose up -d --no-deps embeddings; then
  # Wait for model readiness before switching slots when the sidecar changes.
  if ! wait_for_embeddings; then
    echo "warning: the embeddings sidecar did not become healthy; search will serve lexical results until it does" >&2
  fi
else
  echo "warning: could not start the embeddings sidecar; search will serve lexical results" >&2
fi

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
