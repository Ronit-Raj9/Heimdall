#!/usr/bin/env bash
# Check once that the hosted site answers, from another machine (the
# owner's laptop), and alert on the second failure in a row
# (docs/operations.md, Down alert). Run every 5 minutes by the user timer
# heimdall-uptime.timer.
#
#   deploy/hosted/ops/uptime-probe.sh
#
# Settings in ~/.config/heimdall/uptime.env ($HEIMDALL_UPTIME_ENV):
#   HEIMDALL_UPTIME_URL      the health address, for example
#                            https://20-70-160-61.sslip.io/healthz
#   HEIMDALL_UPTIME_WEBHOOK  a Slack incoming webhook URL (never printed)
#
# Two failures in a row post "Heimdall is down: <url> did not answer since
# <time>" and show a desktop notice; the first success after that posts
# "Heimdall is back: down for <minutes> minutes". State is kept in
# ~/.local/state/heimdall-uptime ($HEIMDALL_UPTIME_STATE).
set -euo pipefail
# shellcheck source=../lib.sh
. "$(dirname "${BASH_SOURCE[0]}")/../lib.sh"

case ${1:-} in
-h | --help) header_help "${BASH_SOURCE[0]}"; exit 0 ;;
'') ;;
*) die "unknown argument: $1" ;;
esac

CONF=${HEIMDALL_UPTIME_ENV:-$HOME/.config/heimdall/uptime.env}
STATE=${HEIMDALL_UPTIME_STATE:-$HOME/.local/state/heimdall-uptime}
[ -f "$CONF" ] || die "no settings at $CONF"
url=$(sed -n 's/^HEIMDALL_UPTIME_URL=//p' "$CONF" | tail -n 1)
hook=$(sed -n 's/^HEIMDALL_UPTIME_WEBHOOK=//p' "$CONF" | tail -n 1)
[ -n "$url" ] || die "HEIMDALL_UPTIME_URL is not set in $CONF"

# post TEXT: to the webhook (its URL goes to curl on standard input, never on
# the command line) and as a desktop notice when one can be shown.
post() {
	if [ -n "$hook" ]; then
		printf 'url = "%s"\n' "$hook" | curl -fsS --max-time 10 -K - -H 'Content-Type: application/json' \
			--data "$(jq -cn --arg t "$1" '{text: $t}')" >/dev/null || log "could not post to the webhook"
	fi
	if command -v notify-send >/dev/null 2>&1; then notify-send "Heimdall" "$1" || true; fi
}

# State: failures in a row, the time of the first failure, and whether the
# down message was sent.
fails=0 since=0 alerted=0
# shellcheck disable=SC1090
[ -f "$STATE" ] && . "$STATE"
now=$(date -u +%s)

if curl -fsS --max-time 10 -o /dev/null "$url"; then
	if [ "$alerted" = 1 ]; then
		post "Heimdall is back: down for $(((now - since + 59) / 60)) minutes"
	fi
	fails=0 since=0 alerted=0
else
	[ "$fails" -gt 0 ] || since=$now
	fails=$((fails + 1))
	if [ "$fails" -ge 2 ] && [ "$alerted" = 0 ]; then
		post "Heimdall is down: $url did not answer since $(date -u -d "@$since" '+%Y-%m-%d %H:%M UTC')"
		alerted=1
	fi
fi
mkdir -p "$(dirname "$STATE")"
printf 'fails=%s\nsince=%s\nalerted=%s\n' "$fails" "$since" "$alerted" >"$STATE"
