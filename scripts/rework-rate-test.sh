#!/usr/bin/env bash
# rework-rate-test.sh — Tests for rework-rate.sh with mock gh.
#
# Run from repo root: bash scripts/rework-rate-test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REWORK_SCRIPT="${SCRIPT_DIR}/rework-rate.sh"
FAILURES=0

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

MOCK_BIN="${TMPDIR}/bin"
mkdir -p "${MOCK_BIN}"

# Fixture files the mock gh reads from
SEARCH_RESULTS="${TMPDIR}/search_results.json"
PR_FILES="${TMPDIR}/pr_files.json"
PR_DETAIL="${TMPDIR}/pr_detail.json"
FOLLOWUP_COMMITS="${TMPDIR}/followup_commits.json"
COMMIT_DETAIL="${TMPDIR}/commit_detail.json"
GH_LOG="${TMPDIR}/gh.log"
GH_FAIL="false"

# Write mock gh that routes by URL pattern and applies --jq filters
cat >"${MOCK_BIN}/gh" <<'MOCK_EOF'
#!/usr/bin/env bash
echo "gh $*" >> "GHLOG_PLACEHOLDER"
if [[ "${GH_FAIL}" == "true" ]]; then
  echo "simulated gh failure" >&2
  exit 1
fi

# Extract --jq filter if present
JQ_FILTER=""
ARGS=("$@")
for i in "${!ARGS[@]}"; do
  if [[ "${ARGS[$i]}" == "--jq" ]]; then
    JQ_FILTER="${ARGS[$((i+1))]}"
    break
  fi
done

# Route by URL pattern (use first positional arg after "api")
url_arg=""
for arg in "$@"; do
  [[ "$arg" == "api" ]] && continue
  [[ "$arg" == --* ]] && continue
  url_arg="$arg"
  break
done

output=""
case "$*" in
  *"search/issues"*)
    output=$(cat "SEARCH_PLACEHOLDER")
    ;;
  *"/pulls/"*"/files"*)
    output=$(cat "PRFILES_PLACEHOLDER")
    ;;
  *"/pulls/"*)
    output=$(cat "PRDETAIL_PLACEHOLDER")
    ;;
  *"/commits?"*)
    output=$(cat "COMMITS_PLACEHOLDER")
    ;;
  *"/commits/"*)
    output=$(cat "COMMITDETAIL_PLACEHOLDER")
    ;;
  *)
    echo "unexpected gh call: $*" >&2
    exit 1
    ;;
esac

if [[ -n "${JQ_FILTER}" ]]; then
  echo "${output}" | jq -r "${JQ_FILTER}"
else
  echo "${output}"
fi
MOCK_EOF

# Replace placeholders with actual paths
sed -i "s|GHLOG_PLACEHOLDER|${GH_LOG}|g" "${MOCK_BIN}/gh"
sed -i "s|SEARCH_PLACEHOLDER|${SEARCH_RESULTS}|g" "${MOCK_BIN}/gh"
sed -i "s|PRFILES_PLACEHOLDER|${PR_FILES}|g" "${MOCK_BIN}/gh"
sed -i "s|PRDETAIL_PLACEHOLDER|${PR_DETAIL}|g" "${MOCK_BIN}/gh"
sed -i "s|COMMITS_PLACEHOLDER|${FOLLOWUP_COMMITS}|g" "${MOCK_BIN}/gh"
sed -i "s|COMMITDETAIL_PLACEHOLDER|${COMMIT_DETAIL}|g" "${MOCK_BIN}/gh"

chmod +x "${MOCK_BIN}/gh"
export PATH="${MOCK_BIN}:${PATH}"
export GH_FAIL="false"

run_case() {
  local name="$1"
  local expected_pattern="$2"

  : >"${GH_LOG}"

  local output
  output="$("${REWORK_SCRIPT}" "test-org/test-repo" 30 7 2>&1)" || true

  if echo "${output}" | grep -qE "${expected_pattern}"; then
    echo "PASS: ${name}"
  else
    echo "FAIL: ${name}"
    echo "  expected pattern: ${expected_pattern}"
    echo "  got output:"
    echo "${output}" | sed 's/^/    /'
    FAILURES=$((FAILURES + 1))
  fi
}

# --- Test 1: Genuine single-parent rework ---
# Bot PR #10 merged, human commit abc1234 touches the same file → rework
cat >"${SEARCH_RESULTS}" <<'EOF'
{"items":[{"number":10,"title":"bot fix","closed_at":"2026-01-01T10:00:00Z"}]}
EOF
cat >"${PR_FILES}" <<'EOF'
[{"filename":"src/main.go"}]
EOF
cat >"${PR_DETAIL}" <<'EOF'
{"merge_commit_sha":"merge111"}
EOF
cat >"${FOLLOWUP_COMMITS}" <<'EOF'
[{"sha":"abc1234","author":{"type":"User","login":"human"},"parents":[{"sha":"p1"}]}]
EOF
cat >"${COMMIT_DETAIL}" <<'EOF'
{"sha":"abc1234","files":[{"filename":"src/main.go"}]}
EOF

run_case "genuine single-parent rework detected" "Rework rate: 100.0%"

# --- Test 2: Merge commit must NOT count as rework ---
# Follow-up commit has 2 parents (merge commit) touching same file → no rework
cat >"${FOLLOWUP_COMMITS}" <<'EOF'
[{"sha":"merge999","author":{"type":"User","login":"human"},"parents":[{"sha":"p1"},{"sha":"p2"}]}]
EOF
cat >"${COMMIT_DETAIL}" <<'EOF'
{"sha":"merge999","files":[{"filename":"src/main.go"}]}
EOF

run_case "merge commit (2 parents) excluded from rework" "Rework rate: 0.0%"

# --- Test 3: PR's own merge SHA excluded ---
# Follow-up commit SHA matches the PR's merge_commit_sha → must not count
cat >"${PR_DETAIL}" <<'EOF'
{"merge_commit_sha":"abc1234"}
EOF
cat >"${FOLLOWUP_COMMITS}" <<'EOF'
[{"sha":"abc1234","author":{"type":"User","login":"human"},"parents":[{"sha":"p1"}]}]
EOF

run_case "PR own merge commit SHA excluded" "Rework rate: 0.0%"

# --- Test 4: >100-item paginated search response ---
# Generate 101 PRs in search results; script should process all of them
ITEMS=""
for i in $(seq 1 101); do
  [ -n "${ITEMS}" ] && ITEMS="${ITEMS},"
  ITEMS="${ITEMS}{\"number\":${i},\"title\":\"bot pr ${i}\",\"closed_at\":\"2026-01-01T10:00:00Z\"}"
done
cat >"${SEARCH_RESULTS}" <<EOF
{"items":[${ITEMS}]}
EOF
cat >"${PR_DETAIL}" <<'EOF'
{"merge_commit_sha":"merge111"}
EOF
cat >"${FOLLOWUP_COMMITS}" <<'EOF'
[]
EOF

run_case "handles >100 PRs from paginated response" "Found 101 agent PRs"

# --- Test 5: API failure skips PR with warning ---
cat >"${SEARCH_RESULTS}" <<'EOF'
{"items":[{"number":10,"title":"bot fix","closed_at":"2026-01-01T10:00:00Z"}]}
EOF
export GH_FAIL="true"

run_case "API failure exits with error" "ERROR: could not fetch bot PRs"
export GH_FAIL="false"

# --- Results ---
echo ""
if [[ "${FAILURES}" -gt 0 ]]; then
  echo "${FAILURES} test(s) failed"
  exit 1
fi

echo "All rework-rate tests passed."
