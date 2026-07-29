#!/usr/bin/env bash
# Install a pinned podman 4.x static bundle from mgoltzsche/podman-static.
#
# Runner images ship a static podman 5.8.4 bundle under /usr/local/ whose
# crun requirement (>= 1.15) is not met by the system crun (1.14.1),
# breaking sandbox creation with "crun: unknown version specified".
# Apt pinning to 4.x (#5738) installs to /usr/bin/ but the /usr/local/bin/
# binary wins on PATH, so podman --version still reports 5.8.4.
#
# This script replaces the runner image's /usr/local/ podman bundle with a
# pinned 4.x static build from mgoltzsche/podman-static, which bundles a
# compatible crun. The approach mirrors how runner-images itself installs
# podman (self-contained static tarball) and is portable across distros.
#
# See #5733, #5742. Remove this pin once runner images ship crun >= 1.15
# (podman 5.x's requirement) as standard.
#
# Usage:
#   .github/scripts/install-podman.sh
set -euo pipefail

# Pinned podman-static release tag. Keep on the 4.x series until
# runner images bundle a crun compatible with podman 5.x.
PODMAN_STATIC_TAG="v4.9.5"

case "$(uname -m)" in
  x86_64)  arch="amd64" ;;
  aarch64) arch="arm64" ;;
  *)
    echo "::error::Unsupported architecture: $(uname -m)"
    exit 1
    ;;
esac

archive_url="https://github.com/mgoltzsche/podman-static/releases/download/${PODMAN_STATIC_TAG}/podman-linux-${arch}.tar.gz"
archive_path="$(mktemp)"
trap 'rm -f "${archive_path}"' EXIT

echo "Downloading podman static ${PODMAN_STATIC_TAG} (${arch})..."
curl -fsSL --retry 3 --retry-delay 5 -o "${archive_path}" "${archive_url}"

# The archive contains a top-level podman-linux-<arch>/ directory with
# usr/ and etc/ sub-trees. Extracting with --strip-components=1 into /
# overlays /usr/local/bin/podman (and companion binaries), replacing
# the runner image's pre-installed 5.x bundle.
sudo tar -xzf "${archive_path}" -C / --strip-components=1 \
  "podman-linux-${arch}/usr" "podman-linux-${arch}/etc"

installed_version="$(podman --version)"
case "${installed_version}" in
  *"version 4."*) ;;
  *)
    echo "::error::Failed to install podman 4.x (see #5742); got: ${installed_version}"
    exit 1
    ;;
esac

echo "${installed_version}"
