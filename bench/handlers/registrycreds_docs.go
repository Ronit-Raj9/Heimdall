package handlers

import "github.com/valyala/fasthttp"

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: RegistryCredentialsPath, Tag: "secrets", Summary: "List the org's private image registry logins (no passwords; each names a secret)", Response: RegistryCredentialList{}},
		RouteDoc{Method: "POST", Path: RegistryCredentialsPath, Tag: "secrets", Summary: "Add a private image registry login (admin): Docker Hub, GHCR, ECR, GCR or Artifact Registry, ACR, other; the password lives in a named secret", Request: registryInputDoc{}, Status: fasthttp.StatusCreated, Response: RegistryCredential{}},
		RouteDoc{Method: "DELETE", Path: RegistryCredentialsPath + "/{id}", Tag: "secrets", Summary: "Remove a private image registry login (admin); the named secret stays", Response: RegistryCredentialDeleted{}},
	)
}

// registryInputDoc documents the POST body (registrycreds.Input).
type registryInputDoc struct {
	Kind       string `json:"kind"`               // dockerhub | ghcr | ecr | gcr | acr | other
	Host       string `json:"host,omitempty"`     // filled for dockerhub and ghcr
	Username   string `json:"username,omitempty"` // ECR: access key id; Google: _json_key (filled)
	SecretName string `json:"secret_name"`        // a Settings > Secrets name holding the password, token, secret access key or service account JSON
	Region     string `json:"region,omitempty"`   // ECR, read from the host when empty
}
