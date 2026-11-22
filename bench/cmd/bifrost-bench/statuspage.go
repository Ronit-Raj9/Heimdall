package main

// Lane Z14: the public status page (bench/statuspage). The server builds
// it from live checks (readiness, the worker and queue gauges of /metrics,
// optional HTTP probes in HEIMDALL_STATUS_PROBES) and the incident file
// the operator edits with `bifrost-bench status incident ...`
// (statuscmd.go). Hosted mode serves it as a platform area, local mode
// through server.Deps.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/selfmetrics"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/statuspage"
)

// StatusFileEnv names the incident file; empty: <data>/status/incidents.json
// next to the runs root.
const StatusFileEnv = "HEIMDALL_STATUS_FILE"

// StatusProbesEnv lists extra HTTP probes: id=url,id2=url2.
const StatusProbesEnv = "HEIMDALL_STATUS_PROBES"

func init() {
	hostedAreas = append(hostedAreas, func(env *hostedEnv) (server.RouteRegistrar, error) {
		return handlers.NewStatusPageHandler(env.base.cfg.Server.Mode, newStatusService(env.base.cfg, readyChecks(env.base))), nil
	})
}

// statusFilePath is where the incidents live.
func statusFilePath(cfg *lib.Config) string {
	if v := strings.TrimSpace(os.Getenv(StatusFileEnv)); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(cfg.Path(cfg.RunsRoot)), "status", "incidents.json")
}

// newStatusService builds the page's checks: the API itself, the database
// and run storage from the readiness checks, the evaluation workers and
// queue from the /metrics gauges, then the configured HTTP probes.
func newStatusService(cfg *lib.Config, ready []handlers.ReadyCheck) *statuspage.Service {
	checks := []statuspage.Check{statuspage.APICheck()}
	var db []handlers.ReadyCheck
	for _, c := range ready {
		switch c.Name {
		case "database", "migrations":
			db = append(db, c)
		case "runs_root":
			checks = append(checks, statuspage.ErrorCheck(statuspage.ComponentStorage, "Run storage", "results cannot be saved", c.Check))
		}
	}
	if len(db) > 0 {
		checks = append(checks, statuspage.ErrorCheck(statuspage.ComponentDatabase, "Database", "not reachable", func(ctx context.Context) error {
			for _, r := range handlers.RunReadyChecks(ctx, db) {
				if !r.OK {
					return errStatusCheck
				}
			}
			return nil
		}))
	}
	checks = append(checks, statuspage.WorkersCheck(selfmetrics.Default), statuspage.QueueCheck(selfmetrics.Default))
	checks = append(checks, statuspage.ParseProbes(os.Getenv(StatusProbesEnv), nil)...)
	return statuspage.New(checks, statuspage.NewStore(statusFilePath(cfg)), benchVersion)
}

var errStatusCheck = errors.New("check failed")
