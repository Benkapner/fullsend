#!/usr/bin/env bash
#
# lib.sh — Shared helpers for GitLab Runner VM provisioning scripts.
#
# Source this file at the top of any script that needs gl_curl(),
# validate_runner_scope(), build_scope_args(), uses_runner_token(),
# find_runner_ids(), runner_exists(), check_runner_registration(), or
# drain_runner_vm().

# uses_runner_token reports whether RUNNER_TOKEN is set, selecting the
# join-existing-pool (runner-hub) path over GitLab API registration.
uses_runner_token() {
  [ -n "${RUNNER_TOKEN:-}" ]
}

# Wrap curl with GL_TOKEN passed via a temp config file to avoid
# exposing the token in /proc/<pid>/cmdline.
gl_curl() {
  local config old_umask rc
  old_umask=$(umask)
  umask 077
  config=$(mktemp)
  umask "${old_umask}"
  printf 'header = "PRIVATE-TOKEN: %s"\n' "${GL_TOKEN}" > "${config}"
  rc=0
  curl --max-time 30 --connect-timeout 10 -sf -K "${config}" "$@" || rc=$?
  rm -f "${config}"
  return "${rc}"
}

# Validate and resolve PROJECT_ID / GROUP_ID into RUNNER_SCOPE and SCOPE_ID.
# Exactly one of the two env vars must be set and numeric.
# Returns 1 (with an error message on stderr) when neither is set — the
# caller should print a usage hint and exit. All other validation errors
# exit directly because a usage hint would not help.
# Sets:
#   RUNNER_SCOPE  — "project" or "group"
#   SCOPE_ID      — the numeric GitLab ID
validate_runner_scope() {
  if [ -n "${PROJECT_ID:-}" ] && [ -n "${GROUP_ID:-}" ]; then
    echo "ERROR: PROJECT_ID and GROUP_ID are mutually exclusive — set one, not both" >&2
    exit 1
  fi
  if [ -z "${PROJECT_ID:-}" ] && [ -z "${GROUP_ID:-}" ]; then
    echo "ERROR: one of PROJECT_ID or GROUP_ID is required" >&2
    return 1
  fi
  if [ -n "${PROJECT_ID:-}" ]; then
    if ! [[ "${PROJECT_ID}" =~ ^[0-9]+$ ]]; then
      echo "ERROR: PROJECT_ID must be numeric (got: ${PROJECT_ID})" >&2
      exit 1
    fi
    RUNNER_SCOPE="project"
    # shellcheck disable=SC2034  # consumed by callers
    SCOPE_ID="${PROJECT_ID}"
  else
    if ! [[ "${GROUP_ID}" =~ ^[0-9]+$ ]]; then
      echo "ERROR: GROUP_ID must be numeric (got: ${GROUP_ID})" >&2
      exit 1
    fi
    RUNNER_SCOPE="group"
    # shellcheck disable=SC2034  # consumed by callers
    SCOPE_ID="${GROUP_ID}"
  fi
}

