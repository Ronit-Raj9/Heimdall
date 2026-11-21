package k8s

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

const benchRoot = ".."

func TestPinnedBifrostImageIsTheComposeOne(t *testing.T) {
	img, err := PinnedBifrostImage(benchRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(img, "maximhq/bifrost:") || !strings.Contains(img, "@sha256:") {
		t.Fatalf("image %q", img)
	}
}

func TestBifrostConfigUsesSQLite(t *testing.T) {
	raw, err := bifrostConfig(benchRoot)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"config_store", "logs_store"} {
		st := cfg[k].(map[string]any)
		if st["type"] != "sqlite" || st["enabled"] != true {
			t.Fatalf("%s: %v", k, st)
		}
	}
	if strings.Contains(string(raw), "BIFROST_PG_PASSWORD") || !strings.Contains(string(raw), "env.BIFROST_SETUP_TOKEN") {
		t.Fatal("no database password needed, setup token kept")
	}
	client := cfg["client"].(map[string]any)
	if client["enforce_auth_on_inference"] != true || client["allow_direct_keys"] != false {
		t.Fatalf("client settings the preflight checks changed: %v", client)
	}
	if cfg["logs_store"].(map[string]any)["writer"] == nil {
		t.Fatal("the log writer settings were dropped")
	}
}

func TestDatasheetsFitAConfigMap(t *testing.T) {
	total := 0
	for _, f := range []string{"pricing.json", "model-parameters.json"} {
		b, err := gzipFile(filepath.Join(benchRoot, "deploy", "bifrost", "datasheet", f))
		if err != nil {
			t.Fatal(err)
		}
		total += len(b)
	}
	if total > 900<<10 {
		t.Fatalf("gzipped datasheets are %d bytes, too close to a config map's 1 MiB", total)
	}
}

func TestFaultProxyGetsNoProviderKey(t *testing.T) {
	dep := faultProxyDeployment(GatewayOptions{FaultProxyImage: "bench/bifrost-bench:dev"}, nil)
	raw, _ := json.Marshal(dep)
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "_API_KEY"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("the in-cluster fault proxy must hold no provider key (%s)", k)
		}
	}
	c := dep.Spec.Template.Spec.Containers[0]
	if strings.Join(c.Args, " ") != "proxy --config /etc/bench/bench.yaml" || *c.SecurityContext.ReadOnlyRootFilesystem != true {
		t.Fatalf("proxy container: %+v", c)
	}
	// The proxy serves every slot of gateway.key_slots (live finding: with
	// eight slots in bench.yaml and the proxy's default four, every mint was
	// refused with "unknown slot fp-slot-5").
	slots := []string{"fp-slot-1", "fp-slot-2", "fp-slot-3", "fp-slot-4", "fp-slot-5", "fp-slot-6", "fp-slot-7", "fp-slot-8"}
	c = faultProxyDeployment(GatewayOptions{FaultProxyImage: "x", KeySlots: slots}, nil).Spec.Template.Spec.Containers[0]
	if strings.Join(c.Args, " ") != "proxy --config /etc/bench/bench.yaml --slots "+strings.Join(slots, ",") {
		t.Fatalf("proxy args with key slots: %v", c.Args)
	}
	b := bifrostDeployment("maximhq/bifrost:v2.5.1@sha256:abc", "IfNotPresent", nil)
	if b.Spec.Template.Spec.Containers[0].Image != "maximhq/bifrost:v2.5.1@sha256:abc" || b.Spec.Template.Spec.InitContainers[0].Image != "maximhq/bifrost:v2.5.1@sha256:abc" {
		t.Fatal("bifrost image")
	}
}

func TestGatewayUpNeedsTokens(t *testing.T) {
	if err := GatewayUp(t.Context(), nil, GatewayOptions{Namespace: "gw", BenchRoot: benchRoot, FaultProxyImage: "x"}); err == nil || !strings.Contains(err.Error(), "BIFROST_SETUP_TOKEN") {
		t.Fatalf("no tokens: %v", err)
	}
	if ns, err := GatewayNamespace("http://bifrost.heimdall-gw.svc:8080"); err != nil || ns != "heimdall-gw" {
		t.Fatalf("namespace %q %v", ns, err)
	}
}
