#!/bin/sh
# Installs gVisor (runsc) on this Docker host and registers it with Docker as
# runtime "runsc" (docs/hosting.md, Shared worker). The shared worker runs
# every org's agents under it, and does not start without it unless
# HEIMDALL_SHARED_ALLOW_RUNC is set.
#
#   sudo sh deploy/hosted/install-gvisor.sh [--check] [--release <name>]
#
#   --check            only report whether Docker has a working runsc runtime
#   --release <name>   gVisor release channel or tag for the binary install
#                      (default "release/latest"; e.g. "release/20251110.0")
#
# On Debian and Ubuntu it uses gVisor's apt repository (signed); elsewhere
# it downloads the release binaries and checks their SHA-512 sums. Then it
# runs `runsc install` (adds the runtime to /etc/docker/daemon.json, keeping
# the rest), reloads Docker (no container restarts) and runs a test
# container under runsc. Safe to run again.
#
# Next: deploy/hosted/upgrade.sh (the shared worker picks gVisor up on start;
# its log says "runtime runsc").
set -eu

release=release/latest
check=''
while [ $# -gt 0 ]; do
	case $1 in
	--check) check=1; shift ;;
	--release) release=${2:?--release needs a value}; shift 2 ;;
	-h | --help) awk 'NR == 1 { next } /^#/ { sub(/^# ?/, ""); print; next } { exit }' "$0"; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 64 ;;
	esac
done

say() { printf '[heimdall] %s\n' "$*" >&2; }
die() { printf '[heimdall] error: %s\n' "$*" >&2; exit 1; }

has_runtime() { docker info --format '{{json .Runtimes}}' 2>/dev/null | grep -q '"runsc"'; }
smoke_test() {
	# A container under runsc reports gVisor's kernel in dmesg.
	docker run --rm --runtime=runsc --network none alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f dmesg 2>/dev/null | grep -qi gvisor
}

command -v docker >/dev/null 2>&1 || die "docker is not installed"
if [ -n "$check" ]; then
	if has_runtime && smoke_test; then
		say "runsc is registered with Docker and runs containers"
		exit 0
	fi
	say "runsc is not usable on this Docker host"
	exit 1
fi
[ "$(id -u)" -eq 0 ] || die "run as root: sudo sh $0"

if command -v runsc >/dev/null 2>&1; then
	say "runsc already installed: $(runsc --version 2>/dev/null | head -n 1)"
elif command -v apt-get >/dev/null 2>&1 && command -v dpkg >/dev/null 2>&1; then
	say "installing runsc from gVisor's apt repository"
	apt-get update -q
	apt-get install -y -q ca-certificates curl gnupg
	curl -fsSL https://gvisor.dev/archive.key | gpg --batch --yes --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
	echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" >/etc/apt/sources.list.d/gvisor.list
	apt-get update -q
	apt-get install -y -q runsc
else
	arch=$(uname -m)
	case $arch in x86_64 | aarch64) ;; *) die "gVisor has no release for $arch" ;; esac
	url=https://storage.googleapis.com/gvisor/releases/$release/$arch
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	say "downloading runsc from $url"
	for f in runsc runsc.sha512 containerd-shim-runsc-v1 containerd-shim-runsc-v1.sha512; do
		curl -fsSL -o "$tmp/$f" "$url/$f"
	done
	(cd "$tmp" && sha512sum -c runsc.sha512 && sha512sum -c containerd-shim-runsc-v1.sha512) >/dev/null || die "SHA-512 check of the downloaded binaries failed"
	install -m 0755 "$tmp/runsc" "$tmp/containerd-shim-runsc-v1" /usr/local/bin/
fi

if has_runtime; then
	say "Docker already lists the runsc runtime"
else
	say "registering runsc with Docker (/etc/docker/daemon.json)"
	runsc install
	# Runtimes are reloadable: SIGHUP, no container restarts.
	if command -v systemctl >/dev/null 2>&1 && systemctl is-active -q docker; then
		systemctl reload docker
	else
		kill -HUP "$(pidof dockerd)" 2>/dev/null || true
	fi
	i=0
	until has_runtime; do
		i=$((i + 1))
		[ $i -lt 20 ] || die "Docker does not list runsc after the reload; restart Docker (sudo systemctl restart docker: it restarts every container) and run this again"
		sleep 1
	done
fi

if smoke_test; then
	say "runsc works: a test container ran under gVisor"
	say "next: deploy/hosted/upgrade.sh, then check the shared worker's log for \"runtime runsc\""
else
	die "Docker lists runsc but a test container did not run under it; see: docker run --rm --runtime=runsc alpine dmesg"
fi
