#!/usr/bin/env bash
# check-agents-gate-pin.sh — Verify the validate-agents workflow pin in
# release.yml is current with fullsend-ai/agents main.
#
# Exits 0 if the pin matches agents main HEAD.
# Exits 1 if the pin is behind, unreachable, or missing.
#
# Inputs (env vars):
#   RELEASE_YML — path to release.yml
#     (default: .github/workflows/release.yml)
#
# Requires: gh CLI authenticated with read access to fullsend-ai/agents.

set -euo pipefail

RELEASE_YML="${RELEASE_YML:-.github/workflows/release.yml}"

if [[ ! -f "${RELEASE_YML}" ]]; then
  echo "::error::Release workflow not found: ${RELEASE_YML}" >&2
  exit 1
fi

# Extract the pinned SHA from the uses: directive.
PINNED_SHA=$(
  grep -oE \
    'fullsend-ai/agents/\.github/workflows/functional-tests\.yml@[a-f0-9]+' \
    "${RELEASE_YML}" \
  | sed 's/.*@//'
) || true

if [[ -z "${PINNED_SHA}" ]]; then
  echo "::error::Could not find fullsend-ai/agents workflow pin in ${RELEASE_YML}" >&2
  exit 1
fi

# Fetch agents main HEAD SHA.
AGENTS_MAIN_SHA=$(
  gh api repos/fullsend-ai/agents/commits/main --jq '.sha'
) || {
  echo "::error::Failed to fetch fullsend-ai/agents main SHA" >&2
  exit 1
}

if [[ -z "${AGENTS_MAIN_SHA}" ]]; then
  echo "::error::Empty SHA returned for fullsend-ai/agents main" >&2
  exit 1
fi

if [[ "${PINNED_SHA}" == "${AGENTS_MAIN_SHA}" ]]; then
  echo "::notice::validate-agents gate pin is current: ${PINNED_SHA}"
  exit 0
fi

# Count how far behind the pin is.
BEHIND_COUNT=$(
  gh api \
    "repos/fullsend-ai/agents/compare/${PINNED_SHA}...${AGENTS_MAIN_SHA}" \
    --jq '.ahead_by'
) || BEHIND_COUNT="unknown"

echo "::error::validate-agents gate pin is stale: pinned ${PINNED_SHA} is ${BEHIND_COUNT} commit(s) behind agents main ${AGENTS_MAIN_SHA}" >&2
exit 1
