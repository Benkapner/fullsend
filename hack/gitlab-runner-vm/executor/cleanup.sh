#!/usr/bin/env bash
# GitLab Runner custom executor — cleanup stage.
# Stops and removes the job container. Always succeeds.
# -e intentionally omitted — cleanup must not abort on individual failures.
set -uo pipefail

# shellcheck source=job_id.sh
source "$(dirname "${BASH_SOURCE[0]}")/job_id.sh"
JOB_ID=$(resolve_job_id) || {
  echo "WARN: could not read job id from JOB_RESPONSE_FILE — nothing to clean up"
  exit 0
}

STATE_DIR="${HOME}/.local/state/gitlab-runner"
STATE_FILE="${STATE_DIR}/container-${JOB_ID}"

if [ -f "${STATE_FILE}" ]; then
  CONTAINER_NAME=$(cat "${STATE_FILE}")
  if [[ "${CONTAINER_NAME}" =~ ^runner-[0-9]+$ ]]; then
    echo "Cleaning up container: ${CONTAINER_NAME}"
    podman stop --time 10 "${CONTAINER_NAME}" 2>/dev/null || true
    podman rm -f "${CONTAINER_NAME}" 2>/dev/null || true
  else
    # Skip only the podman calls — the staging copy of the gateway mTLS
    # material below must still be removed.
    echo "WARN: state file holds an unexpected container name (${CONTAINER_NAME}) — not touching podman"
  fi
  rm -f "${STATE_FILE}"
fi

OPENSHELL_STAGING="${STATE_DIR}/openshell-${JOB_ID}"
rm -rf "${OPENSHELL_STAGING}" 2>/dev/null || true
