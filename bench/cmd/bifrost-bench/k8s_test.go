package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
)

func TestK8sUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"gateway"}, {"gateway", "sideways"}, {"images"}, {"images", "push"}} {
		if code := runK8s(args); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestToolProxyImageDefault(t *testing.T) {
	if got := toolProxyImage(&lib.Config{}); got != "bench/bifrost-bench:dev" {
		t.Fatalf("default %q", got)
	}
	cfg := &lib.Config{}
	cfg.Images.ToolProxy = "registry.example.com/bench@sha256:abc"
	if got := toolProxyImage(cfg); got != cfg.Images.ToolProxy {
		t.Fatalf("configured %q", got)
	}
}
