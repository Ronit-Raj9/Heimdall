package environments

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestCheckWorkspacePath(t *testing.T) {
	for p, ok := range map[string]bool{"": true, "/app": true, "/testbed": true, "/workspace": true, "app": false, "/": false, "/a/../b": false, "/tmp/x": false, "/home/agent": false, "/memory": false} {
		if err := checkWorkspacePath(p); (err == nil) != ok {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestParseSHA256Sum(t *testing.T) {
	sum := strings.Repeat("a", 64)
	raw := sum + "  ./hello.txt\n" + sum + "  ./sub/x y.py\n\\" + sum + "  ./we\\nird\nbad line\n"
	if got := parseSHA256Sum([]byte(raw)); !reflect.DeepEqual(got, map[string]string{"hello.txt": sum, "sub/x y.py": sum}) {
		t.Fatalf("got %v", got)
	}
}

func TestEnsureTaskImage(t *testing.T) {
	noDocker := utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}
	tag, id, err := EnsureTaskImage(t.Context(), noDocker, &schemas.Suite{ID: "s"}, &schemas.Task{ID: "t"}, nil)
	if tag != "" || id != "" || err != nil {
		t.Fatalf("task without image: %q %q %v", tag, id, err)
	}
	docker, logPath := fakeDocker(t)
	tag, id, err = EnsureTaskImage(t.Context(), docker, &schemas.Suite{ID: "s"}, &schemas.Task{ID: "t", Environment: &schemas.TaskEnvironment{Image: "tb/fix-git:1"}}, nil)
	if tag != "tb/fix-git:1" || !strings.HasPrefix(id, "sha256:1111") || err != nil {
		t.Fatalf("prebuilt: %q %q %v", tag, id, err)
	}
	if callWith(calls(t, logPath), "pull") != "" {
		t.Fatal("present image pulled")
	}
}

// TestSeedPreparesEventsFolder checks that the seeding helper makes the
// agent events folder when the events feature is on, after the aux volume is
// handed to uid 1000 (a root helper without DAC_OVERRIDE cannot mkdir there
// afterwards), and leaves it out when the feature is off.
func TestSeedPreparesEventsFolder(t *testing.T) {
	chown := strings.Index(populateScript, "chown -R 1000:1000 /workspace /opt/bench")
	mk := strings.Index(populateScript, "mkdir -p /opt/bench/events")
	if chown < 0 || mk < chown || !strings.Contains(populateScript[mk:], "chmod 1777 /opt/bench/events") {
		t.Fatalf("populate script does not make the events folder after the aux chown:\n%s", populateScript)
	}
	for _, on := range []bool{true, false} {
		docker, logPath := fakeDocker(t)
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EventsCollector: on})
		rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Suite: &schemas.Suite{ID: "s"},
			Task: &schemas.Task{ID: "t", Dir: t.TempDir(), Environment: &schemas.TaskEnvironment{Image: "tb/x:1", WorkspacePath: "/app"}}}
		h := &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"}
		_, _ = e.seed(t.Context(), rc, h, "", "r_1", seedOptions{})
		got := callWith(calls(t, logPath), "run -i --rm --name bench-r_1-prep", "BENCH_EVENTS=1") != ""
		if got != on {
			t.Fatalf("events feature %v: BENCH_EVENTS=1 passed %v", on, got)
		}
	}
}

// TestSeedFromImageWithFakeDocker checks that a task image's workspace path
// is streamed from a created (never started) container into the helper.
func TestSeedFromImageWithFakeDocker(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Suite: &schemas.Suite{ID: "s"},
		Task: &schemas.Task{ID: "t", Dir: t.TempDir(), Environment: &schemas.TaskEnvironment{Image: "tb/x:1", WorkspacePath: "/app"}}}
	h := &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"}
	if _, err := e.seed(t.Context(), rc, h, "", "r_1", seedOptions{}); err == nil || !strings.Contains(err.Error(), "no baseline commit id") {
		t.Fatalf("seed: %v", err)
	}
	all := calls(t, logPath)
	if callWith(all, "create --name bench-r_1-src", "--entrypoint /bench-none tb/x:1") == "" ||
		callWith(all, "cp bench-r_1-src", "/app/. -") == "" ||
		callWith(all, "run -i --rm --name bench-r_1-prep", "BENCH_SRC_TAR=1", "helper:1") == "" {
		t.Fatalf("calls:\n%s", strings.Join(all, "\n"))
	}
	for _, c := range all {
		if strings.HasPrefix(c, "start") && strings.Contains(c, "-src") {
			t.Fatalf("source container started: %s", c)
		}
	}
}
