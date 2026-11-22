package server

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/plugins/heimdall"
	"github.com/maximhq/bifrost/transports/v2/bifrost-http/lib"
)

func TestHeimdallIsBuiltinPlugin(t *testing.T) {
	if !lib.IsBuiltinPlugin(heimdall.PluginName) {
		t.Fatalf("%q should be a built-in plugin", heimdall.PluginName)
	}
}

func TestInstantiateHeimdallPlugin(t *testing.T) {
	cfg := map[string]any{"enabled": true, "allow_inline_plans": true}
	p, err := InstantiatePlugin(context.Background(), heimdall.PluginName, nil, cfg, &lib.Config{})
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	if p == nil || p.GetName() != heimdall.PluginName {
		t.Fatalf("got plugin %v, want %q", p, heimdall.PluginName)
	}
}

func TestInstantiateHeimdallPluginNilConfig(t *testing.T) {
	p, err := InstantiatePlugin(context.Background(), heimdall.PluginName, nil, nil, &lib.Config{})
	if err != nil || p == nil {
		t.Fatalf("nil config should use defaults: plugin=%v err=%v", p, err)
	}
}

func TestResolveHeimdallEnv(t *testing.T) {
	t.Setenv("HEIMDALL_TEST_SECRET", "s3")
	got := resolveHeimdallEnv(map[string]any{
		"usage": map[string]any{"secret": "env.HEIMDALL_TEST_SECRET", "url": "http://heimdall:8090"},
		"list":  []any{"env.HEIMDALL_TEST_SECRET", 1},
	}).(map[string]any)
	if s := got["usage"].(map[string]any)["secret"]; s != "s3" {
		t.Fatalf("secret = %v, want s3", s)
	}
	if u := got["usage"].(map[string]any)["url"]; u != "http://heimdall:8090" {
		t.Fatalf("url changed: %v", u)
	}
	if l := got["list"].([]any)[0]; l != "s3" {
		t.Fatalf("list[0] = %v, want s3", l)
	}
}
