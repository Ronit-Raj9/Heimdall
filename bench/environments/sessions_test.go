package environments

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestCheckAgentPath(t *testing.T) {
	for p, ok := range map[string]bool{
		"/home/agent/.claude/projects": true, "/opt/skills": true, "/tmp/x": true,
		"relative": false, "/a/../b": false, "/": false, "/workspace": false, "/workspace/x": false,
		"/opt/bench/shim": false, "/memory": false, "/memory/x": false, "/proc/x": false,
	} {
		if err := checkAgentPath(p); (err == nil) != ok {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestMemoryPathsAndEnabled(t *testing.T) {
	plain := &schemas.RunContext{Task: &schemas.Task{}, Agent: &schemas.AgentConfig{}}
	if memoryEnabled(plain) {
		t.Fatal("plain run has memory")
	}
	sessions := &schemas.RunContext{Task: &schemas.Task{Sessions: []schemas.SessionSpec{{ID: "a"}}}, Agent: &schemas.AgentConfig{}}
	if !memoryEnabled(sessions) {
		t.Fatal("multi-session run has no memory")
	}
	rc := &schemas.RunContext{Task: &schemas.Task{}, Agent: &schemas.AgentConfig{Memory: &schemas.AgentMemory{Paths: []string{"/home/agent/.mem", "/opt/mem"}}}}
	got, err := memoryPaths(rc)
	if err != nil || !memoryEnabled(rc) || !reflect.DeepEqual(got, []string{"/home/agent/.mem", "/opt/mem"}) {
		t.Fatalf("paths %v %v", got, err)
	}
	if v := memoryVolumes("r_1", 2); !reflect.DeepEqual(v, []string{"bench-mem-r_1", "bench-memp-r_1-0", "bench-memp-r_1-1"}) {
		t.Fatalf("volumes %v", v)
	}
	rc.Agent.Memory.Paths = []string{"/x", "/x"}
	if _, err := memoryPaths(rc); err == nil {
		t.Fatal("repeated path accepted")
	}
	rc.Agent.Memory.Paths = []string{"/workspace/notes"}
	if _, err := memoryPaths(rc); err == nil {
		t.Fatal("path inside the workspace accepted")
	}
}

func TestLinked(t *testing.T) {
	for p, want := range map[string]bool{"/home/agent/.claude/skills/pdf": true, "/tmp/x": true, "/home/agent": false, "/opt/skills": false, "/home/agentx/a": false} {
		if got := linked(p); got != want {
			t.Errorf("linked(%q) = %v", p, got)
		}
	}
	if err := checkAgentPath("/bench/dirs/0"); err == nil {
		t.Error("path under /bench accepted")
	}
}

func TestPlanDirs(t *testing.T) {
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(host, "skills"), filepath.Join(host, "link")); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(host, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := planDirs(&schemas.RunPlan{Dirs: map[string]string{"/home/agent/.skills": filepath.Join(host, "link"), "/opt/a": filepath.Join(host, "skills")}})
	if err != nil {
		t.Fatal(err)
	}
	want := []planDir{{dst: "/home/agent/.skills", src: real}, {dst: "/opt/a", src: real}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	for name, dirs := range map[string]map[string]string{
		"relative host":  {"/opt/a": "skills"},
		"missing host":   {"/opt/a": filepath.Join(host, "nope")},
		"reserved dst":   {"/workspace/skills": filepath.Join(host, "skills")},
		"relative dst":   {"skills": filepath.Join(host, "skills")},
		"host is a file": {"/opt/a": os.Args[0]},
	} {
		if _, err := planDirs(&schemas.RunPlan{Dirs: dirs}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSessionNamesAndStreams(t *testing.T) {
	rc := &schemas.RunContext{Dir: "/run"}
	if agentName(rc, "r_1") != "bench-r_1" || !reflect.DeepEqual(staleAgents(rc, "r_1"), []string{"bench-r_1"}) {
		t.Fatal("single session names")
	}
	if out, errp, n := streamPaths(rc); out != "/run/agent.stream.jsonl" || errp != "/run/agent.stderr.txt" || n != 0 {
		t.Fatalf("single session streams %s %s %d", out, errp, n)
	}
	rc.Session, rc.SessionIndex = &schemas.SessionSpec{ID: "b"}, 1
	if agentName(rc, "r_1") != "bench-r_1-s2" || !reflect.DeepEqual(staleAgents(rc, "r_1"), []string{"bench-r_1", "bench-r_1-s1", "bench-r_1-s2"}) {
		t.Fatal("session names")
	}
	if out, errp, n := streamPaths(rc); out != "/run/agent.stream.2.jsonl" || errp != "/run/agent.stderr.2.txt" || n != 2 {
		t.Fatalf("session streams %s %s %d", out, errp, n)
	}
}

func TestAppendSession(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "agent.stream.jsonl"), "stale\n")
	for n, line := range []string{"one\n", "two\n"} {
		out, errp := filepath.Join(dir, "o"), filepath.Join(dir, "e")
		writeFile(t, out, line)
		writeFile(t, errp, "err"+line)
		if err := appendSession(dir, n+1, out, errp); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(readOut(t, filepath.Join(dir, "agent.stream.jsonl"))); got != "one\ntwo\n" {
		t.Fatalf("stream %q", got)
	}
	if got := string(readOut(t, filepath.Join(dir, "agent.stderr.txt"))); got != "errone\nerrtwo\n" {
		t.Fatalf("stderr %q", got)
	}
}

func TestBeginSessionChecks(t *testing.T) {
	task := t.TempDir()
	writeFile(t, filepath.Join(task, "s1", "a.txt"), "a")
	if err := os.Symlink("/etc", filepath.Join(task, "leak")); err != nil {
		t.Fatal(err)
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: filepath.Join(task, "no-docker")}})
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Handle: &schemas.EnvironmentHandle{Volume: "v", BaselineRef: "x"},
		Task: &schemas.Task{ID: "t", Dir: task, Sessions: []schemas.SessionSpec{{ID: "a"}, {ID: "b", Files: "leak"}}}}
	if err := e.BeginSession(t.Context(), rc, 0); err != nil || rc.Handle.BaselineRef != "x" {
		t.Fatalf("session 0 without files must change nothing: %v %s", err, rc.Handle.BaselineRef)
	}
	if err := e.BeginSession(t.Context(), rc, 2); err == nil || !strings.Contains(err.Error(), "no session 2") {
		t.Fatalf("out of range: %v", err)
	}
	if err := e.BeginSession(t.Context(), rc, 1); err == nil || !strings.Contains(err.Error(), "outside the folder") {
		t.Fatalf("escaping files: %v", err)
	}
}

// TestExecSessionWithFakeDocker checks the wiring of one session's Exec:
// earlier containers removed, the session's own name, memory and plan folder
// mounts, the service network joined, the stream files written and
// appended, and the container removed after.
func TestExecSessionWithFakeDocker(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "evalnet"})
	skills := t.TempDir()
	rc := &schemas.RunContext{
		Run:     &schemas.Run{RunID: "r_1"},
		Suite:   &schemas.Suite{},
		Task:    &schemas.Task{ID: "t", Sessions: []schemas.SessionSpec{{ID: "a"}, {ID: "b"}}},
		Agent:   &schemas.AgentConfig{Memory: &schemas.AgentMemory{Paths: []string{"/home/agent/.mem"}}},
		Handle:  &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"},
		Dir:     t.TempDir(),
		Session: &schemas.SessionSpec{ID: "b"}, SessionIndex: 1,
		Services: []schemas.ServiceHandle{{Spec: schemas.ServiceSpec{ID: "kv"}}},
	}
	writeFile(t, filepath.Join(rc.Dir, "agent.stream.jsonl"), "out bench-r_1-s1\n")
	plan := &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}, Dirs: map[string]string{"/opt/skills": skills}}
	res, err := e.Exec(t.Context(), rc, plan)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.StdoutPath != filepath.Join(rc.Dir, "agent.stream.2.jsonl") {
		t.Fatalf("stdout %s", res.StdoutPath)
	}
	if got := string(readOut(t, filepath.Join(rc.Dir, "agent.stream.jsonl"))); got != "out bench-r_1-s1\nout bench-r_1-s2\n" {
		t.Fatalf("concatenated %q", got)
	}
	all := calls(t, logPath)
	for _, want := range []struct {
		prefix string
		parts  []string
	}{
		{"rm -f -v bench-r_1 bench-r_1-s1 bench-r_1-s2", nil},
		{"create --name bench-r_1-s2", []string{"--network evalnet", "-v bench-mem-r_1:/memory", "-v bench-memp-r_1-0:/bench/memory/0", "-v bench-dir-r_1-0:/opt/skills:ro", "--read-only", "--entrypoint /bin/sh"}},
		{"run --rm --name bench-r_1-fill", []string{"-v bench-mem-r_1:/bench-mem/0", "-v bench-dir-r_1-0:/bench-dir/0", ":/bench-src/0:ro", "BENCH_UID=1000"}},
		{"network connect bench-net-r_1 bench-r_1-s2", nil},
		{"start -a bench-r_1-s2", nil},
		{"rm -f -v bench-r_1-s2", nil},
	} {
		if callWith(all, want.prefix, want.parts...) == "" {
			t.Errorf("no call %q with %q in:\n%s", want.prefix, want.parts, strings.Join(all, "\n"))
		}
	}
}

// TestExecKeepsContainerForSnapshot checks that a snapshot-graded run keeps
// the exited agent container, with a writable root.
func TestExecKeepsContainerForSnapshot(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
	rc := &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_1"},
		Suite:  &schemas.Suite{Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}},
		Task:   &schemas.Task{ID: "t"},
		Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"},
		Dir:    t.TempDir(),
	}
	if _, err := e.Exec(t.Context(), rc, &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}}); err != nil {
		t.Fatal(err)
	}
	all := calls(t, logPath)
	if c := callWith(all, "create --name bench-r_1"); c == "" || strings.Contains(c, "--read-only") || strings.Contains(c, "--tmpfs /tmp") {
		t.Fatalf("create %q", c)
	}
	if last := all[len(all)-1]; strings.HasPrefix(last, "rm ") {
		t.Fatalf("container removed: %s", last)
	}
}
