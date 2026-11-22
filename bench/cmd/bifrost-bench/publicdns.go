package main

import (
	"context"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// publicDNSProbeHost is the name looked up at start-up to prove the public
// DNS servers answer.
const publicDNSProbeHost = "example.com"

// publicDNSLookup is the lookup logPublicDNS runs; tests replace it.
var publicDNSLookup = func(ctx context.Context, host string) error {
	_, err := utils.LookupIPAddr(ctx, utils.PublicResolver(), host)
	return err
}

// logPublicDNS checks once, at hosted start-up, that the public DNS servers
// of guarded outbound calls (utils.PublicResolver, HEIMDALL_PUBLIC_DNS)
// answer, and logs the result. Every guarded call (webhooks, Test my MCP
// server, Connect) fails when they do not, so the log line says how to fall
// back. It returns the line it logged.
func logPublicDNS(ctx context.Context, logger schemas.Logger) string {
	servers := "system resolver"
	if s := utils.PublicDNSServers(); len(s) > 0 {
		servers = strings.Join(s, ",")
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := publicDNSLookup(lctx, publicDNSProbeHost); err != nil {
		line := "public DNS unreachable (" + servers + "): guarded outbound calls will fail; set " + utils.PublicDNSEnv + "=system to use the container resolver"
		logger.Warn(line, "error", err.Error())
		return line
	}
	line := "public DNS ok (" + servers + ")"
	logger.Info(line)
	return line
}
