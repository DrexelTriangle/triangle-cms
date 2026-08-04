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

# The sidecar image is tagged by content: the git tree hash of embeddings/,
# which changes only when something in that directory does. Publish skips the
# build when that tag already exists, and Compose leaves the running container
# alone when the tag is unchanged -- so a Go-only commit no longer rebuilds a
# ~130MB model image in CI, nor reloads the model on the host for ~60s.
#
# Derived from the trusted default-branch checkout rather than from
# CMS_IMAGE_TAG, which the workflow treats strictly as data and never resolves
# as a git ref. For automatic deployments the two are the same commit. A manual
# rollback to an older SHA therefore keeps main's sidecar; that is safe because
# the sidecar is stateless and its HTTP contract is stable, and a change that
# broke it (a different vector width) would be a schema change needing its own
# migration anyway.
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

# The embedding sidecar is shared by both slots rather than duplicated per slot:
# it is stateless, so a second copy would only cost memory on a host that has
# little to spare. That means it is not part of the blue/green swap and has to be
# brought up separately -- the slot services below are started with --no-deps.
#
# Deliberately never fatal. Search degrades to lexical-only when the sidecar is
# missing or still loading its model, so a sidecar problem must not block or roll
# back a deployment that is otherwise fine.
if ! compose pull embeddings; then
  echo "warning: could not pull the embeddings sidecar; semantic search may be unavailable" >&2
fi
if compose up -d --no-deps embeddings; then
  # Recreated on every deploy because its image tag is the commit SHA, so it
  # reloads its model each time. Waiting here keeps the window where search is
  # lexical-only from overlapping the slot switch.
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
