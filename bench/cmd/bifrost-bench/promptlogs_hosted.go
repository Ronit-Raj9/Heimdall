package main

// Prompt logs per organization (lane X03, audit PROD2 N4): the hosted server
// keeps each organization's prompt logging setting in its settings rows and
// reaches the shared gateway's log store to list, delete and expire one
// organization's rows (handlers/promptlogs.go, promptlogs package).

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/promptlogs"
	"github.com/maximhq/bifrost/bench/server"
)

// Environment of the shared gateway's log store. The URL defaults to the
// hosted compose service (http://bifrost:8080) when the shared pool is on;
// the token is the gateway's setup token.
const (
	gatewayLogsURLEnv   = "HEIMDALL_GATEWAY_LOGS_URL"
	gatewayLogsTokenEnv = "BIFROST_SETUP_TOKEN"
	sharedRunnerSecEnv  = "HEIMDALL_SHARED_RUNNER_SECRET"
	defaultGatewayLogs  = "http://bifrost:8080"
)

// promptLogSweepEvery is how often the retention sweep runs.
const promptLogSweepEvery = time.Hour

var promptLogSweepers sync.Map // *hostedEnv -> struct{}

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		if env == nil || env.db == nil {
			return nil, nil
		}
		c := promptlogs.NewClient(gatewayLogsURL(os.Getenv), os.Getenv(gatewayLogsTokenEnv))
		handlers.SetPromptLogs(env.db, c)
		startPromptLogSweep(env, c)
		return nil, nil
	})
}

// gatewayLogsURL is the shared gateway's address, or "" when this server has
// none (no shared pool and no explicit URL).
func gatewayLogsURL(getenv func(string) string) string {
	if u := strings.TrimSpace(getenv(gatewayLogsURLEnv)); u != "" {
		if strings.EqualFold(u, "off") {
			return ""
		}
		return u
	}
	if strings.TrimSpace(getenv(sharedRunnerSecEnv)) != "" {
		return defaultGatewayLogs
	}
	return ""
}

// startPromptLogSweep deletes, every promptLogSweepEvery, each
// organization's gateway rows older than its retention_days. It stops with
// env.ctx.
func startPromptLogSweep(env *hostedEnv, c *promptlogs.Client) {
	if c == nil || env.ctx == nil {
		return
	}
	if _, loaded := promptLogSweepers.LoadOrStore(env, struct{}{}); loaded {
		return
	}
	go func() {
		defer promptLogSweepers.Delete(env)
		tk := time.NewTicker(promptLogSweepEvery)
		defer tk.Stop()
		for {
			select {
			case <-env.ctx.Done():
				return
			case now := <-tk.C:
				orgs, err := env.db.ListOrgs(env.ctx)
				if err != nil {
					env.base.logger.Warn("prompt logs: list organizations failed", "error", err.Error())
					continue
				}
				ids := make([]string, 0, len(orgs))
				for _, o := range orgs {
					ids = append(ids, o.ID)
				}
				n, err := promptlogs.Sweep(env.ctx, env.db, c, ids, now)
				if err != nil {
					env.base.logger.Warn("prompt logs: retention sweep incomplete", "deleted", n, "error", err.Error())
				} else if n > 0 {
					env.base.logger.Info("prompt logs: retention sweep", "deleted", n)
				}
			}
		}
	}()
}
