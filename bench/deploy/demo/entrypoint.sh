#!/bin/sh
# Seed the recorded experiments once, then serve them read-only.
set -eu
if [ ! -e /var/lib/heimdall/runs/.seeded ]; then
	mkdir -p /var/lib/heimdall/runs
	cp -R /srv/heimdall/seed/e_pilot_mixed_nc93 /srv/heimdall/seed/e_smoke_sdoj /var/lib/heimdall/runs/
	touch /var/lib/heimdall/runs/.seeded
fi
exec /usr/local/bin/bifrost-bench serve -offline -config /srv/heimdall/demo/bench.demo.yaml
