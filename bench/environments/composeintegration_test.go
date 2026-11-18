//go:build integration

package environments

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/compose"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestComposeStackEndToEnd runs one acme-stack task (examples/acme-stack,
// cache-invalidation) with real Docker: three containers from its compose
// file (the app as the agent's workspace image, Postgres and Redis beside
// it), the reference solution in the agent's place, the clean-room verifier
// on the run's internal network, the services' logs as evidence, and a
// cleanup that leaves nothing behind. The noop control on the same stack
// must fail. It needs postgres:18, redis:7-alpine and python:3.12-slim
// locally and builds the task image (pip, with network) when it is missing.
func TestComposeStackEndToEnd(t *testing.T) {
	for _, img := range []string{"postgres:18", "redis:7-alpine", "python:3.12-slim"} {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err != nil {
			t.Skipf("image %s is not present locally", img)
		}
	}
	_, task, err := suite.LoadTask(filepath.Join("..", "examples", "acme-stack", "benchmarks"), "acme-stack", "cache-invalidation")
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := suite.LoadTask(filepath.Join("..", "examples", "acme-stack", "benchmarks"), "acme-stack", "cache-invalidation")
	if err != nil {
		t.Fatal(err)
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{HelperImage: testImage})
	ctx := context.Background()
	image, _, err := e.EnsureTaskImage(ctx, s, task)
	if err != nil {
		t.Fatalf("task image: %v", err)
	}
	var grader schemas.Grader
	for _, g := range graders.Builtin(graders.HiddenTestsConfig{}) {
		if g.GetName() == s.Grader {
			grader = g
		}
	}
	reference, err := os.ReadFile(filepath.Join(task.Dir, task.Controls.Reference))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, script string
		pass         bool
	}{{"reference", string(reference), true}, {"noop", "true", false}} {
		t.Run(c.name, func(t *testing.T) {
			runID := strings.Replace(utils.NewID("r", 10), "r_", "hd-x2-", 1)
			rc := &schemas.RunContext{
				Run:   &schemas.Run{RunID: runID, ExpID: "e_hdx2it"},
				Suite: s, Task: task, Dir: t.TempDir(),
				Agent: &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "AGENTS.md"}},
			}
			t.Cleanup(func() {
				if err := e.Destroy(ctx, rc); err != nil {
					t.Errorf("destroy: %v", err)
				}
				assertNoLeftovers(t, runID)
				out, _ := exec.Command("docker", "network", "ls", "-q", "--filter", "label="+utils.LabelRun+"="+runID).Output()
				if strings.TrimSpace(string(out)) != "" {
					t.Errorf("leftover networks: %s", out)
				}
			})
			h, err := e.Create(ctx, rc)
			if err != nil {
				t.Fatal(err)
			}
			rc.Handle = h
			handles, results, err := e.StartServices(ctx, rc)
			if err != nil {
				t.Fatalf("start services: %v (%+v)", err, results)
			}
			rc.Services = handles
			if len(handles) != 2 || handles[0].Spec.ID != "db" || handles[1].Spec.ID != "cache" || handles[0].Network == "" {
				t.Fatalf("handles %+v", handles)
			}
			// The cache answers to its compose name on the run's network, and
			// that network is internal.
			internal, err := exec.Command("docker", "network", "inspect", "--format", "{{.Internal}}", handles[0].Network).Output()
			if err != nil || strings.TrimSpace(string(internal)) != "true" {
				t.Fatalf("run network %s internal = %q, %v", handles[0].Network, internal, err)
			}
			plan := &schemas.RunPlan{Image: image, Argv: []string{"sh", "/tmp/bench/control.sh"}, Files: map[string]string{"/tmp/bench/control.sh": c.script},
				Workdir: "/app", WallS: 300, CPUs: 1, MemGB: 1, Network: "none", User: "1000"}
			res, err := e.Exec(ctx, rc, plan)
			if err != nil || res.ExitCode != 0 {
				t.Fatalf("exec: %v %+v", err, res)
			}
			diff, err := e.ExportDiff(ctx, rc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.ExportState(ctx, rc); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"db", "cache"} {
				b, err := os.ReadFile(filepath.Join(rc.Dir, ServiceLogsFolder, id+".log"))
				if err != nil || len(b) == 0 {
					t.Errorf("service log %s: %d bytes, %v", id, len(b), err)
				}
			}
			grade, err := grader.Grade(ctx, rc, diff)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: passed %v, reason %s", c.name, grade.Passed, grade.Reason)
			if grade.Passed != c.pass {
				t.Fatalf("%s graded passed=%v (%s), want %v", c.name, grade.Passed, grade.Reason, c.pass)
			}
		})
	}
}

