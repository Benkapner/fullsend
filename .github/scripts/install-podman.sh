#!/usr/bin/env bash
# Pin and install podman, working around a crun incompatibility on some
# runner images.
#
# Runner images occasionally pre-install podman 5.x paired with a crun too
# old to support it, breaking sandbox creation with "crun: unknown version
# specified". Pin to the 4.x series, which matches the crun shipped on these
# images, until GitHub Actions runner images ship a compatible crun pairing.
# `apt-get install -y podman` alone does not downgrade an already-installed
# newer version, so the pin's Pin-Priority authorizes the downgrade and
# --allow-downgrades permits apt to execute it.
#
# See #5733. Remove this pin once runner images ship crun >= 1.15
# (podman 5.x's requirement) as standard.
#
# Usage:
#   .github/scripts/install-podman.sh
set -euo pipefail

sudo install -d /etc/apt/preferences.d
printf 'Package: podman\nPin: version 4.*\nPin-Priority: 1001\n' \
  | sudo tee /etc/apt/preferences.d/podman-pin >/dev/null
sudo apt-get update
sudo apt-get install -y --allow-downgrades podman

installed_version="$(podman --version)"
case "${installed_version}" in
  *"version 4."*) ;;
  *)
    echo "::error::Failed to pin podman to the 4.x series (see #5733); got: ${installed_version}"
    exit 1
    ;;
esac

echo "${installed_version}"
