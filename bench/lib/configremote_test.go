package lib

import "testing"

// TestRemoteCoverageConfig checks the remote coverage keys: empty in the
// shipped config (loopback default, no public addresses) and settable from
// the environment like every other key.
func TestRemoteCoverageConfig(t *testing.T) {
	cfg, err := LoadConfig("../deploy/bench.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Gateway.PublicURL != "" || cfg.RemoteTools.Listen != "" || cfg.RemoteTools.PublicURL != "" {
		t.Fatalf("shipped config publishes something: %+v %+v", cfg.Gateway, cfg.RemoteTools)
	}
	t.Setenv("BENCH_GATEWAY_PUBLIC_URL", "https://gw.example.com")
	t.Setenv("BENCH_REMOTE_TOOLS_LISTEN", "127.0.0.1:9400")
	t.Setenv("BENCH_REMOTE_TOOLS_PUBLIC_URL", "http://127.0.0.1:19400")
	if cfg, err = LoadConfig("../deploy/bench.yaml"); err != nil {
		t.Fatal(err)
	}
	if cfg.Gateway.PublicURL != "https://gw.example.com" || cfg.RemoteTools.Listen != "127.0.0.1:9400" || cfg.RemoteTools.PublicURL != "http://127.0.0.1:19400" {
		t.Fatalf("overrides not applied: %+v %+v", cfg.Gateway, cfg.RemoteTools)
	}
}
