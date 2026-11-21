package lib

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerDiskDefaults(t *testing.T) {
	var d RunnerDiskConfig
	if d.MinFreeBytes() != DefaultMinFreeGB<<30 || d.ImageMaxUnused() != DefaultImageMaxUnusedH*time.Hour ||
		d.BuildCacheKeepBytes() != DefaultBuildCacheKeepGB<<30 || d.JanitorEvery() != DefaultJanitorEveryMin*time.Minute {
		t.Fatalf("defaults: %d %s %d %s", d.MinFreeBytes(), d.ImageMaxUnused(), d.BuildCacheKeepBytes(), d.JanitorEvery())
	}
	off := RunnerDiskConfig{MinFreeGB: -1, ImageMaxUnusedH: -1, BuildCacheKeepGB: -1}
	if off.MinFreeBytes() != 0 || off.ImageMaxUnused() != 0 || off.BuildCacheKeepBytes() != -1 {
		t.Fatalf("negative values must turn steps off: %d %s %d", off.MinFreeBytes(), off.ImageMaxUnused(), off.BuildCacheKeepBytes())
	}
}

func TestSlotsPerProviderFromEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "deploy", "bench.yaml")
	if err := os.WriteFile(p, []byte("runs_root: runs\nserver:\n  listen: 127.0.0.1:8090\n  mode: live\ngateway:\n  key_slots: [fp-slot-1, fp-slot-2]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_GATEWAY_SLOTS_PER_PROVIDER", "32")
	t.Setenv("BENCH_RUNNER_DISK_MIN_FREE_GB", "25")
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Gateway.KeySlots) != 32 || cfg.Gateway.KeySlots[31] != "fp-slot-32" || len(cfg.Gateway.OpenAIKeySlots) != 32 || cfg.Gateway.OpenAIKeySlots[0] != "oa-slot-1" {
		t.Fatalf("slots = %v / %v", cfg.Gateway.KeySlots, cfg.Gateway.OpenAIKeySlots)
	}
	if cfg.Runner.Disk.MinFreeBytes() != 25<<30 {
		t.Fatalf("min free = %d", cfg.Runner.Disk.MinFreeBytes())
	}
	t.Setenv("BENCH_GATEWAY_SLOTS_PER_PROVIDER", "999")
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("999 slots per provider accepted")
	}
	os.Unsetenv("BENCH_GATEWAY_SLOTS_PER_PROVIDER")
	t.Setenv("BENCH_SERVER_METRICS_LISTEN", "nope")
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("bad metrics_listen accepted")
	}
}
