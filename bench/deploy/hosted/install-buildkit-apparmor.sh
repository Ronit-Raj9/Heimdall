#!/bin/sh
# Loads the heimdall-buildkitd AppArmor profile (see apparmor/heimdall-buildkitd)
# so the rootless BuildKit container can create its user namespace on hosts
# that restrict unprivileged user namespaces (Ubuntu 23.10 and later).
#
# Run once as root on the host, then set HEIMDALL_BUILDKIT_APPARMOR=heimdall-buildkitd
# in deploy/hosted/.env and run deploy/hosted/upgrade.sh. The profile survives
# reboots: AppArmor loads /etc/apparmor.d at boot.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
src="$here/apparmor/heimdall-buildkitd"
dst=/etc/apparmor.d/heimdall-buildkitd

if [ "$(id -u)" -ne 0 ]; then
	echo "run as root: sudo sh $0" >&2
	exit 1
fi
if ! command -v apparmor_parser >/dev/null 2>&1; then
	echo "apparmor_parser not found: this host has no AppArmor, nothing to do" >&2
	exit 0
fi

install -m 0644 "$src" "$dst"
apparmor_parser -r -W "$dst"

if grep -q '^heimdall-buildkitd ' /sys/kernel/security/apparmor/profiles 2>/dev/null; then
	echo "heimdall-buildkitd profile loaded"
	echo "next: set HEIMDALL_BUILDKIT_APPARMOR=heimdall-buildkitd in deploy/hosted/.env and run deploy/hosted/upgrade.sh"
else
	echo "profile file installed but not listed as loaded; check: sudo aa-status" >&2
	exit 1
fi