// TestComposeSnapshotAcrossServices maps a compose file whose cache service
// declares a state export, runs an agent that writes a key into the cache
// over the run network and a file outside the workspace, and grades in
// snapshot mode: the verifier, in the frozen agent container, reads the
// exported state at /grader/state/cache/k and queries the live cache by
// name. It needs redis:7-alpine locally.
func TestComposeSnapshotAcrossServices(t *testing.T) {
	if err := exec.Command("docker", "image", "inspect", "redis:7-alpine").Run(); err != nil {
		t.Skip("image redis:7-alpine is not present locally")
	}
	ctx := context.Background()
	check := `import socket, sys
def cmd(*a):
    s = socket.create_connection(("cache", 6379), timeout=5)
    s.sendall(("*%d\r\n" % len(a) + "".join("$%d\r\n%s\r\n" % (len(x), x) for x in a)).encode())
    return s.recv(256).decode()
assert open("/tmp/outside.txt").read().strip() == "out", "file outside the workspace missing"
assert open("/grader/state/cache/k").read().strip() == "v", "exported state wrong"
assert "v" in cmd("GET", "k"), "live cache does not hold the key"
print("ok")
`
	e, rc := scenarioRun(t, DockerWorkspaceConfig{}, map[string]string{
		"repo/README.md":          "task\n",
		"verifier/tests/check.py": check,
		"compose.yaml": `services:
  app:
    image: bench/coding-core-base:1
    working_dir: /workspace
    x-heimdall: {workspace: true}
  cache:
    image: redis:7-alpine
    command: ["redis-server", "--save", "", "--appendonly", "no"]
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 1s
      retries: 30
    expose: ["6379"]
    x-heimdall:
      export:
        - {id: k, argv: [redis-cli, GET, k]}
x-heimdall:
  verifier_services: true
`,
	})
	res, err := compose.Expand(rc.Task.Dir, "compose.yaml", nil, compose.Options{})
	if err != nil || len(res.Refused()) > 0 {
		t.Fatalf("expand: %v %v", err, res.Refused())
	}
	rc.Task.Services = res.Services
	rc.Suite.Verifier = schemas.SuiteVerifier{Command: []string{"python3", "/grader/tests/check.py"}, Result: schemas.VerifierResultExitCode, TimeoutS: 60, Mode: schemas.VerifierSnapshot}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	handles, _, err := e.StartServices(ctx, rc)
	if err != nil {
		t.Fatalf("start services: %v", err)
	}
	rc.Services = handles
	agent := `import socket
s = socket.create_connection(("cache", 6379), timeout=5)
s.sendall(b"*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n")
assert s.recv(64).startswith(b"+OK")
open("/tmp/outside.txt", "w").write("out\n")
`
	plan := shPlan("python3 -c "+shellQuoteTest(agent), 60)
	if r, err := e.Exec(ctx, rc, plan); err != nil || r.ExitCode != 0 {
		t.Fatalf("exec: %+v %v %s", r, err, readFile(t, r.StderrPath))
	}
	if _, err := e.Snapshot(ctx, rc); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	files, err := e.ExportState(ctx, rc)
	if err != nil || len(files) != 1 || files[0].Error != "" {
		t.Fatalf("state %+v %v", files, err)
	}
	if b := readFile(t, filepath.Join(rc.Dir, ServiceLogsFolder, "cache.log")); !strings.Contains(string(b), "Ready to accept connections") {
		t.Fatalf("cache log %q", b)
	}
	g := gradeWith(t, rc, d)
	if !g.Passed || g.Mode != schemas.VerifierSnapshot {
		t.Fatalf("grade %+v", g)
	}
}

func shellQuoteTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
