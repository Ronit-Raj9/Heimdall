package bifrost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// providerCred is a credential of the right shape for spec.
func providerCred(spec schemas.BifrostProviderSpec) ProviderCredential {
	c := ProviderCredential{KeyID: "key_" + spec.ID, Provider: spec.ID, Value: "sk-real-" + spec.ID, Models: []string{"m-1"}}
	if spec.NeedsBaseURL {
		c.BaseURL = "https://" + spec.ID + ".example.com/v1"
	}
	switch spec.KeyConfig {
	case "bedrock_key_config", "bedrock_mantle_key_config", "sagemaker_key_config":
		c.Value = `{"access_key":"AKIAREAL","secret_key":"real-secret","region":"us-east-1"}`
	case "vertex_key_config":
		c.Value = `{"project_id":"proj","region":"us-central1","auth_credentials":{"type":"service_account","private_key":"real-pk"}}`
	case "cloudflare_key_config":
		c.Value = `{"api_key":"cf-real-token","account_id":"acc123"}`
	}
	return c
}

// TestApplyOrgCredentialsEveryProvider checks, for every provider in the
// catalog plus a custom OpenAI-compatible one, the Bifrost provider and key
// bodies the gateway writes and the fault proxy registration (lane A09).
func TestApplyOrgCredentialsEveryProvider(t *testing.T) {
	specs := append(schemas.BifrostProviders(), schemas.CustomProviderSpec("zhipu"))
	for _, spec := range specs {
		t.Run(spec.ID, func(t *testing.T) {
			f := newFakeBifrost(t)
			reg := &fakeOrgRegistrar{}
			cfg := testConfig(f.srv.URL)
			cfg.Claims = reg
			g, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := g.Setup(context.Background()); err != nil { // the shared anthropic provider
				t.Fatal(err)
			}
			c := providerCred(spec)
			if err := g.ApplyOrgCredentials(context.Background(), "org_a", []ProviderCredential{c}); err != nil {
				t.Fatalf("apply: %v", err)
			}
			prov := spec.ID
			f.mu.Lock()
			pcfg, keys := f.state(prov)
			body := *pcfg
			var keyRows []map[string]any
			for _, k := range *keys {
				if strings.HasPrefix(k["name"].(string), "org_") {
					keyRows = append(keyRows, k)
				}
			}
			f.mu.Unlock()
			if body == nil {
				t.Fatalf("provider %s not created", prov)
			}
			nc, _ := body["network_config"].(map[string]any)
			direct := spec.Route == schemas.RouteDirect
			if direct && nc["base_url"] != "" {
				t.Errorf("direct provider base_url %v", nc["base_url"])
			}
			if !direct && nc["base_url"] != testProxy {
				t.Errorf("proxied provider base_url %v, want the fault proxy", nc["base_url"])
			}
			cpc, hasCustom := body["custom_provider_config"].(map[string]any)
			if spec.Custom != hasCustom || (hasCustom && cpc["base_provider_type"] != "openai") {
				t.Errorf("custom_provider_config %v", body["custom_provider_config"])
			}
			if len(keyRows) != orgSlotsPerProvider {
				t.Fatalf("%d keys in Bifrost", len(keyRows))
			}
			k := keyRows[0]
			raw, _ := json.Marshal(k)
			if !direct && strings.Contains(string(raw), "real") {
				t.Errorf("a proxied key in Bifrost carries a real secret: %s", raw)
			}
			if spec.KeyConfig != "" {
				blk, ok := k[spec.KeyConfig].(map[string]any)
				if !ok {
					t.Fatalf("key has no %s: %s", spec.KeyConfig, raw)
				}
				switch spec.KeyConfig {
				case "azure_key_config":
					if blk["endpoint"] != testProxy {
						t.Errorf("azure endpoint %v, want the fault proxy", blk["endpoint"])
					}
				case "bedrock_key_config", "bedrock_mantle_key_config", "sagemaker_key_config":
					if blk["region"] != "us-east-1" || blk["access_key"] != "AKIAREAL" || blk["secret_key"] != "real-secret" {
						t.Errorf("aws block %v", blk)
					}
				case "vertex_key_config":
					if blk["project_id"] != "proj" || blk["region"] != "us-central1" || !strings.Contains(blk["auth_credentials"].(string), "service_account") {
						t.Errorf("vertex block %v", blk)
					}
				case "databricks_key_config":
					if blk["workspace_url"] != strings.TrimRight(c.BaseURL, "/") {
						t.Errorf("databricks block %v", blk)
					}
				case "cloudflare_key_config":
					if blk["account_id"] != "acc123" {
						t.Errorf("cloudflare block %v", blk)
					}
				}
			}
			if direct {
				if len(reg.got) != 0 {
					t.Errorf("direct provider registered %d fault proxy slots", len(reg.got))
				}
				if spec.KeyConfig == "" && k["value"] != c.Value {
					t.Errorf("direct key value not the real credential")
				}
				return
			}
			if len(reg.got) != orgSlotsPerProvider {
				t.Fatalf("fault proxy slots %d", len(reg.got))
			}
			s := reg.got[0]
			wantProv := prov
			if spec.Custom {
				wantProv = "openai"
			}
			wantKey := c.Value
			if spec.KeyConfig == "cloudflare_key_config" {
				wantKey = "cf-real-token"
			}
			if s.Provider != wantProv || s.Key != wantKey || s.BaseURL != c.BaseURL || s.Auth != k["value"] {
				t.Errorf("fault proxy slot %+v (Bifrost value %v)", s, k["value"])
			}
		})
	}
}

