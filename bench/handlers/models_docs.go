package handlers

import "github.com/valyala/fasthttp"

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/models", Tag: "models", Summary: "Providers with suggested models, the org's provider keys (never their values) and the default model",
			Response: ModelsCatalogResponse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/models/keys", Tag: "models", Summary: "List the org's provider keys; only the last 4 characters of each value are shown",
			Response: ModelKeysResponse{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/models/keys", Tag: "models", Summary: "Store a provider key (encrypted at rest); the value is never returned",
			Auth: AuthWrite, Request: ModelKeyRequest{}, Status: fasthttp.StatusCreated, Response: ModelKeyResponse{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/models/keys/{id}", Tag: "models", Summary: "One provider key's metadata",
			Response: ModelKeyResponse{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/models/keys/{id}", Tag: "models", Summary: "Update a provider key's label, base URL, models or budget; an empty value keeps the stored one",
			Auth: AuthWrite, Request: ModelKeyRequest{}, Response: ModelKeyResponse{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/models/keys/{id}", Tag: "models", Summary: "Delete a provider key",
			Auth: AuthWrite, Status: fasthttp.StatusNoContent, NoContent: true},
		RouteDoc{Method: "POST", Path: APIPrefix + "/models/keys/{id}/test", Tag: "models", Summary: "Test a provider key with its cheapest authenticated call (a model list where the provider has one) and record the status",
			Auth: AuthWrite, Response: ModelKeyTestResponse{}},
	)
}
