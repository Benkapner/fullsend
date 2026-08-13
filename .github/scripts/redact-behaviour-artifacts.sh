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

  REDACT_LITERAL_TOKEN="${token}" REDACT_LITERAL_REPL="[REDACTED]" awk '
    {
      token = ENVIRON["REDACT_LITERAL_TOKEN"]
      repl = ENVIRON["REDACT_LITERAL_REPL"]
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
}

_redact_patterns() {
  sed -E \
    -e 's/gh[a-z]_[A-Za-z0-9_]{20,}/[REDACTED]/g' \
    -e 's/github_pat_[A-Za-z0-9_]+/[REDACTED]/g' \
    -e 's/x-access-token:[^@[:space:]]+/x-access-token:[REDACTED]/g' \
    -e 's/(Bearer|token)[[:space:]]+[A-Za-z0-9._-]+/\1 [REDACTED]/gI' \
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
  python3 -c "import sys; data=open(sys.argv[1], 'rb').read(); sys.exit(0 if b'\\x00' in data else 1)" "${file}"
}

_sanitize_log_path() {
  local value="$1"
  value="${value//$'\n'/}"
  value="${value//$'\r'/}"
  value="${value//::/}"
  value="${value//%0A/}"
  value="${value//%0a/}"
  value="${value//%0D/}"
  value="${value//%0d/}"
  value="${value//%25/}"
  value="$(printf '%s' "${value}" | sed $'s/\x1b\\[[0-9;]*[A-Za-z]//g')"
  printf '%s' "${value}"
}

_stub_opaque_file() {
  local file="$1"
  local reason="${2:-could not be scanned for job secrets}"
  local tmp safe_file
  tmp="$(mktemp)"
  printf '%s\n' "[REDACTED OPAQUE CONTENT]" "This file was removed from behaviour debug artifacts because it ${reason}." >"${tmp}"
  mv "${tmp}" "${file}"
  safe_file="$(_sanitize_log_path "${file}")"
  echo "::warning::Replaced opaque artifact file: ${safe_file}"
}

_safe_extract_archive() {
  local archive="$1"
  local dest="$2"
  local format="$3"

  python3 - "${archive}" "${dest}" "${format}" "${ARCHIVE_PER_FILE_LIMIT}" "${ARCHIVE_TOTAL_LIMIT}" <<'PY'
import pathlib
import sys
import tarfile
import zipfile

archive, dest, fmt = sys.argv[1:4]
per_file = int(sys.argv[4])
total_limit = int(sys.argv[5])
root = pathlib.Path(dest)
root.mkdir(parents=True, exist_ok=True)
total = 0


def safe_child(name: str) -> pathlib.Path:
    candidate = (root / name).resolve()
    root_resolved = root.resolve()
    if candidate != root_resolved and root_resolved not in candidate.parents:
        raise ValueError(f"path traversal entry: {name}")
    return candidate


if fmt == "zip":
    with zipfile.ZipFile(archive) as zf:
        for info in zf.infolist():
            mode = (info.external_attr >> 16) & 0o170000
            if mode == 0o120000:
                raise ValueError(f"symlink entry: {info.filename}")
            if info.is_dir():
                safe_child(info.filename).mkdir(parents=True, exist_ok=True)
                continue
            if info.file_size > per_file:
                raise ValueError(f"entry exceeds per-file limit: {info.filename}")
            total += info.file_size
            if total > total_limit:
                raise ValueError("archive exceeds total extraction limit")
            target = safe_child(info.filename)
            target.parent.mkdir(parents=True, exist_ok=True)
            with zf.open(info) as src, open(target, "wb") as dst:
                chunk = src.read(per_file + 1)
                if len(chunk) > per_file:
                    raise ValueError(f"entry exceeds per-file limit: {info.filename}")
                dst.write(chunk)
elif fmt == "tar.gz":
    with tarfile.open(archive, "r:gz") as tf:
        for member in tf.getmembers():
            if member.issym() or member.islnk():
                raise ValueError(f"link entry: {member.name}")
            if member.isdir():
                safe_child(member.name).mkdir(parents=True, exist_ok=True)
                continue
            if member.size > per_file:
                raise ValueError(f"entry exceeds per-file limit: {member.name}")
            total += member.size
            if total > total_limit:
                raise ValueError("archive exceeds total extraction limit")
            target = safe_child(member.name)
            target.parent.mkdir(parents=True, exist_ok=True)
            extracted = tf.extractfile(member)
            if extracted is None:
                continue
            data = extracted.read(per_file + 1)
            if len(data) > per_file:
                raise ValueError(f"entry exceeds per-file limit: {member.name}")
            target.write_bytes(data)
else:
    raise ValueError(f"unsupported archive format: {fmt}")
PY
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

_redact_zip_file() {
  local file="$1"
  local tmpdir workdir

  tmpdir="$(mktemp -d)"
  workdir="${tmpdir}/contents"
  mkdir -p "${workdir}"

  if ! _safe_extract_archive "${file}" "${workdir}" zip; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "could not be safely extracted as zip"
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

  if ! _safe_extract_archive "${file}" "${workdir}" tar.gz; then
    rm -rf "${tmpdir}"
    _stub_opaque_file "${file}" "could not be safely extracted as tar.gz"
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
