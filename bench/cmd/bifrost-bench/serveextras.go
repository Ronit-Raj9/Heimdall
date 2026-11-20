package main

import (
	"context"
	"errors"
	"flag"

	"github.com/maximhq/bifrost/bench/server"
)

// serveExtras are the serve flags and wiring of the index and trends area:
// the trends API over the derived results index, and the schedule daemon,
// which runs only when asked for.
type serveExtras struct {
	schedules     *bool
	schedulesFile *string
}

// addServeExtras registers the area's serve flags on fs.
func addServeExtras(fs *flag.FlagSet) *serveExtras {
	return &serveExtras{
		schedules:     fs.Bool("schedules", false, "also run the schedule daemon (schedules.yaml) inside the server; off by default"),
		schedulesFile: fs.String("schedules-file", "", "schedules file for -schedules (default: schedules.yaml in the project root)"),
	}
}

// apply wires the trends source into deps and starts the schedule daemon
// when -schedules is set. The results index is created lazily: its first
// trends request builds it. The daemon runs experiments, so it needs the
// gateway and is refused with -offline.
func (x *serveExtras) apply(ctx context.Context, a *app, deps *server.Deps, offline bool) error {
	deps.Trends = trendSource{ix: a.resultsSource(), logger: a.logger, manifests: a.store.LoadManifest} // Tenancy area: the database index when one is wired (servetenancy.go)
	if x.schedules == nil || !*x.schedules {
		return nil
	}
	if offline {
		return errors.New("-schedules runs experiments and needs the gateway; it cannot be combined with -offline")
	}
	return startScheduleDaemon(ctx, a, *x.schedulesFile)
}

// checkHosted refuses the schedule flags on a hosted server: hosted serve
// starts no schedule daemon, so accepting them would silently run nothing
// (audit 8-C-04).
func (x *serveExtras) checkHosted() error {
	if (x.schedules != nil && *x.schedules) || (x.schedulesFile != nil && *x.schedulesFile != "") {
		return errors.New("-schedules and -schedules-file are not supported in hosted mode: the hosted server runs no schedule daemon")
	}
	return nil
}
