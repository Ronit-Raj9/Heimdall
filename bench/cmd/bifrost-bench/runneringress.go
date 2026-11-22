package main

// Y03 (G1 P0-1): a worker's public ingress. With remote_tools.ingress_listen
// set, the worker serves its model gateway (/gw) and its live runs' tool
// gates (/rt) on that address, for live runs only (bench/publicingress). The
// hosted stack's Caddy forwards https://<domain>/gw and /rt to the shared
// worker's listener, so deployed agents and cloud sandboxes reach both from
// the internet with the run's own key or token.

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/publicingress"
)

// startPublicIngress starts the listener when the config asks for one. A
// listener that cannot start is logged, not fatal: local runs still work,
// and the reachability refusals keep remote runs from scoring 0.
func startPublicIngress(ctx context.Context, a *app) {
	listen := a.cfg.RemoteTools.IngressListen
	if listen == "" {
		return
	}
	gw := a.cfg.Gateway.URL
	if gw == "" {
		gw = a.cfg.Gateway.AgentURL
	}
	h, err := publicingress.NewHandler(publicingress.Default, gw)
	if err != nil {
		a.logger.Error("public ingress not started", "error", err.Error())
		return
	}
	h.Logf = func(format string, args ...any) { a.logger.Warn(fmt.Sprintf(format, args...)) }
	go func() {
		if err := publicingress.Serve(ctx, listen, h); err != nil {
			a.logger.Error("public ingress stopped", "error", err.Error())
		}
	}()
	a.logger.Info("public ingress listening", "listen", listen, "gateway_public_url", a.cfg.Gateway.PublicURL,
		"tools_public_url", a.cfg.RemoteTools.PublicURL, "run_per_minute", publicingress.DefaultRunPerMinute)
}
