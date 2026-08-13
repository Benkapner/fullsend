#!/usr/bin/env bash
# redact-behaviour-artifacts-test.sh — Tests for redact-behaviour-artifacts.sh
#
# Run from repo root: bash .github/scripts/redact-behaviour-artifacts-test.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REDACT_SCRIPT="${SCRIPT_DIR}/redact-behaviour-artifacts.sh"
FAILURES=0

TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

run_test() {
  local test_name="$1"
  local must_not_contain="$2"
  local must_contain="${3:-}"

  local actual
  actual="$(<"${TMPDIR}/artifact.log")"

  if [ -n "${must_not_contain}" ] && echo "${actual}" | grep -qF "${must_not_contain}"; then
    echo "FAIL: ${test_name}"
    echo "  sanitized output still contains: '${must_not_contain}'"
    FAILURES=$((FAILURES + 1))
    return
  fi

  if [ -n "${must_contain}" ] && ! echo "${actual}" | grep -qF "${must_contain}"; then
    echo "FAIL: ${test_name}"
    echo "  expected to find: '${must_contain}'"
    FAILURES=$((FAILURES + 1))
    return
  fi

  echo "PASS: ${test_name}"
}

run_redaction() {
  rm -rf "${TMPDIR}/artifacts"
  mkdir -p "${TMPDIR}/artifacts"
  cp "${TMPDIR}/artifact.log" "${TMPDIR}/artifacts/artifact.log"
  ARTIFACT_DIR="${TMPDIR}/artifacts" bash "${REDACT_SCRIPT}" >/dev/null
  cp "${TMPDIR}/artifacts/artifact.log" "${TMPDIR}/artifact.log"
}

run_redaction_on_tree() {
  ARTIFACT_DIR="${TMPDIR}/artifacts" bash "${REDACT_SCRIPT}" >/dev/null
}

echo "==> PEM block redaction"
cat >"${TMPDIR}/artifact.log" <<EOF
before
$(printf '%s\n' '-----BEGIN RSA PRIVATE KEY-----' 'MIIEowIBAAKCAQEAfake' '-----END RSA PRIVATE KEY-----')
after
EOF
run_redaction
run_test "redacts-rsa-pem-block" "MIIEowIBAAKCAQEAfake" "[REDACTED PRIVATE KEY]"

cat >"${TMPDIR}/artifact.log" <<EOF
$(printf '%s\n' '-----BEGIN PGP PRIVATE KEY BLOCK-----' 'lQOYBCA' '-----END PGP PRIVATE KEY BLOCK-----')
EOF
run_redaction
run_test "redacts-pgp-pem-block" "lQOYBCA" "[REDACTED PRIVATE KEY]"

echo "==> Token pattern redaction"
cat >"${TMPDIR}/artifact.log" <<'EOF'
auth failed with ghp_abcdefghijklmnopqrstuvwxyz1234567890
EOF
run_redaction
run_test "redacts-ghp-token" "ghp_abcdefghijklmnopqrstuvwxyz1234567890"

cat >"${TMPDIR}/artifact.log" <<'EOF'
remote: https://x-access-token:ghp_secret@github.com/org/repo.git
EOF
run_redaction
run_test "redacts-access-token-url" "ghp_secret"

echo "==> Literal secret redaction"
cat >"${TMPDIR}/artifact.log" <<'EOF'
dumped literal-secret-pem-value in log
Normal log line: assertion failed at step 3
EOF
mkdir -p "${TMPDIR}/artifacts"
cp "${TMPDIR}/artifact.log" "${TMPDIR}/artifacts/artifact.log"
ARTIFACT_DIR="${TMPDIR}/artifacts" TEST_CODER_PEM="literal-secret-pem-value" bash "${REDACT_SCRIPT}" >/dev/null
cp "${TMPDIR}/artifacts/artifact.log" "${TMPDIR}/artifact.log"
run_test "redacts-literal-env-secret" "literal-secret-pem-value" "Normal log line: assertion failed at step 3"

echo "==> Compressed artifact redaction"
rm -rf "${TMPDIR}/artifacts"
mkdir -p "${TMPDIR}/artifacts"
printf 'token=ghp_abcdefghijklmnopqrstuvwxyz1234567890\n' >"${TMPDIR}/artifacts/secret.log"
gzip -c "${TMPDIR}/artifacts/secret.log" >"${TMPDIR}/artifacts/secret.log.gz"
rm -f "${TMPDIR}/artifacts/secret.log"
run_redaction_on_tree
gunzip -c "${TMPDIR}/artifacts/secret.log.gz" >"${TMPDIR}/artifact.log"
run_test "redacts-gzip-log" "ghp_abcdefghijklmnopqrstuvwxyz1234567890"

rm -rf "${TMPDIR}/artifacts"
mkdir -p "${TMPDIR}/artifacts/inner"
printf 'leaked literal-secret-pem-value here\n' >"${TMPDIR}/artifacts/inner/nested.log"
(
  cd "${TMPDIR}/artifacts/inner"
  zip -qr "../bundle.zip" nested.log
)
rm -rf "${TMPDIR}/artifacts/inner"
ARTIFACT_DIR="${TMPDIR}/artifacts" TEST_CODER_PEM="literal-secret-pem-value" bash "${REDACT_SCRIPT}" >/dev/null
mkdir -p "${TMPDIR}/unzipped"
unzip -q "${TMPDIR}/artifacts/bundle.zip" -d "${TMPDIR}/unzipped"
cp "${TMPDIR}/unzipped/nested.log" "${TMPDIR}/artifact.log"
run_test "redacts-zip-nested-log" "literal-secret-pem-value"

echo "==> Encrypted/opaque artifact handling"
rm -rf "${TMPDIR}/artifacts"
mkdir -p "${TMPDIR}/artifacts"
printf 'opaque-binary-secret' >"${TMPDIR}/artifacts/payload.gpg"
run_redaction_on_tree
cp "${TMPDIR}/artifacts/payload.gpg" "${TMPDIR}/artifact.log"
run_test "stubs-encrypted-artifact" "opaque-binary-secret" "[REDACTED OPAQUE CONTENT]"

echo ""
if [ "${FAILURES}" -gt 0 ]; then
  echo "${FAILURES} test(s) failed"
  exit 1
fi
echo "All redact-behaviour-artifacts tests passed"
