package main

import "github.com/maximhq/bifrost/bench/handlers"

// QuickRun area (web parity L28): the org's forecast for POST /quickrun, so
// its answer carries the same estimate as POST /estimate.
func init() {
	serviceHooks = append(serviceHooks, func(a *app, _ *handlers.Services) {
		handlers.SetQuickRunForecast(a.cfg.Root, serveForecast{a: a})
	})
}
