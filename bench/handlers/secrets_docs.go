package handlers

import "github.com/valyala/fasthttp"

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/secrets", Tag: "secrets", Summary: "List the org's secrets (names and dates only; values are never returned)", Response: SecretList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/secrets", Tag: "secrets", Summary: "Store a secret for tools (admin); referenced by name from an external API's auth_env", Request: SecretCreate{}, Status: fasthttp.StatusCreated, Response: Secret{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/secrets/{id}", Tag: "secrets", Summary: "Rotate a secret's value (admin)", Request: SecretRotate{}, Response: Secret{}},
		RouteDoc{Method: "DELETE", Path: APIPrefix + "/secrets/{id}", Tag: "secrets", Summary: "Delete a secret (admin)", Response: SecretDeleted{}},
	)
}