# find_runner_ids <description>
#
# Print the IDs of runners visible to GL_TOKEN whose description is exactly
# <description> (create-openshift-vm.sh registers each VM as
# "<namespace>/<vm-name>"), one per line. The lookup is by VM identity only:
# it deliberately does not filter on tags, which are mutable in GitLab, so a
# runner whose tags were edited is still found. Tag compatibility is checked
# separately by check_runner_registration. Uses /runners (user-scoped) rather
# than /runners/all (admin-only). Scans at most 50 pages of 100.
#
# Returns 1 when a GitLab API request fails or returns unparseable JSON, or
# when page 50 is still full (the scan is incomplete), so callers can fail
# closed instead of mistaking an outage or a partial scan for "no runner".
# Requires: GL_TOKEN, GITLAB_URL
find_runner_ids() {
  local description="$1" page page_json ids count complete=false
  page=1
  while [ "${page}" -le 50 ]; do
    page_json=$(gl_curl \
      "${GITLAB_URL}/api/v4/runners?per_page=100&page=${page}" 2>/dev/null) || return 1
    ids=$(printf '%s' "${page_json}" | python3 -c "
import sys, json
for r in json.load(sys.stdin):
    if r.get('description', '') == sys.argv[1]:
        print(r['id'])
" "${description}" 2>/dev/null) || return 1
    if [ -n "${ids}" ]; then
      printf '%s\n' "${ids}"
    fi
    count=$(printf '%s' "${page_json}" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null) || return 1
    if [ "${count}" -lt 100 ]; then
      complete=true
      break
    fi
    page=$((page + 1))
  done
  [ "${complete}" = "true" ]
}

# runner_exists <runner_id>
#
# Ask GitLab whether runner <runner_id> still exists. Returns 0 when it does
# (HTTP 200), 1 when GitLab positively reports it gone (HTTP 404), and 2 when
# the answer is unknown (outage, auth failure, any other status), so callers
# can fail closed instead of mistaking an outage for a deleted runner.
# Requires: GL_TOKEN, GITLAB_URL
runner_exists() {
  local code
  code=$(gl_curl -o /dev/null -w '%{http_code}' \
    "${GITLAB_URL}/api/v4/runners/$1" 2>/dev/null) || true
  if [ "${code}" = "200" ]; then
    return 0
  elif [ "${code}" = "404" ]; then
    return 1
  fi
  return 2
}

# check_runner_registration <runner_id>
#
# Confirm the registration <runner_id> still matches the requested scope,
# access level and tag before a --resume reuses it. Returns 1 and prints the
# reason on stderr when GitLab reports a different access_level, runner type,
# or project/group, when RUNNER_TAG is not among the runner's tags, and when
# the runner cannot be fetched (fail closed). Positive
# evidence of membership is required: details without a non-empty
# groups/projects list matching SCOPE_ID are refused.
# Requires: GL_TOKEN, GITLAB_URL, RUNNER_SCOPE, SCOPE_ID, RUNNER_ACCESS_LEVEL,
#           RUNNER_TAG
check_runner_registration() {
  local runner_id="$1" runner_json
  if ! runner_json=$(gl_curl "${GITLAB_URL}/api/v4/runners/${runner_id}" 2>/dev/null); then
    echo "  could not fetch runner ${runner_id} from ${GITLAB_URL}" >&2
    return 1
  fi
  printf '%s' "${runner_json}" | python3 -c "
import sys, json
scope, scope_id, level, tag = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
r = json.load(sys.stdin)
if r.get('access_level') != level:
    sys.exit('access_level is %r, requested %r' % (r.get('access_level'), level))
if tag not in (r.get('tag_list') or []):
    sys.exit('tag %r is not among the runner tags %r' % (tag, r.get('tag_list')))
want = scope + '_type'
if r.get('runner_type') != want:
    sys.exit('runner_type is %r, requested %r' % (r.get('runner_type'), want))
key = scope + 's'
members = r.get(key)
if not isinstance(members, list) or not members:
    sys.exit('runner details list no %s, cannot confirm it belongs to %s %d' % (key, scope, scope_id))
if scope_id not in [x.get('id') for x in members if isinstance(x, dict)]:
    sys.exit('runner does not belong to %s %d' % (scope, scope_id))
" "${RUNNER_SCOPE}" "${SCOPE_ID}" "${RUNNER_ACCESS_LEVEL}" "${RUNNER_TAG}" >&2
}

# Build scope-specific curl arguments for the GitLab runner registration API.
# Requires: RUNNER_SCOPE, SCOPE_ID (set by validate_runner_scope)
# Sets:
#   scope_args  — array of --data-urlencode flags for curl
build_scope_args() {
  # shellcheck disable=SC2034  # consumed by callers
  scope_args=()
  if [ "${RUNNER_SCOPE}" = "project" ]; then
    # shellcheck disable=SC2034  # consumed by callers
    scope_args=(
      --data-urlencode "runner_type=project_type"
      --data-urlencode "project_id=${PROJECT_ID}"
      --data-urlencode "locked=true"
    )
  else
    # shellcheck disable=SC2034  # consumed by callers
    scope_args=(
      --data-urlencode "runner_type=group_type"
      --data-urlencode "group_id=${GROUP_ID}"
      --data-urlencode "locked=false"
    )
  fi
}

# drain_runner_vm <remote_exec_fn> [runner_user]
#
# Gracefully stop the GitLab runner on a VM so it stops accepting new jobs
# and in-flight jobs can finish, then return. Policy is drain-then-proceed:
# operational failures and cap overruns warn and return 0 so the caller can
# delete or recreate the VM instead of hanging forever.
#
# remote_exec_fn is the name of a function that takes a single command string
# and runs it on the VM over the caller's SSH plumbing (virtctl ssh, or
# gcloud compute ssh --tunnel-through-iap). stdout of the remote command is
# captured; the function must put diagnostics on stderr.
#
# runner_user, when given, is the Unix account gitlab-runner/podman run as on
# the VM (setup.sh's RUNNER_USER — it chowns /etc/gitlab-runner to this user
# and runs the rootless podman socket as it). The config edit and the podman
# query then run as that identity via `sudo -u`, instead of relying on the
# identity the caller's SSH plumbing happens to connect as: for GCP in
# particular there is no equivalent of OpenShift's pinned VM_USER, so a
# different operator draining/deleting a VM than the one who created it can
# connect as a different identity. Without runner_user, the config edit
# still runs via plain sudo (root can always write the file regardless of
# owner), restoring the file's pre-edit owner/mode afterward since `sed -i`
# rewrites through a new inode owned by root:root otherwise; the podman
# query falls back to running as the connecting identity and can
# under-report (wrong rootless namespace) if that identity differs from the
# one gitlab-runner actually runs as.
#
# Idle is detected by watching per-job runner-* containers (podman ps) AND
# confirming gitlab-runner.service is no longer active, not process exit or
# an empty container list alone: gitlab-runner's shutdown_timeout=0 only
# waits ~30s, so the process can exit while a job container is still
# running, and a job still in `podman pull`/prepare_exec (before it has
# created its runner-${JOB_ID} container) shows an empty `podman ps` while
# the service is still very much active. Idle therefore requires both no
# leftover runner-* containers AND gitlab-runner.service reporting anything
# other than "active", bounded throughout by DRAIN_TIMEOUT_SEC.
#
# The remote side sends SIGQUIT (not systemctl stop, which trips Fedora's
# 45s TimeoutStopUSec abort) and runtime-masks the unit so Restart=always
# does not bring the runner back mid-drain. shutdown_timeout is raised to
# the drain cap so SIGQUIT does not kill the job at the ~30s default.
#
# Every remote_exec call (the initial signal, and each poll) runs under a
# command-level timeout bounded by the remaining drain budget, so a hung SSH
# session after connect cannot block the drain past DRAIN_TIMEOUT_SEC. A
# failed poll (SSH/IAP transport blip, virtctl hiccup) warns and retries on
# the next poll interval rather than proceeding immediately — only the
# overall DRAIN_TIMEOUT_SEC cap gives up early, so a single transient error
# does not delete a VM with a job still running.
#
# Env:
#   DRAIN_TIMEOUT_SEC — cap in seconds (default 600). Non-numeric values
#                       fall back to 600.
#   DRAIN_POLL_SEC    — poll interval in seconds (default 5). Used by tests.
drain_runner_vm() {
  local remote_exec="$1"
  local runner_user="${2:-}"
  local timeout_sec poll_sec cmd_timeout start now elapsed remaining rc
  local remote_cmd sudo_prefix ps_cmd poll_output containers svc_status

  if [ -z "${remote_exec}" ]; then
    echo "ERROR: drain_runner_vm requires a remote exec function" >&2
    return 1
  fi

  if [ -n "${runner_user}" ] && ! [[ "${runner_user}" =~ ^[a-z_][a-z0-9_-]*$ ]]; then
    echo "ERROR: drain_runner_vm: runner_user must be a plain Unix user name (got: ${runner_user})" >&2
    return 1
  fi

  timeout_sec="${DRAIN_TIMEOUT_SEC:-600}"
  if ! [[ "${timeout_sec}" =~ ^[0-9]+$ ]]; then
    timeout_sec=600
  fi
  poll_sec="${DRAIN_POLL_SEC:-5}"
  if ! [[ "${poll_sec}" =~ ^[0-9]+$ ]] || [ "${poll_sec}" -lt 1 ]; then
    poll_sec=5
  fi

  echo "  draining runner (cap ${timeout_sec}s)..."

  sudo_prefix="sudo"
  if [ -n "${runner_user}" ]; then
    sudo_prefix="sudo -u ${runner_user}"
  fi

  # Expand timeout_sec/sudo_prefix into the remote script; everything else
  # is literal. The remote script tracks per-step failures in $status and
  # exits non-zero if any of them fail, so an in-guest signal failure is
  # caught by the same rc check below as a transport failure — this used to
  # end with an unconditional "true", so a fully-failed drain could still
  # report success.
  #
  # Without runner_user, the config edit runs via plain `sudo`, whose
  # `sed -i` rewrites the file through a new inode owned by root:root —
  # clobbering setup.sh's chown of /etc/gitlab-runner to RUNNER_USER, which
  # the gitlab-runner service (User=${RUNNER_USER}) needs to keep reading
  # its own config. Stat the pre-edit owner/mode and restore them after the
  # sed calls so a plain-sudo edit does not change who owns the file.
  if [ -n "${runner_user}" ]; then
    remote_cmd=$(cat <<EOF
status=0
cfg=/etc/gitlab-runner/config.toml
if ${sudo_prefix} test -f "\$cfg"; then
  ${sudo_prefix} sed -i '/^shutdown_timeout/d' "\$cfg" || status=1
  ${sudo_prefix} sed -i '1i shutdown_timeout = ${timeout_sec}' "\$cfg" || status=1
fi
sudo systemctl kill --kill-whom=main -s HUP gitlab-runner.service >/dev/null 2>&1 || status=1
sudo systemctl mask --runtime gitlab-runner.service >/dev/null 2>&1 || status=1
sudo systemctl kill --kill-whom=main -s QUIT gitlab-runner.service >/dev/null 2>&1 || status=1
exit "\$status"
EOF
)
  else
    remote_cmd=$(cat <<EOF
status=0
cfg=/etc/gitlab-runner/config.toml
if sudo test -f "\$cfg"; then
  cfg_owner=\$(sudo stat -c '%U:%G' "\$cfg" 2>/dev/null) || cfg_owner=""
  cfg_mode=\$(sudo stat -c '%a' "\$cfg" 2>/dev/null) || cfg_mode=""
  sudo sed -i '/^shutdown_timeout/d' "\$cfg" || status=1
  sudo sed -i '1i shutdown_timeout = ${timeout_sec}' "\$cfg" || status=1
  if [ -n "\$cfg_owner" ]; then
    sudo chown "\$cfg_owner" "\$cfg" || status=1
  fi
  if [ -n "\$cfg_mode" ]; then
    sudo chmod "\$cfg_mode" "\$cfg" || status=1
  fi
fi
sudo systemctl kill --kill-whom=main -s HUP gitlab-runner.service >/dev/null 2>&1 || status=1
sudo systemctl mask --runtime gitlab-runner.service >/dev/null 2>&1 || status=1
sudo systemctl kill --kill-whom=main -s QUIT gitlab-runner.service >/dev/null 2>&1 || status=1
exit "\$status"
EOF
)
  fi

  # The poll command reports both leftover runner-* containers and whether
  # gitlab-runner.service is still active, so idle detection (below) can
  # require both signals instead of trusting an empty container list alone.
  # `podman ps` failures propagate through $status (a real transport/podman
  # problem); `systemctl is-active`'s own non-zero exit for inactive/failed
  # units is deliberately swallowed so it never masks a podman failure.
  if [ -n "${runner_user}" ]; then
    ps_cmd="status=0; containers=\$(sudo -u ${runner_user} podman ps --format '{{.Names}}') || status=1; svc=\$(systemctl is-active gitlab-runner.service 2>/dev/null || true); printf '%s\n' \"\${containers}\"; printf '__SVC__%s\n' \"\${svc}\"; exit \"\${status}\""
  else
    ps_cmd="status=0; containers=\$(podman ps --format '{{.Names}}') || status=1; svc=\$(systemctl is-active gitlab-runner.service 2>/dev/null || true); printf '%s\n' \"\${containers}\"; printf '__SVC__%s\n' \"\${svc}\"; exit \"\${status}\""
  fi

  # A DURATION of 0 disables GNU timeout's cap entirely, so floor the
  # command-level timeout at 1s (the overall drain-cap checks below still
  # use the real timeout_sec, including 0).
  cmd_timeout="${timeout_sec}"
  if [ "${cmd_timeout}" -lt 1 ]; then
    cmd_timeout=1
  fi

  # remote_exec is a shell function (gcp_drain_ssh / ocp_drain_ssh), not an
  # executable on PATH, so `timeout N "${remote_exec}"` can't exec it
  # directly. Export it and run it through `bash -c` instead, which inherits
  # exported functions from the environment; timeout then bounds that bash
  # process.
  if declare -F "${remote_exec}" >/dev/null 2>&1; then
    # shellcheck disable=SC2163  # exports the function named by $remote_exec, not the variable itself
    export -f "${remote_exec}"
  fi

  start=$(date +%s)
  rc=0
  timeout "${cmd_timeout}" bash -c '"$0" "$1"' "${remote_exec}" "${remote_cmd}" || rc=$?
  if [ "${rc}" -ne 0 ]; then
    echo "  WARN: drain signal failed (exit ${rc}) — polling for idle anyway" >&2
  fi

  while true; do
    now=$(date +%s)
    elapsed=$(( now - start ))
    if [ "${elapsed}" -ge "${timeout_sec}" ]; then
      echo "  WARN: drain cap ${timeout_sec}s reached — proceeding" >&2
      return 0
    fi
    remaining=$(( timeout_sec - elapsed ))
    cmd_timeout="${remaining}"
    if [ "${cmd_timeout}" -lt 1 ]; then
      cmd_timeout=1
    fi

    rc=0
    poll_output=$(timeout "${cmd_timeout}" bash -c '"$0" "$1"' "${remote_exec}" "${ps_cmd}" 2>/dev/null) || rc=$?
    if [ "${rc}" -ne 0 ]; then
      # A transport blip (SSH/IAP reconnect, virtctl hiccup) does not mean
      # the job is done — retry on the next poll interval instead of
      # proceeding immediately, so a single failure cannot short-circuit
      # the drain. The cap check above still bounds total retry time.
      echo "  WARN: drain poll failed (exit ${rc}) — retrying" >&2
    else
      svc_status=$(printf '%s\n' "${poll_output}" | grep '^__SVC__' | sed 's/^__SVC__//' || true)
      containers=$(printf '%s\n' "${poll_output}" | grep '^runner-' || true)
      # Idle requires both no leftover runner-* containers AND the service
      # reporting anything other than "active" — an empty container list
      # alone (e.g. a job still in podman pull/prepare_exec, before its
      # runner-${JOB_ID} container exists) must not be treated as idle
      # while gitlab-runner.service is still running.
      if [ -z "${containers}" ] && [ "${svc_status}" != "active" ]; then
        echo "  OK: runner idle"
        return 0
      fi
    fi

    now=$(date +%s)
    elapsed=$(( now - start ))
    if [ "${elapsed}" -ge "${timeout_sec}" ]; then
      echo "  WARN: drain cap ${timeout_sec}s reached — proceeding" >&2
      return 0
    fi
    remaining=$(( timeout_sec - elapsed ))
    if [ "${remaining}" -lt "${poll_sec}" ]; then
      sleep "${remaining}"
    else
      sleep "${poll_sec}"
    fi
  done
}
