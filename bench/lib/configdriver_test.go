package lib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "bench.yaml")
	if err := os.WriteFile(p, []byte("runs_root: runs\nserver: {listen: 127.0.0.1:8090, mode: live}\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigKubernetesSection(t *testing.T) {
	p := writeConfig(t, `environment: {driver: kubernetes}
kubernetes:
  context: kind-hd-x3
  namespace_prefix: hd-run
  gateway_url: http://bifrost.gw.svc:8080
  node_selector: {pool: agents}
  tolerations: [{key: dedicated, operator: Equal, value: agents, effect: NoSchedule}]
`)
	t.Setenv("BENCH_KUBERNETES_IMAGE_PULL_POLICY", "Never")
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	k := cfg.Kubernetes
	if cfg.Environment.DriverName() != "kubernetes" || k.Context != "kind-hd-x3" || k.NamespacePrefix != "hd-run" || k.GatewayURL != "http://bifrost.gw.svc:8080" ||
		k.NodeSelector["pool"] != "agents" || len(k.Tolerations) != 1 || k.Tolerations[0].Effect != "NoSchedule" || k.ImagePullPolicy != "Never" {
		t.Fatalf("parsed: %+v %+v", cfg.Environment, k)
	}
}

func TestConfigDriverDefaultAndRefusal(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, ""))
	if err != nil || cfg.Environment.DriverName() != "docker" {
		t.Fatalf("default driver: %v %v", cfg, err)
	}
	if _, err := LoadConfig(writeConfig(t, "environment: {driver: nomad}\n")); err == nil || !strings.Contains(err.Error(), "environment.driver") {
		t.Fatalf("unknown driver: %v", err)
	}
}
