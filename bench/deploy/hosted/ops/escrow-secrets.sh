#!/usr/bin/env bash
# Keep an encrypted copy of the hosted server's master key and passwords
# (docs/operations.md, Secret escrow). No value is ever printed.
#
#   deploy/hosted/ops/escrow-secrets.sh [--dir /var/backups/heimdall] [--extra-file PATH]...
#   deploy/hosted/ops/escrow-secrets.sh --verify FILE --identity KEYFILE [--extra-file PATH]...
#
# Write mode (on the server) reads HEIMDALL_MASTER_KEY, HEIMDALL_PG_PASSWORD,
# BIFROST_PG_PASSWORD, BIFROST_SETUP_TOKEN, HEIMDALL_SHARED_RUNNER_SECRET and
# HEIMDALL_PULL_SIGNING_KEY from deploy/hosted/.env and pipes them straight
# into age, encrypted to the public key in /etc/heimdall/escrow.pub
# ($HEIMDALL_ESCROW_PUB). It writes heimdall-secrets-<UTC time>.age (mode
# 0600) and prints only the file name, the number of values and the file's
# sha256. The server holds only the public key: it can write escrow files but
# never read them. --extra-file PATH adds a whole file (for example the
# backup passphrase file and /etc/heimdall/restic.pass).
#
# Verify mode (on the laptop, which holds the private key) decrypts FILE in
# memory and prints, for each name, "matches" or "differs" against the env
# file given by HEIMDALL_ENV_FILE (and against each --extra-file), never a
# value.
#
# Needs age (a single binary: apt install age).
set -euo pipefail
# shellcheck source=../lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"

NAMES='HEIMDALL_MASTER_KEY HEIMDALL_PG_PASSWORD BIFROST_PG_PASSWORD BIFROST_SETUP_TOKEN HEIMDALL_SHARED_RUNNER_SECRET HEIMDALL_PULL_SIGNING_KEY'
PUB=${HEIMDALL_ESCROW_PUB:-/etc/heimdall/escrow.pub}

dir=${HEIMDALL_BACKUP_DIR:-/var/backups/heimdall} verify='' identity=''
extras=()
while [ $# -gt 0 ]; do
	case $1 in
	--dir) dir=${2:?--dir needs a value}; shift 2 ;;
	--extra-file) extras+=("${2:?--extra-file needs a path}"); shift 2 ;;
	--verify) verify=${2:?--verify needs a file}; shift 2 ;;
	--identity) identity=${2:?--identity needs the private key file}; shift 2 ;;
	-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
	*) die "unknown argument: $1" ;;
	esac
done
need age sha256sum base64

# Every line is NAME=VALUE; an extra file is FILE:<path>=<base64 of its bytes>.
emit() {
	local n v f
	for n in $NAMES; do
		v=$(env_get "$n")
		printf '%s=%s\n' "$n" "$v"
	done
	for f in "${extras[@]}"; do
		printf 'FILE:%s=%s\n' "$f" "$(base64 -w0 <"$f")"
	done
}

if [ -n "$verify" ]; then
	[ -n "$identity" ] || die "--verify needs --identity KEYFILE (the private key)"
	[ -f "$verify" ] || die "no such file: $verify"
	age -d -i "$identity" "$verify" | while IFS= read -r line; do
		name=${line%%=*}
		want=${line#*=}
		case $name in
		FILE:*)
			f=${name#FILE:}
			if [ ! -f "$f" ]; then
				echo "$name not checked (no such file here)"
				continue
			fi
			have=$(base64 -w0 <"$f")
			;;
		*) have=$(env_get "$name") ;;
		esac
		if [ "$have" = "$want" ]; then echo "$name matches"; else echo "$name differs"; fi
	done
	exit 0
fi

[ -f "$PUB" ] || die "no public key at $PUB (docs/operations.md, Secret escrow)"
[ -f "$ENV_FILE" ] || die "no env file at $ENV_FILE"
for f in "${extras[@]}"; do [ -f "$f" ] || die "no such file: $f"; done
missing=0
for n in $NAMES; do
	if [ -z "$(env_get "$n")" ]; then
		log "warning: $n is not set in the env file; escrowed as empty"
		missing=$((missing + 1))
	fi
done

mkdir -p "$dir"
ts=$(date -u +%Y%m%dT%H%M%SZ)
out=$dir/heimdall-secrets-$ts.age
umask 077
emit | age -r "$(cat "$PUB")" -o "$out.part"
chmod 600 "$out.part"
mv "$out.part" "$out"
count=$(($(wc -w <<<"$NAMES") + ${#extras[@]} - missing))
echo "wrote $(basename "$out") with $count values; sha256 of the file: $(sha256sum "$out" | cut -d' ' -f1)"
