//go:build integration

package environments

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// eventsFileAgent appends one native event to the events file as uid 1000,
// the way an agent without the collector reports a step.
const eventsFileAgent = `set -e
test "$(id -u)" = 1000
test "$BENCH_EVENTS_FILE" = /opt/bench/events/events.jsonl
printf '%s\n' '{"type":"plan","name":"fix add","output":"change minus to plus"}' >> "$BENCH_EVENTS_FILE"
echo '{"type":"result","result":"done"}'
`

// TestEventsFileChannelDocker runs one agent session the way the run kernel
// does with the agent events feature on and checks the events file channel
// end to end: the aux volume gets its events folder, the agent (uid 1000)
// appends a native event to $BENCH_EVENTS_FILE, and the agent_events evidence
// source reads it back as one file-channel event. "collector missing" points
// the collector at an image that does not exist, so the file channel stands
// alone, as docs/agent-events.md promises; "collector on" builds the tool
// proxy image from the bench image (BENCH_IMAGE, default
// bench/bifrost-bench:dev) and a fresh binary, so the collector serves beside
// the file channel, as in a default run. Run with:
//
//	GOWORK=off go test -tags integration -run EventsFileChannelDocker ./environments/
func TestEventsFileChannelDocker(t *testing.T) {
	t.Run("collector missing", func(t *testing.T) {
		eventsFileRun(t, "hd-f2-no-such-toolproxy:none", "missing")
	})
	t.Run("collector on", func(t *testing.T) {
		eventsFileRun(t, eventsToolProxyImage(t), "on")
	})
}

// eventsToolProxyImage builds a tool proxy image with the current binary on
// the bench image, removed when the test ends; it skips when the bench image
// is missing.
func eventsToolProxyImage(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	d := utils.Docker{}
	base := os.Getenv("BENCH_IMAGE")
	if base == "" {
		base = "bench/bifrost-bench:dev"
	}
	if _, err := d.MustRun(ctx, "image", "inspect", base); err != nil {
		t.Skipf("image %s not available: %v", base, err)
	}
	image := strings.ToLower(utils.NewID("hd-f2-tp", 8)) + ":test"
	build := t.TempDir()
	cmd := exec.Command("go", "build", "-o", filepath.Join(build, "bifrost-bench"), "../cmd/bifrost-bench")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(build, "Dockerfile"), []byte("FROM "+base+"\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := d.MustRun(ctx, "build", "-q", "-t", image, build); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Run(context.Background(), "image", "rm", image) })
	return image
}

// eventsFileRun creates a run, runs eventsFileAgent in it and checks the
// collector state and the one file-channel event the evidence source reads.
func eventsFileRun(t *testing.T, toolProxyImage, wantCollector string) {
	ctx := context.Background()
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{HelperImage: testImage, ToolProxyImage: toolProxyImage, EventsCollector: true})
	runID := strings.Replace(utils.NewID("r", 8), "r_", "hd-f2-", 1)
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: "e_hdf2events"},
		Slot:  &schemas.Slot{Condition: schemas.ConditionClean},
		Suite: &schemas.Suite{Version: "1.0.0", Workdir: "/workspace", BaseImage: schemas.ImageSpec{Tag: testImage}},
		Task:  &schemas.Task{ID: "tcalc", Dir: taskDir, RepoDir: "repo", Notes: schemas.TaskNotes{Clean: "fixtures/notes.clean.md"}},
		Agent: &schemas.AgentConfig{ID: "events-file"},
		Dir:   t.TempDir(),
	}
	t.Cleanup(func() {
		if err := e.Destroy(context.Background(), rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		assertNoLeftovers(t, runID)
	})
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	res, err := e.Exec(ctx, rc, shPlan(eventsFileAgent, 60))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("agent exit %d\nstdout: %s\nstderr: %s", res.ExitCode, readFile(t, res.StdoutPath), readFile(t, res.StderrPath))
	}
	rc.Exec = res
	state, _ := os.ReadFile(filepath.Join(rc.Dir, CollectorStateFile))
	if !strings.Contains(string(state), `"`+wantCollector+`"`) {
		t.Fatalf("collector state %s, want %s", state, wantCollector)
	}
	var src schemas.EvidenceSource
	for _, s := range evidence.Builtin(evidence.Deps{Docker: e.cfg.Docker, HelperImage: testImage}) {
		if s.GetName() == evidence.AgentEvents {
			src = s
		}
	}
	ev := &schemas.Evidence{}
	if err := src.Collect(ctx, rc, ev); err != nil {
		t.Fatalf("collect: %v", err)
	}
	st := ev.AgentEvents
	if st == nil || st.Kept != 1 || st.Dropped != 0 || st.Channels[schemas.ReportChannelFile] != 1 || st.Collector != wantCollector {
		t.Fatalf("agent events status %+v", st)
	}
	var got *schemas.Event
	for i := range ev.Events {
		if ev.Events[i].Source == schemas.SourceAgentReported {
			got = &ev.Events[i]
		}
	}
	if got == nil || got.Channel != schemas.ReportChannelFile || got.ReportedType != "plan" {
		t.Fatalf("agent-reported event %+v", got)
	}
	t.Logf("run %s: event %s on channel %s, collector %s", runID, got.EventID, got.Channel, strings.TrimSpace(string(state)))
}
