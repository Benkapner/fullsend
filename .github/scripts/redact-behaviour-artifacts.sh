#!/usr/bin/env bash
# redact-behaviour-artifacts.sh — Strip job secrets from behaviour debug artifacts
# before upload.
#
# Invoked from .github/workflows/e2e.yml after a behaviour job failure. The workflow
# checks out this script from the base branch (not PR head) so malicious PR code
# cannot disable or weaken redaction.
#
# Handles plain text (logs, JSON, JSONL), nested archives (zip, tar.gz, gzip), and
# replaces opaque or encrypted blobs that cannot be scanned safely.
#
# Prior art: fullsend-ai/agents scripts/lib/post-failure-report.lib.sh

set -euo pipefail

ARTIFACT_DIR="${ARTIFACT_DIR:?ARTIFACT_DIR is required}"

# SYNC-WITH: perFileLimit/totalExtractLimit in pkg/behaviourtest/drivers/ci/githubactions/githubactions.go
readonly ARCHIVE_PER_FILE_LIMIT=$((10 << 20))
readonly ARCHIVE_TOTAL_LIMIT=$((100 << 20))

if [ ! -d "${ARTIFACT_DIR}" ]; then
  echo "::notice::No behaviour artifact dir at ${ARTIFACT_DIR} — skipping redaction"
  exit 0
fi

_redact_multiline_pem() {
  awk '
    function is_pem_begin(line) {
      return tolower(line) ~ /-----begin .*private key.*-----/
    }
    function is_pem_end(line) {
      return tolower(line) ~ /-----end .*private key.*-----/
    }
    is_pem_begin($0) {
      print "[REDACTED PRIVATE KEY]"
      in_pem = 1
      next
    }
    is_pem_end($0) {
      in_pem = 0
      next
    }
    in_pem { next }
    { print }
  '
}

_redact_literal_token() {
  local detail="$1"
  local token="$2"

  if [ -z "${token}" ]; then
    printf '%s' "${detail}"
    return 0
  fi

  export REDACT_LITERAL_TOKEN="${token}"
  awk '
    BEGIN {
      token = ENVIRON["REDACT_LITERAL_TOKEN"]
      repl = "[REDACTED]"
    }
    {
      s = $0
      while ((i = index(s, token)) > 0) {
        s = substr(s, 1, i - 1) repl substr(s, i + length(token))
      }
      print s
    }
  ' <<< "${detail}" | {
    local line result=""
    while IFS= read -r line || [ -n "${line}" ]; do
      if [ -n "${result}" ]; then
        result="${result}"$'\n'"${line}"
      else
        result="${line}"
      fi
    done
    printf '%s' "${result}"
  }
  unset REDACT_LITERAL_TOKEN
}

_redact_patterns() {
  sed -E \
    -e 's/gh[pousr]_[A-Za-z0-9_]{20,}/[REDACTED]/g' \
    -e 's/github_pat_[A-Za-z0-9_]+/[REDACTED]/g' \
    -e 's/x-access-token:[^@[:space:]]+/x-access-token:[REDACTED]/g' \
    -e 's/(Bearer|token)[[:space:]]+[A-Za-z0-9._-]+/\1 [REDACTED]/gi' \
    -e 's/ya29\.[A-Za-z0-9._-]+/[REDACTED]/g'
}

_redact_literal_secrets() {
  local detail="$1"
  local name value

  local secret_names=(
    TEST_FULLSEND_PEM
    TEST_TRIAGE_PEM
    TEST_CODER_PEM
    TEST_REVIEW_PEM
    TEST_RETRO_PEM
    TEST_PRIORITIZE_PEM
    CLOUDFLARE_ACCOUNT_ID
    CLOUDFLARE_API_TOKEN
    TEST_ACTOR_WRITE_PAT
    TEST_ACTOR_TRIAGE_PAT
    TEST_ACTOR_OUTSIDER_PAT
    E2E_GCP_PROJECT_ID
    E2E_GCP_WIF_PROVIDER
  )

  for name in "${secret_names[@]}"; do
    value="${!name:-}"
    if [ -n "${value}" ]; then
      detail="$(_redact_literal_token "${detail}" "${value}")"
    fi
  done

  printf '%s' "${detail}"
}

_redact_text_content() {
  local content="$1"
  local redacted
  redacted="$(printf '%s\n' "${content}" | _redact_multiline_pem | _redact_patterns)"
  _redact_literal_secrets "${redacted}"
}

_contains_nul_bytes() {
  local file="$1"
  python3 -c "import sys; data=open(sys.argv[1], 'rb').read(8192); sys.exit(0 if b'\\x00' in data else 1)" "${file}"
}

