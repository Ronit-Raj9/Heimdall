package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X20b run debugging: the run page reads and releases breakpoints through
// the fault proxy this process runs its experiments behind.
func init() {
	serviceHooks = append(serviceHooks, func(a *app, s *handlers.Services) {
		if a.reg == nil {
			return
		}
		if hc, ok := a.reg.Fault.(schemas.HoldController); ok {
			s.Holds = hc
		}
	})
}
