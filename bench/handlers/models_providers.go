package handlers

import (
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Bifrost provider list (lane A09): every provider Bifrost core serves, with
// what the key form needs (base URL, credential fields) and how a run's
// calls reach it (proxied through the fault proxy, or direct).

// BifrostProvidersResponse is GET /models/providers.
type BifrostProvidersResponse struct {
	Providers []schemas.BifrostProviderSpec `json:"providers"`
	// Custom describes a custom OpenAI-compatible provider: any lowercase
	// name that is no built-in, with a base URL ending in /v1. Agents then
	// use the model "<name>/<model>".
	Custom schemas.BifrostProviderSpec `json:"custom"`
}

func init() {
	RegisterOrgArea("models-providers", func(env OrgEnv, r *router.Router) {
		r.GET(APIPrefix+"/models/providers", env.R(func(ctx *fasthttp.RequestCtx) {
			lib.SendJSON(ctx, env.Mode, fasthttp.StatusOK, bifrostProvidersView())
		}))
	})
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: APIPrefix + "/models/providers", Tag: "models",
		Summary:  "Every provider Bifrost serves: key fields, base URL needs and route (proxied or direct); plus the custom OpenAI-compatible form",
		Response: BifrostProvidersResponse{}})
}

func bifrostProvidersView() BifrostProvidersResponse {
	return BifrostProvidersResponse{Providers: schemas.BifrostProviders(), Custom: schemas.CustomProviderSpec("my-provider")}
}
