package bifrost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// fakeRun is what one fake docker call prints and exits with.
type fakeRun struct {
	out, errOut string
	exit        int
}

// fakeDocker writes an executable that stands in for the docker CLI. A call
// with --network none is the helper self-test and gets self; any other call
// is the egress probe, gets probe and has its arguments recorded in the
// returned file.
func fakeDocker(t *testing.T, self, probe fakeRun) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	write := func(name, s string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
  *" --network none "*) cat %q; cat %q >&2; exit %d ;;
esac
printf '%%s\n' "$@" > %q
cat %q; cat %q >&2; exit %d
`, write("self.out", self.out), write("self.err", self.errOut), self.exit, argsFile,
		write("probe.out", probe.out), write("probe.err", probe.errOut), probe.exit)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, argsFile
}

func TestPreflight(t *testing.T) {
	allBlocked := "dns blocked\ntcp:1.1.1.1:443 blocked\ntcp:169.254.169.254:80 blocked"
	lacks := func(tool string) string {
		return "egress probe not run: helper image bench/coding-core-base:1 has no " + tool + "; set images.helper to the bench helper image"
	}
	tests := []struct {
		name         string
		self         fakeRun // the helper self-test
		dockerOut    string
		dockerErr    string
		dockerExit   int
		noNetwork    bool
		mutate       func(f *fakeBifrost)
		wantEgress   string
		wantProblems []string
	}{
		{name: "healthy and isolated", dockerOut: allBlocked, wantEgress: "blocked"},
		{name: "dns resolves", dockerOut: strings.Replace(allBlocked, "dns blocked", "dns open", 1), wantEgress: "open",
			wantProblems: []string{"egress from bench-evalnet is open: dns"}},
		{name: "no python in helper", self: fakeRun{out: "missing python3"}, dockerOut: allBlocked, wantEgress: "not_run",
			wantProblems: []string{lacks("python3")}},
		// Without the self-test a missing getent printed "dns blocked", the
		// same as a truly blocked network.
		{name: "no getent in helper", self: fakeRun{out: "missing getent"}, dockerOut: allBlocked, wantEgress: "not_run",
			wantProblems: []string{lacks("getent")}},
		{name: "getent not found while probing", dockerOut: allBlocked, dockerErr: "sh: 3: getent: not found", wantEgress: "not_run",
			wantProblems: []string{lacks("getent")}},
		{name: "python3 command not found while probing", dockerOut: "dns blocked", dockerErr: "/bin/sh: line 6: python3: command not found",
			dockerExit: 127, wantEgress: "not_run", wantProblems: []string{lacks("python3")}},
		{name: "no sh in helper", self: fakeRun{errOut: `docker: Error response from daemon: failed to create task for container: exec: "sh": executable file not found in $PATH: unknown.`, exit: 127},
			dockerOut: allBlocked, wantEgress: "not_run", wantProblems: []string{lacks("sh")}},
		{name: "self-test fails without naming a tool", self: fakeRun{errOut: "No such image: bench/coding-core-base:1", exit: 125},
			dockerOut: allBlocked, wantEgress: "not_run", wantProblems: []string{"egress probe not run: helper self-test in bench/coding-core-base:1 exited 125: No such image"}},
		{name: "dns lookup error is no result", dockerOut: strings.Replace(allBlocked, "dns blocked", "dns error 1", 1), wantEgress: "not_run",
			wantProblems: []string{"egress probe incomplete (exit 0), no result for dns"}},
		{name: "probe cut short", dockerOut: "dns blocked", dockerExit: 1, wantEgress: "not_run",
			wantProblems: []string{"egress probe incomplete (exit 1), no result for tcp:1.1.1.1:443, tcp:169.254.169.254:80"}},
		{name: "open wins over incomplete", dockerOut: "dns open", dockerExit: 1, wantEgress: "open",
			wantProblems: []string{"egress from bench-evalnet is open: dns"}},
		{name: "no eval network", noNetwork: true, dockerOut: allBlocked, wantEgress: "not_run",
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
			docker, argsFile := fakeDocker(t, tt.self, fakeRun{out: tt.dockerOut, errOut: tt.dockerErr, exit: tt.dockerExit})
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
			// The reason is recorded exactly when the probe did not run, so the
			// egress gate can say why instead of only "not blocked".
			if reason := m.Settings["egress_probe_error"]; (tt.wantEgress == "not_run") != (reason != "") ||
				(reason != "" && !strings.Contains(reason, tt.wantProblems[0])) {
				t.Errorf("egress_probe_error = %q", reason)
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

func TestMissingTools(t *testing.T) {
	tests := []struct {
		name        string
		out, errOut string
		want        []string
	}{
		{"self-test lines, in list order", "missing getent\nmissing python3\n", "", []string{"python3", "getent"}},
		{"dash not found", "", "sh: 1: getent: not found", []string{"getent"}},
		{"bash command not found", "", "/bin/sh: line 2: python3: command not found", []string{"python3"}},
		{"entrypoint absent", "", `exec: "sh": executable file not found in $PATH`, []string{"sh"}},
		{"unknown tools and noise ignored", "missing curl\ndns blocked\n", "manifest for x not found", nil},
		{"shell prefix is not a missing sh", "", "/bin/sh: getent: not found", []string{"getent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingTools(&utils.DockerResult{Stdout: []byte(tt.out), Stderr: []byte(tt.errOut)})
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("missingTools = %v, want %v", got, tt.want)
			}
		})
	}
	if !strings.Contains(HelperSelfTestScript, "getent") || !strings.Contains(egressScript, HelperSelfTestScript) {
		t.Fatal("the egress probe must repeat the canonical self-test, getent included")
	}
}

func TestParseEgress(t *testing.T) {
	got := parseEgress("dns blocked\ngarbage line here\ntcp:1.1.1.1:443 open\n\n")
	if len(got) != 2 || got["dns"] != "blocked" || got["tcp:1.1.1.1:443"] != "open" {
		t.Errorf("parseEgress = %v", got)
	}
}
