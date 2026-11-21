package toolproxy

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
)

func TestCheckOrgSecretName(t *testing.T) {
	for _, ok := range []string{"TAVILY_API_KEY", "A", "X1_2"} {
		if err := CheckOrgSecretName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "lower", "1ABC", "A-B", "HEIMDALL_MASTER_KEY", "AWS_SECRET_ACCESS_KEY", "PATH", "BENCH_WRITE_TOKEN"} {
		if CheckOrgSecretName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOrgSecretsGetenvInjectsRouteCredential(t *testing.T) {
	ctx := context.Background()
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Create(ctx, "o1", keystore.KindSecret, keystore.Put{Label: "TAVILY_API_KEY", Value: "tvly-secret"}, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Create(ctx, "o2", keystore.KindSecret, keystore.Put{Label: "OTHER_KEY", Value: "o2-secret"}, "a"); err != nil {
		t.Fatal(err)
	}
	// A provider key with the same label must never be injected as a secret.
	if _, err := ks.Create(ctx, "o1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "OTHER_KEY", Value: "prov"}, "a"); err != nil {
		t.Fatal(err)
	}
	rs := &Routes{Routes: []Route{
		{Service: "search", Protocol: ProtocolExternal, External: &ExternalRoute{Mode: "live", AuthEnv: "TAVILY_API_KEY"}},
		{Service: "other", Protocol: ProtocolExternal, External: &ExternalRoute{Mode: "live", AuthEnv: "OTHER_KEY"}},
		{Service: "tape", Protocol: ProtocolExternal, External: &ExternalRoute{Mode: "replay", AuthEnv: "TAPE_KEY"}},
	}}
	if got := RouteSecretNames(rs); len(got) != 2 || got[0] != "OTHER_KEY" || got[1] != "TAVILY_API_KEY" {
		t.Fatalf("names = %v", got)
	}
	getenv, err := OrgSecretsGetenv(ctx, ks, "o1", rs, func(n string) string {
		if n == "OTHER_KEY" {
			return "from-env"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := getenv("TAVILY_API_KEY"); got != "tvly-secret" {
		t.Errorf("TAVILY_API_KEY = %q", got)
	}
	if got := getenv("OTHER_KEY"); got != "from-env" {
		t.Errorf("OTHER_KEY = %q, want fallback (o2's secret and o1's provider key must not leak)", got)
	}
}
