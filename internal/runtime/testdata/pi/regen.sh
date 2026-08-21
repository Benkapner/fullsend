#!/usr/bin/env bash
# Capture a live pi --mode json basic_run.ndjson for parsePiStream tests.
#
# Other fixtures (error/malformed/multi-step/reasoning/truncated) remain
# hand-authored to packages/coding-agent/docs/json.md v0.84.2. Run this
# when you have a configured provider to replace basic_run.ndjson.
#
# Usage (from repo root or this directory):
#   internal/runtime/testdata/pi/regen.sh
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
PINNED="${PI_VERSION:-0.84.2}"
PI=(npx -y "@earendil-works/pi-coding-agent@${PINNED}" --print --mode json)

if ! command -v npx >/dev/null 2>&1; then
	echo "regen.sh: npx is required" >&2
	exit 1
fi

echo "regen.sh: capturing fixtures with @earendil-works/pi-coding-agent@${PINNED}" >&2
echo "regen.sh: writing into ${DIR}" >&2

# --no-session keeps the capture out of the operator's session store when supported.
# If the flag is unknown on this pin, drop it and retry.
capture() {
	local out="$1"
	shift
	if ! "${PI[@]}" --no-session "$@" >"${out}" 2>/dev/null; then
		"${PI[@]}" "$@" >"${out}"
	fi
}

capture "${DIR}/basic_run.ndjson" "List files in one sentence, then stop."
echo "wrote ${DIR}/basic_run.ndjson ($(wc -l <"${DIR}/basic_run.ndjson") lines)"
