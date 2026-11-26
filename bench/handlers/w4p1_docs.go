package handlers

import (
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/schemas"
)

// W4 Bifrost feature tests (gatewaytests.go, gatewayreportcard.go).

// gatewayFeaturesDoc is GET /gateway/features.
type gatewayFeaturesDoc struct {
	Features []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"features"`
}

// gatewayDraftDoc is POST /gateway/tests/draft.
type gatewayDraftDoc struct {
	Experiment schemas.Experiment `json:"experiment"`
	Notes      []string           `json:"notes"`
}

// gatewayEffectsDoc is GET /gateway/effects.
type gatewayEffectsDoc struct {
	Effects []gwfeature.Effect `json:"effects"`
}

// gatewaySourceRequestDoc is POST /gateway/source.
type gatewaySourceRequestDoc struct {
	GatewayURL       string `json:"gateway_url"`
	CredentialSecret string `json:"credential_secret,omitempty"`
}

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/features", Tag: "integrations",
			Summary: "The Bifrost features this server can test", Response: gatewayFeaturesDoc{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/gateway/tests/draft", Tag: "integrations",
			Summary: "Draft a comparison whose setups differ in one Bifrost setting; nothing is planned or started", Request: gwfeature.DraftRequest{}, Response: gatewayDraftDoc{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/experiments/{id}/gateway-effect", Tag: "integrations",
			Summary: "What the tested Bifrost setting did in this comparison; 404 when it is not a Bifrost feature test",
			Query:   []QueryDoc{{Name: "refresh", Description: "1 recomputes the report even when the runs did not change"}}, Response: gwfeature.Effect{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/effects", Tag: "integrations",
			Summary: "Saved Bifrost feature reports, newest first",
			Query:   []QueryDoc{{Name: "feature", Description: "only this feature id"}, {Name: "limit", Description: "1 to 100, default 20"}}, Response: gatewayEffectsDoc{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/report-card", Tag: "integrations",
			Summary: "One line per Bifrost feature: in use or not, and its newest measured effect", Response: GatewayReportCard{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/gateway/source", Tag: "integrations",
			Summary: "The connection to the org's own Bifrost (host only, never the credential)", Response: GatewaySourceInfo{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/gateway/source", Tag: "integrations",
			Summary: "Connect the org's own Bifrost; the credential is the name of an org secret", Request: gatewaySourceRequestDoc{}, Response: GatewaySourceInfo{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/gateway/source/remove", Tag: "integrations",
			Summary: "Forget the connection to the org's own Bifrost", Response: GatewaySourceInfo{}},
	)
}
