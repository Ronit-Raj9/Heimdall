package environments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func streamRC(t *testing.T) *schemas.RunContext {
	return &schemas.RunContext{
		Run:        &schemas.Run{RunID: "r_1", ExpID: "e_1"},
		Slot:       &schemas.Slot{Arm: "A", StreamIndex: 2},
		Experiment: &schemas.Experiment{Stream: &schemas.StreamSpec{Memory: "carry"}},
		Task:       &schemas.Task{ID: "t"},
		Agent:      &schemas.AgentConfig{Memory: &schemas.AgentMemory{Paths: []string{"/home/agent/.mem"}}},
		Dir:        t.TempDir(),
	}
}

func TestStreamCarryEnablesMemory(t *testing.T) {
	rc := &schemas.RunContext{Task: &schemas.Task{ID: "t"}, Slot: &schemas.Slot{Arm: "A", StreamIndex: 1}, Experiment: &schemas.Experiment{Stream: &schemas.StreamSpec{Memory: "carry", FreshArm: "B"}}}
	if !memoryEnabled(rc) {
		t.Fatal("a carrying stream run needs memory volumes")
	}
	rc.Slot.Arm = "B"
	if memoryEnabled(rc) {
		t.Fatal("the fresh arm keeps the plain container")
	}
}

func TestArchiveAndSeedMemory(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := streamRC(t)
	dst := filepath.Join(rc.Dir, schemas.StreamMemoryArchive)
	if err := e.ArchiveMemory(t.Context(), rc, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("archive not written: %v", err)
	}
	if err := e.SeedMemory(t.Context(), rc, dst); err != nil {
		t.Fatal(err)
	}
	if err := e.SeedMemory(t.Context(), rc, filepath.Join(rc.Dir, "missing.tar")); err == nil {
		t.Fatal("a missing archive must be refused")
	}
	all := calls(t, logPath)
	for _, want := range []struct {
		prefix string
		parts  []string
	}{
		{"run --rm --name bench-r_1-memtar", []string{"--network none", "-v bench-mem-r_1:/bench-mem/0:ro", "-v bench-memp-r_1-0:/bench-mem/1:ro", "BENCH_MAX="}},
		{"run --rm --name bench-r_1-memseed", []string{"-v bench-mem-r_1:/bench-mem/0", "-v bench-memp-r_1-0:/bench-mem/1", dst + ":/bench-seed.tar:ro"}},
	} {
		if callWith(all, want.prefix, want.parts...) == "" {
			t.Errorf("no call %q with %q in:\n%s", want.prefix, want.parts, strings.Join(all, "\n"))
		}
	}
}

func TestKillAgent(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := streamRC(t)
	rc.Session, rc.SessionIndex = &schemas.SessionSpec{ID: "b"}, 1
	if err := e.KillAgent(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	if callWith(calls(t, logPath), "kill --signal KILL bench-r_1-s2") == "" {
		t.Fatalf("no kill call in %v", calls(t, logPath))
	}
}

func TestWorldTick(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := streamRC(t)
	rc.Task.Services = []schemas.ServiceSpec{{ID: "shop", Image: "shop:1", Port: 8080}, {ID: "worker", Image: "w:1"}}
	ticks, err := e.WorldTick(t.Context(), rc, 2, "2026-01-12T00:00:00Z", nil)
	if err != nil {
		t.Fatalf("a service that ignores the hook is no error: %v", err)
	}
	if len(ticks) != 1 || ticks[0].Service != "shop" || ticks[0].Date != "2026-01-12T00:00:00Z" {
		t.Fatalf("ticks %+v", ticks)
	}
	if callWith(calls(t, logPath), "exec bench-r_1-svcprobe", "python3 -c", "http://shop:8080/_world/tick", `"session":2`) == "" {
		t.Fatalf("no tick call in %v", calls(t, logPath))
	}
	if _, err := e.WorldTick(t.Context(), rc, 2, "d", []schemas.WorldEvent{{AfterSession: 1, Service: "shop", Action: "price_change"}}); err == nil {
		t.Fatal("an undelivered event must fail")
	}
	if _, err := e.WorldTick(t.Context(), rc, 2, "d", []schemas.WorldEvent{{AfterSession: 1, Service: "worker", Action: "x"}}); err == nil {
		t.Fatal("an event for a service without a port must fail")
	}
}