// TestApplyOrgCredentialsDirectNeedsNoProxy checks that an organization
// with only direct providers needs no fault proxy control client.
func TestApplyOrgCredentialsDirectNeedsNoProxy(t *testing.T) {
	f := newFakeBifrost(t)
	g, err := New(testConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := schemas.LookupBifrostProvider("bedrock")
	if err := g.ApplyOrgCredentials(context.Background(), "org_a", []ProviderCredential{providerCred(spec)}); err != nil {
		t.Fatalf("direct only: %v", err)
	}
	if err := g.ApplyOrgCredentials(context.Background(), "org_a", []ProviderCredential{{Provider: "groq", Value: "gsk"}}); err == nil {
		t.Fatal("a proxied provider without the fault proxy control API was accepted")
	}
}

// TestOrgRunProviderPrefix checks that an agent naming a provider by
// "<provider>/<model>" resolves to that provider's org slots.
func TestOrgRunProviderPrefix(t *testing.T) {
	f := newFakeBifrost(t)
	reg := &fakeOrgRegistrar{}
	cfg := testConfig(f.srv.URL)
	cfg.Claims = reg
	g, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	creds := []ProviderCredential{
		{KeyID: "k1", Provider: "deepseek", Value: "sk-ds"},
		{KeyID: "k2", Provider: "zhipu", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Value: "zk"},
	}
	if err := g.ApplyOrgCredentials(context.Background(), "org_a", creds); err != nil {
		t.Fatal(err)
	}
	g.BindExperimentOrg("exp_1", "org_a")
	for _, m := range []string{"deepseek/deepseek-chat", "zhipu/glm-4.6"} {
		prov, model := schemas.SplitProviderModel(m, "zhipu")
		rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "exp_1"},
			Agent: &schemas.AgentConfig{Model: model, Gateway: schemas.GatewayPolicy{Provider: prov}}}
		p, got, err := g.runProvider(rc)
		if err != nil || p.Name != prov || got != model || !strings.HasPrefix(p.Slots[0].Name, "org_orga_"+prov) {
			t.Fatalf("%s: %+v %s %v", m, p.Name, got, err)
		}
	}
}
