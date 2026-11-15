//go:build integration

package environments

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestEventCollectorDocker starts the collector the way Exec does for a run
// without services (ensureEventCollector: service network, probe, the tool
// proxy image running bifrost-bench eventcollector, ready check), posts one
// event from a container on the run's service network, copies the ledger
// into the run folder (copyAgentEvents) and removes everything with Destroy.
// Run with:
//
//	GOWORK=off go test -tags integration -run EventCollectorDocker ./environments/
//
// It needs Docker, the bench image (BENCH_IMAGE, default
// bench/bifrost-bench:dev, the base the fresh binary is copied onto) and a
// Python image (BENCH_PYTHON_IMAGE, default python:3.12-slim) as the helper.
func TestEventCollectorDocker(t *testing.T) {
	ctx := context.Background()
	d := utils.Docker{}
	get := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	benchImage, pyImage := get("BENCH_IMAGE", "bench/bifrost-bench:dev"), get("BENCH_PYTHON_IMAGE", "python:3.12-slim")
	for _, img := range []string{benchImage, pyImage} {
		if _, err := d.MustRun(ctx, "image", "inspect", img); err != nil {
			t.Skipf("image %s not available: %v", img, err)
		}
	}
	id := strings.ToLower(utils.NewID("hd-ev", 8))
	image := id + ":test"
	build := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(build, "bifrost-bench"), "../cmd/bifrost-bench")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte("FROM "+benchImage+"\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MustRun(ctx, "build", "-q", "-t", image, build); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Run(context.Background(), "image", "rm", image) })

	runID := id
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: runID, ExpID: id + "-exp"}, Dir: t.TempDir(), Agent: &schemas.AgentConfig{ID: "tiny"}}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: d, HelperImage: pyImage, ToolProxyImage: image, EventsCollector: true})
	t.Cleanup(func() { _ = e.Destroy(context.Background(), rc) })
	e.ensureEventCollector(ctx, rc, &schemas.RunPlan{}, rc.Run.ExpID, runID)
	if !collectorRunning(runID) {
		state, _ := os.ReadFile(filepath.Join(rc.Dir, CollectorStateFile))
		t.Fatalf("collector not running: %s", state)
	}
	env := strings.Join(e.eventEnv(rc, runID, nil), "\n")
	if !strings.Contains(env, "BENCH_EVENTS_URL=http://toolproxy:9200/events") || !strings.Contains(env, "OTEL_SERVICE_NAME=tiny") {
		t.Fatalf("env:\n%s", env)
	}
	post := `import urllib.request
r = urllib.request.Request("http://toolproxy:9200/events", data=b'{"type":"plan","output":"x"}', method="POST", headers={"Content-Type": "application/x-ndjson"})
print(urllib.request.urlopen(r, timeout=10).status)`
	res, err := d.Run(ctx, "run", "--rm", "--network", serviceNetwork(runID), "--user", "1000:1000", pyImage, "python", "-c", post)
	if err != nil || res.ExitCode != 0 || !strings.Contains(string(res.Stdout), "202") {
		t.Fatalf("post: %v %s %s", err, res.Stdout, res.Stderr)
	}
	e.copyAgentEvents(ctx, runID, rc.Dir)
	raw, err := os.ReadFile(filepath.Join(rc.Dir, CollectorLedgerFile))
	if err != nil {
		t.Fatal(err)
	}
	if recs, bad := agentevents.ReadRecords(raw); len(recs) != 1 || bad != 0 || recs[0].Type != "plan" {
		t.Fatalf("ledger %q", raw)
	}
	if state, _ := os.ReadFile(filepath.Join(rc.Dir, CollectorStateFile)); !strings.Contains(string(state), `"on"`) {
		t.Fatalf("state %s", state)
	}
	if err := e.Destroy(ctx, rc); err != nil {
		t.Fatal(err)
	}
	out, _ := d.MustRun(ctx, "ps", "-a", "-q", "--filter", "label="+utils.LabelRun+"="+runID)
	if strings.TrimSpace(string(out.Stdout)) != "" {
		t.Fatalf("containers left: %s", out.Stdout)
	}
}