_file_kind() {
  local file="$1"
  local base lower mime

  base="$(basename "${file}")"
  lower="${base,,}"

  case "${lower}" in
    *.tar.gz | *.tgz)
      echo archive-tar-gz
      return 0
      ;;
    *.gz)
      echo archive-gzip
      return 0
      ;;
    *.zip)
      echo archive-zip
      return 0
      ;;
    *.gpg | *.age | *.enc)
      echo encrypted
      return 0
      ;;
    *.json | *.jsonl | *.log | *.txt | *.md | *.yaml | *.yml | *.xml | *.feature | *.out | *.err)
      echo text
      return 0
      ;;
  esac

  mime="$(file --brief --mime-type "${file}" 2>/dev/null || true)"
  case "${mime}" in
    text/* | application/json | application/xml | application/x-empty | inode/x-empty)
      echo text
      return 0
      ;;
    application/gzip | application/x-gzip)
      echo archive-gzip
      return 0
      ;;
    application/zip)
      echo archive-zip
      return 0
      ;;
    application/x-tar*)
      echo archive-tar-gz
      return 0
      ;;
    application/pgp-encrypted)
      echo encrypted
      return 0
      ;;
    application/pgp-keys)
      echo text
      return 0
      ;;
    image/* | video/* | audio/* | application/pdf)
      echo media
      return 0
      ;;
  esac

  if _contains_nul_bytes "${file}"; then
    echo binary
    return 0
  fi

  echo text
}

_stub_opaque_file() {
  local file="$1"
  local reason="${2:-could not be scanned for job secrets}"
  local tmp
  tmp="$(mktemp)"
  printf '%s\n' "[REDACTED OPAQUE CONTENT]" "This file was removed from behaviour debug artifacts because it ${reason}." >"${tmp}"
  mv "${tmp}" "${file}"
  echo "::warning::Replaced opaque artifact file: ${file}"
}

_archive_tree_within_limits() {
  local dir="$1"
  local size

  size="$(du -sb "${dir}" | awk '{print $1}')"
  if [ "${size}" -gt "${ARCHIVE_TOTAL_LIMIT}" ]; then
    return 1
  fi
  return 0
}

_redact_zip_file() {
  local file="$1"
  local tmpdir workdir

  tmpdir="$(mktemp -d)"
  workdir="${tmpdir}/contents"
  mkdir -p "${workdir}"

  if ! unzip -q "${file}" -d "${workdir}"; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "could not be extracted as zip"
    return 0
  fi

  if ! _archive_tree_within_limits "${workdir}"; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "exceeds safe extraction limits"
    return 0
  fi

  _redact_tree "${workdir}"
  rm -f "${file}"
  (cd "${workdir}" && zip -qr "${file}" .)
  rm -rf "${tmpdir}"
}

_redact_tar_gz_file() {
  local file="$1"
  local tmpdir workdir

  tmpdir="$(mktemp -d)"
  workdir="${tmpdir}/contents"
  mkdir -p "${workdir}"

  if ! tar -xzf "${file}" -C "${workdir}"; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "could not be extracted as tar.gz"
    return 0
  fi

  if ! _archive_tree_within_limits "${workdir}"; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "exceeds safe extraction limits"
    return 0
  fi

  _redact_tree "${workdir}"
  rm -f "${file}"
  tar -czf "${file}" -C "${workdir}" .
  rm -rf "${tmpdir}"
}

_redact_gzip_file() {
  local file="$1"
  local tmpdir content redacted size

  tmpdir="$(mktemp -d)"
  if ! gzip -dc "${file}" >"${tmpdir}/content"; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "could not be decompressed as gzip"
    return 0
  fi

  size="$(wc -c <"${tmpdir}/content" | tr -d ' ')"
  if [ "${size}" -gt "${ARCHIVE_PER_FILE_LIMIT}" ]; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "exceeds safe extraction limits"
    return 0
  fi

  if _contains_nul_bytes "${tmpdir}/content"; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "contained binary content that could not be scanned for job secrets"
    return 0
  fi

  content="$(<"${tmpdir}/content")"
  redacted="$(_redact_text_content "${content}")"
  printf '%s' "${redacted}" | gzip -c >"${file}"
  rm -rf "${tmpdir}"
}

_redact_text_file() {
  local file="$1"
  local content redacted tmp

  content="$(<"${file}")"
  redacted="$(_redact_text_content "${content}")"

  tmp="$(mktemp)"
  printf '%s' "${redacted}" >"${tmp}"
  mv "${tmp}" "${file}"
}

_redact_tree() {
  local dir="$1"
  local file

  while IFS= read -r -d '' file; do
    _redact_path "${file}"
  done < <(find "${dir}" -type f -print0)
}

_redact_opaque_file() {
  local file="$1"
  local reason="$2"
  _stub_opaque_file "${file}" "${reason}"
}

_redact_path() {
  local file="$1"
  local kind

  kind="$(_file_kind "${file}")"
  case "${kind}" in
    text)
      _redact_text_file "${file}"
      ;;
    archive-zip)
      _redact_zip_file "${file}"
      ;;
    archive-tar-gz)
      _redact_tar_gz_file "${file}"
      ;;
    archive-gzip)
      _redact_gzip_file "${file}"
      ;;
    media | binary)
      _redact_opaque_file "${file}" "is binary or media and could not be scanned for job secrets"
      ;;
    encrypted)
      _redact_opaque_file "${file}" "is encrypted and could not be scanned for job secrets"
      ;;
  esac
}

file_count=0
while IFS= read -r -d '' file; do
  _redact_path "${file}"
  file_count=$((file_count + 1))
done < <(find "${ARTIFACT_DIR}" -type f -print0)

echo "::notice::Redacted secrets in ${file_count} behaviour artifact file(s)"
