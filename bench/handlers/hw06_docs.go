package handlers

import (
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/valyala/fasthttp"
)

// Fix wave 3 lane H06 (N216): the org's own image registry on a sandbox
// platform's card (handlers/workerplatforms.go).
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/worker-platforms/{platform}/registry", Tag: "runners",
			Summary: "Save the image registry the platform pulls Heimdall's images from; Heimdall signs in first and never returns the token (400 for a bad path, 422 when the registry refuses)",
			Auth:    AuthWrite, Request: PlatformRegistryRequest{}, Response: sandbox.RegistryView{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/worker-platforms/{platform}/registry/remove", Tag: "runners",
			Summary: "Remove the platform's image registry login",
			Auth:    AuthWrite, Status: fasthttp.StatusNoContent},
	)
}
