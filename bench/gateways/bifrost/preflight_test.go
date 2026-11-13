package bifrost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDocker writes an executable that stands in for the docker CLI: it
// records its arguments and prints out.
func fakeDocker(t *testing.T, out string, exit int) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat <<'EOF'\n" + out + "\nEOF\nexit " + string(rune('0'+exit)) + "\n"
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, argsFile
}

func TestPreflight(t *testing.T) {
	allBlocked := "dns blocked\ntcp:1.1.1.1:443 blocked\ntcp:169.254.169.254:80 blocked"
	tests := []struct {
		name         string
		dockerOut    string
		dockerExit   int
		noNetwork    bool
		mutate       func(f *fakeBifrost)
		wantEgress   string
		wantProblems []string
	}{
		{name: "healthy and isolated", dockerOut: allBlocked, wantEgress: "blocked"},
		{name: "dns resolves", dockerOut: strings.Replace(allBlocked, "dns blocked", "dns open", 1), wantEgress: "open",
			wantProblems: []string{"egress from bench-evalnet is open: dns"}},
		{name: "no python in helper", dockerOut: "dns blocked", dockerExit: 1, wantEgress: "open",
			wantProblems: []string{"egress probe incomplete (exit 1), no result for tcp:1.1.1.1:443, tcp:169.254.169.254:80"}},
		{name: "no eval network", noNetwork: true, dockerOut: allBlocked, wantEgress: "open",
			wantProblems: []string{"egress probe not run"}},
		{
			name: "drifted settings", dockerOut: allBlocked, wantEgress: "blocked",
			mutate: func(f *fakeBifrost) {
				cc := f.config["client_config"].(map[string]any)
				cc["mcp_disable_auto_tool_inject"] = false
				cc["disable_content_logging"] = true
				f.provider["store_raw_request_response"] = false
				f.provider["network_config"].(map[string]any)["max_retries"] = 2
				f.keys = f.keys[:1]
			},
			wantProblems: []string{
				"mcp_disable_auto_tool_inject is false, want true",
				"content_logging is false, want true",
				"provider max_retries is 2, want 0",
				"store_raw_request_response is off",
				"key slots missing or disabled: fp-slot-2",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeBifrost(t)
			docker, argsFile := fakeDocker(t, tt.dockerOut, tt.dockerExit)
			cfg := testConfig(f.srv.URL)
			cfg.Docker, cfg.HelperImage, cfg.EvalNetwork = docker, "bench/coding-core-base:1", "bench-evalnet"
			if tt.noNetwork {
				cfg.EvalNetwork = ""
			}
			g, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := g.Setup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if tt.mutate != nil {
				tt.mutate(f)
			}
			m, err := g.Preflight(context.Background())
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			if m.Name != "bifrost" || m.Version != "v2.5.1" || len(m.ConfigSHA256) != 64 || m.CheckedAt.IsZero() {
				t.Errorf("manifest = %+v", m)
			}
			if strings.Join(m.Plugins, ",") != "governance,logging" {
				t.Errorf("plugins = %v", m.Plugins)
			}
			if m.Settings["egress_probe"] != tt.wantEgress || m.Settings["eval_network"] != cfg.EvalNetwork {
				t.Errorf("egress settings = %v", m.Settings)
			}
			if len(m.Problems) != len(tt.wantProblems) {
				t.Fatalf("problems = %q, want %q", m.Problems, tt.wantProblems)
			}
			for i, p := range tt.wantProblems {
				if !strings.Contains(m.Problems[i], p) {
					t.Errorf("problem %d = %q, want %q", i, m.Problems[i], p)
				}
			}
			if tt.wantProblems == nil {
				for _, k := range []string{"mcp_disable_auto_tool_inject", "enforce_auth_on_inference", "enable_logging", "content_logging", "store_raw_request_response"} {
					if m.Settings[k] != "true" {
						t.Errorf("setting %s = %q", k, m.Settings[k])
					}
				}
				if m.Settings["max_retries"] != "0" || m.Settings["allow_direct_keys"] != "false" || m.Settings["base_url"] != testProxy {
					t.Errorf("settings = %v", m.Settings)
				}
				args, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"--network\nbench-evalnet", "--cap-drop\nALL", "bifrost-bench=1", "bench/coding-core-base:1\n-c"} {
					if !strings.Contains(string(args), want) {
						t.Errorf("docker args lack %q:\n%s", want, args)
					}
				}
			}
		})
	}
}

func TestParseEgress(t *testing.T) {
	got := parseEgress("dns blocked\ngarbage line here\ntcp:1.1.1.1:443 open\n\n")
	if len(got) != 2 || got["dns"] != "blocked" || got["tcp:1.1.1.1:443"] != "open" {
		t.Errorf("parseEgress = %v", got)
	}
}
