package modelswitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/bench/gwfeature"
)

// TestGatewayFeatureRuleJSONMatches keeps gwfeature's copy of the routing
// rule byte for byte equal to this package's.
func TestGatewayFeatureRuleJSONMatches(t *testing.T) {
	a := RoutingRule{Name: "n", Description: "d", Enabled: true, CelExpression: "true", Targets: []RoutingTarget{{Provider: "p", Model: "m", Weight: 1}},
		Fallbacks: []string{"f"}, Scope: "global", ScopeID: "s", Priority: 3}
	b := gwfeature.RoutingRule{Name: "n", Description: "d", Enabled: true, CelExpression: "true", Targets: []gwfeature.RoutingTarget{{Provider: "p", Model: "m", Weight: 1}},
		Fallbacks: []string{"f"}, Scope: "global", ScopeID: "s", Priority: 3}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("modelswitch %s\ngwfeature  %s", ja, jb)
	}
	var back RoutingRule
	if err := json.Unmarshal(jb, &back); err != nil {
		t.Fatal(err)
	}
	if jc, _ := json.Marshal(back); string(jc) != string(ja) {
		t.Fatalf("round trip %s", jc)
	}
}

// TestGatewayGetSendsCredential checks Get reads a path with the credential.
func TestGatewayGetSendsCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/version" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`"v2.5.1"`))
	}))
	defer srv.Close()
	var v string
	if err := (Gateway{BaseURL: srv.URL + "/", Credential: "tok", Client: srv.Client()}).Get(context.Background(), "/api/version", &v); err != nil || v != "v2.5.1" {
		t.Fatalf("%q %v", v, err)
	}
}
