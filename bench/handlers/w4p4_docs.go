package handlers

import "github.com/maximhq/bifrost/bench/gwfeature"

// W4 Bifrost feature 10, gateway time per plugin (gatewayoverhead.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/experiments/{id}/gateway-overhead", Tag: "integrations",
			Summary: "Bifrost's own time per model call, split by plugin, over this comparison's clean runs; nothing is saved", Response: gwfeature.Effect{}},
	)
}
