package main

import (
	"github.com/maximhq/bifrost/bench/handlers"
)

// F4 key delivery (lane L05): the lease credentials route finds the
// organization's coordinator in the hosted runner hub. An organization
// without a coordinator yet has no lease, so the route answers 503.
func init() {
	handlers.SetLeaseHolders(func(orgID string) handlers.LeaseHolders {
		var out handlers.LeaseHolders
		runnerEnvs.Range(func(_, v any) bool {
			rs, ok := v.(*hostedRunners)
			if !ok || rs == nil || rs.hub == nil {
				return true
			}
			if c, ok := rs.hub.Get(orgID); ok && c != nil {
				out = c
				return false
			}
			return true
		})
		return out
	})
}
