package vendingbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tomlFixture = `artifacts = [{ source = "/tmp/finalized.json", service = "sim-server" }]

[task]
name = "prosus/prosus-vending-bench"

[verifier]
timeout_sec = 900.0
environment_mode = "separate"

[[verifier.collect]]
service = "sim-server"
command = "curl"

[agent]
timeout_sec = 43200.0

[[environment.mcp_servers]]
name = "vending"
url = "http://sim-server:8000/mcp"

[verifier.env]
`

const composeFixture = `services:
  main:
    depends_on:
      sim-server:
        condition: service_healthy

  sim-server:
    build:
      context: ./sim-server
    environment:
      VENDING_CONFIG: "${VENDING_CONFIG:-}"
      VENDING_SEED: "${VENDING_SEED:-}"
`

func TestRewrites(t *testing.T) {
	dir := t.TempDir()
	tp, cp := filepath.Join(dir, "task.toml"), filepath.Join(dir, "c.yaml")
	os.WriteFile(tp, []byte(tomlFixture), 0o644)
	os.WriteFile(cp, []byte(composeFixture), 0o644)
	v := variant{id: "vending-365d-seed2", days: 365, config: "/config/full-year.toml", seed: 2}
	if err := rewriteTOML(tp, v); err != nil {
		t.Fatal(err)
	}
	if err := rewriteCompose(cp, v); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tp)
	for _, bad := range []string{"artifacts", "environment_mode", "verifier.collect", "mcp_servers"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("task.toml still has %s:\n%s", bad, b)
		}
	}
	if !strings.Contains(string(b), `name = "prosus/vending-365d-seed2"`) {
		t.Errorf("name not rewritten:\n%s", b)
	}
	c, _ := os.ReadFile(cp)
	for _, want := range []string{"build: .", "role: mcp", `VENDING_SEED: "2"`, `VENDING_CONFIG: "/config/full-year.toml"`} {
		if !strings.Contains(string(c), want) {
			t.Errorf("compose lacks %q:\n%s", want, c)
		}
	}
	if len(variants([]int{1, 2})) != 4 {
		t.Error("variants")
	}
}
