package lib

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLoadShippedConfig(t *testing.T) {
	cfg, err := LoadConfig("../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"runs_root": cfg.RunsRoot == "runs", "benchmarks_root": cfg.BenchmarksRoot == "benchmarks",
		"listen": cfg.Server.Listen == "127.0.0.1:8090", "mode": cfg.Server.Mode == schemas.ModeLive,
		"gateway.url": cfg.Gateway.URL == "http://127.0.0.1:18080", "agent_url": cfg.Gateway.AgentURL == "http://bifrost:8080",
		"admin_token_env": cfg.Gateway.AdminTokenEnv == "BIFROST_SETUP_TOKEN", "provider": cfg.Gateway.Provider == "anthropic",
		"key_slots":   reflect.DeepEqual(cfg.Gateway.KeySlots, []string{"fp-slot-1", "fp-slot-2", "fp-slot-3", "fp-slot-4"}),
		"control_url": cfg.FaultProxy.ControlURL == "http://127.0.0.1:19101", "eval": cfg.Networks.Eval == "bench-evalnet",
		"helper": cfg.Images.Helper == "bench/coding-core-base:1", "pricing": cfg.Pricing == "deploy/pricing-snapshot.json",
	}
	for k, ok := range want {
		if ok != true {
			t.Errorf("%s wrong: %+v", k, cfg)
		}
	}
	if !cfg.ListenIsLoopback() {
		t.Error("shipped listen must be loopback")
	}
	if got := cfg.Path("runs"); got != filepath.Join(cfg.Root, "runs") || filepath.Base(cfg.Root) != "bench" {
		t.Errorf("Path(runs) = %s, root %s", got, cfg.Root)
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("BENCH_SERVER_MODE", "recorded")
	t.Setenv("BENCH_SERVER_LISTEN", "127.0.0.1:9999")
	t.Setenv("BENCH_GATEWAY_KEY_SLOTS", "a, b ,,c")
	t.Setenv("BENCH_FAULTPROXY_CONTROL_URL", "http://x")
	t.Setenv("BENCH_RUNS_ROOT", "/tmp/runs")
	cfg, err := LoadConfig("../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Mode != schemas.ModeRecorded || cfg.Server.Listen != "127.0.0.1:9999" ||
		!reflect.DeepEqual(cfg.Gateway.KeySlots, []string{"a", "b", "c"}) || cfg.FaultProxy.ControlURL != "http://x" ||
		cfg.Path(cfg.RunsRoot) != "/tmp/runs" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name string
		path string
		env  map[string]string
	}{
		{"missing file", filepath.Join(dir, "nope.yaml"), nil},
		{"bad yaml", write("bad.yaml", "server: [\n"), nil},
		{"bad mode", write("mode.yaml", "runs_root: runs\nserver: {listen: '127.0.0.1:1', mode: demo}\n"), nil},
		{"bad listen", write("listen.yaml", "runs_root: runs\nserver: {listen: nope, mode: live}\n"), nil},
		{"no runs root", write("root.yaml", "server: {listen: '127.0.0.1:1', mode: live}\n"), nil},
		{"bad env value", write("ok.yaml", "runs_root: runs\nserver: {listen: '127.0.0.1:1', mode: live}\n"), map[string]string{"BENCH_SERVER_MODE": "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := LoadConfig(tt.path); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "[::1]": true,
		"0.0.0.0": false, "example.com": false, "192.168.1.2": false} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
