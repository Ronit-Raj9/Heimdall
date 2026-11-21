package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
)

func TestOrgSecretSource(t *testing.T) {
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ks.Create(ctx, "org_a", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "SEARCH_KEY", Value: "a-search"}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := ks.Create(ctx, "org_b", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "AGENT_TOKEN", Value: "b-token"}, "u"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_TOKEN", "env-token")
	t.Setenv("SEARCH_KEY", "env-search")

	hosted := orgSecretSource{keys: ks, orgID: "org_a"}
	got, err := hosted.ResolveSecrets(ctx, []string{"SEARCH_KEY", "AGENT_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["SEARCH_KEY"] != "a-search" {
		t.Fatalf("hosted got %v: another org's secret or the server env leaked", got)
	}

	local := orgSecretSource{keys: ks, orgID: "org_a", envFallback: true}
	got, err = local.ResolveSecrets(ctx, []string{"SEARCH_KEY", "AGENT_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if got["SEARCH_KEY"] != "a-search" || got["AGENT_TOKEN"] != "env-token" {
		t.Fatalf("local got %v: org secret first, then the environment", got)
	}
}
