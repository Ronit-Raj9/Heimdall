package gates

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEgress(t *testing.T) {
	rcWith := func(settings map[string]string, network string) *schemas.RunContext {
		return &schemas.RunContext{
			Manifest: &schemas.Manifest{Gateway: &schemas.GatewayManifest{Settings: settings}},
			Handle:   &schemas.EnvironmentHandle{Network: network},
		}
	}
	ok := map[string]string{"egress_probe": "blocked", "eval_network": "bench-eval"}
	cases := []struct {
		name   string
		rc     *schemas.RunContext
		fired  bool
		detail string
	}{
		{"isolated", rcWith(ok, "bench-eval"), false, ""},
		{"egress open", rcWith(map[string]string{"egress_probe": "open", "eval_network": "bench-eval"}, "bench-eval"), true, `egress probe is "open"`},
		{"probe not run says why", rcWith(map[string]string{"egress_probe": "not_run", "eval_network": "bench-eval",
			"egress_probe_error": "egress probe not run: helper image acme/node:1 has no python3; set images.helper to the bench helper image"}, "bench-eval"),
			true, `egress probe is "not_run", not "blocked": egress probe not run: helper image acme/node:1 has no python3`},
		{"egress not recorded", rcWith(map[string]string{"eval_network": "bench-eval"}, "bench-eval"), true, "egress probe not recorded"},
		{"wrong network", rcWith(ok, "bridge"), true, `run network "bridge" is not the eval network "bench-eval"`},
		{"no run network", rcWith(ok, ""), true, "run has no network"},
		{"no eval network", rcWith(map[string]string{"egress_probe": "blocked"}, "bench-eval"), true, "eval network not recorded"},
		{"no manifest", &schemas.RunContext{Handle: &schemas.EnvironmentHandle{Network: "bench-eval"}}, true, "egress probe not recorded"},
		{"nothing at all", nil, true, "run has no network"},
	}
	g := newEgressGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), c.rc, &schemas.Evidence{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired || !strings.Contains(res.Detail, c.detail) {
				t.Fatalf("fired=%v detail=%q, want %v containing %q", res.Fired, res.Detail, c.fired, c.detail)
			}
		})
	}
}

func TestEgressExternalNote(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.cassette.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := &schemas.Task{Dir: dir, External: []schemas.ExternalAPI{{ID: "acme", BaseURL: "https://api.acme.test/v1", Cassette: "acme.cassette.jsonl"}}}
	settings := map[string]string{"egress_probe": "blocked", "eval_network": "bench-eval"}
	rc := func(external string) *schemas.RunContext {
		return &schemas.RunContext{
			Manifest: &schemas.Manifest{Experiment: schemas.Experiment{External: external}, Gateway: &schemas.GatewayManifest{Settings: settings}},
			Handle:   &schemas.EnvironmentHandle{Network: "bench-eval"},
			Task:     task,
		}
	}
	cases := []struct {
		name, external, want string
	}{
		{"replay from cassette", "", "external APIs: acme -> api.acme.test:443 (replay, no egress)"},
		{"live opt in", "live", "acme -> api.acme.test:443 (live, tool proxy only"},
		{"record opt in", "record", "(record, tool proxy only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := newEgressGate().Check(context.Background(), rc(c.external), &schemas.Evidence{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired || !strings.Contains(res.Detail, c.want) {
				t.Fatalf("fired=%v detail=%q, want not fired containing %q", res.Fired, res.Detail, c.want)
			}
		})
	}
	noTask := &schemas.RunContext{Manifest: &schemas.Manifest{Gateway: &schemas.GatewayManifest{Settings: settings}}, Handle: &schemas.EnvironmentHandle{Network: "bench-eval"}}
	if res, _ := newEgressGate().Check(context.Background(), noTask, &schemas.Evidence{}); res.Detail != "" {
		t.Fatalf("run without external APIs has note %q", res.Detail)
	}
}
