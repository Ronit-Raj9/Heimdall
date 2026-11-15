package environments

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// observerDocker wraps fakeDocker: `run -i` of the process or workspace
// observer prints canned observer output, everything else goes to the base
// fake.
func observerDocker(t *testing.T, procOut, fsOut string) (utils.Docker, string) {
	t.Helper()
	base, logPath := fakeDocker(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "proc.out"), procOut)
	writeFile(t, filepath.Join(dir, "fs.out"), fsOut)
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  "start -a -i"*procwatch*) %[1]q "$@"; cat > /dev/null; cat %[2]q; exit 0 ;;
  "run -i"*fswatch*) %[1]q "$@"; cat > /dev/null; cat %[3]q; exit 0 ;;
esac
exec %[1]q "$@"
`, base.Binary, filepath.Join(dir, "proc.out"), filepath.Join(dir, "fs.out"))
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func observerRun(t *testing.T) *schemas.RunContext {
	t.Helper()
	return &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_1"},
		Suite:  &schemas.Suite{},
		Task:   &schemas.Task{ID: "t"},
		Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"},
		Dir:    t.TempDir(),
		Key:    &schemas.RunKey{Value: "sk-run-key-123"},
	}
}

// TestExecObserversWiring checks that Exec starts both observers with their
// hardening (PID namespace of the agent, read-only workspace, no network,
// labels), redacts the run key from argv and writes the run files.
func TestExecObserversWiring(t *testing.T) {
	now := float64(time.Now().Unix())
	procOut := fmt.Sprintf(`{"t":"ready","pid":9,"interval_ms":20,"last_pid":8}
{"t":"start","pid":1,"ppid":0,"st":100,"start":%[1]f,"seen":%[1]f,"uid":1000,"comm":"sh","argv":["sh","-c","run"],"cwd":"/workspace"}
{"t":"start","pid":12,"ppid":1,"st":200,"start":%[1]f,"seen":%[1]f,"uid":1000,"comm":"curl","argv":["curl","-H","Authorization: Bearer sk-run-key-123","http://x"],"cwd":"/workspace"}
{"t":"end","pid":12,"st":200,"at":%[1]f,"exit":0}
{"t":"stats","alloc":14,"seen":12,"scans":3,"cpu":0.1,"interval_ms":20,"final":false}
`, now)
	fsOut := fmt.Sprintf(`{"t":"ready","watches":3}
{"t":"fs","ts":%[1]f,"op":"create","path":"src/new.py","size":12}
{"t":"stats","events":1,"dropped":2,"watches":3,"notes":[],"final":true}
`, now)
	docker, logPath := observerDocker(t, procOut, fsOut)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ProcessWatch: true, WorkspaceWatch: true})
	rc := observerRun(t)
	if _, err := e.Exec(t.Context(), rc, &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}}); err != nil {
		t.Fatal(err)
	}
	all := calls(t, logPath)
	for _, want := range []struct {
		prefix string
		parts  []string
	}{
		{"create -i --name bench-r_1-procwatch", []string{"--pid container:bench-r_1", "--network none", "--cap-drop ALL", "--cap-add SYS_PTRACE", "--read-only", "--security-opt no-new-privileges", "--label", "python3 helper:1 -u - 20"}},
		{"run -i --name bench-r_1-fswatch", []string{"-v bench-ws-r_1:/w:ro", "--network none", "--cap-drop ALL", "--cap-add DAC_READ_SEARCH", "--read-only", "--label"}},
	} {
		if callWith(all, want.prefix, want.parts...) == "" {
			t.Errorf("no call %q with %q in:\n%s", want.prefix, want.parts, strings.Join(all, "\n"))
		}
	}
	procs, evs, statuses, err := ReadObservers(rc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 || len(evs) != 1 || len(statuses) != 2 {
		t.Fatalf("procs %d events %d statuses %+v", len(procs), len(evs), statuses)
	}
	raw, _ := os.ReadFile(filepath.Join(rc.Dir, schemas.ProcessesFile))
	if strings.Contains(string(raw), "sk-run-key-123") {
		t.Fatalf("run key in processes.jsonl: %s", raw)
	}
	if procs[1].ExitCode == nil || *procs[1].ExitCode != 0 || procs[1].RunningAtEnd || !procs[0].RunningAtEnd {
		t.Fatalf("records %+v", procs)
	}
	p, f := statuses[0], statuses[1]
	if p.Observer != schemas.ObserverProc || p.State != schemas.ObserverOn || p.PIDsAllocated != 14 || p.PIDsSeen != 12 || !strings.Contains(p.Coverage, "at most 2 processes were missed") {
		t.Fatalf("process status %+v", p)
	}
	if f.Observer != schemas.ObserverFS || f.State != schemas.ObserverOn || f.Dropped != 2 || f.Records != 1 {
		t.Fatalf("workspace status %+v", f)
	}
}

// TestExecObserverFailureNeverFailsRun checks that an observer that cannot
// start is recorded as missing with a reason and the agent still runs.
func TestExecObserverFailureNeverFailsRun(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ProcessWatch: true, WorkspaceWatch: true})
	rc := observerRun(t)
	res, err := e.Exec(t.Context(), rc, &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}})
	if err != nil || res == nil {
		t.Fatalf("exec: %v", err)
	}
	if callWith(calls(t, logPath), "start -a bench-r_1") == "" {
		t.Fatal("agent did not run")
	}
	_, _, statuses, err := ReadObservers(rc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses %+v", statuses)
	}
	for _, s := range statuses {
		if s.State != schemas.ObserverFailed || (!strings.Contains(s.Reason, "before it was ready") && !strings.Contains(s.Reason, "could join")) {
			t.Errorf("status %+v", s)
		}
	}
}

// TestExecObserversOff checks the two off paths: config off writes nothing,
// a remote agent records both observers as off.
func TestExecObserversOff(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := observerRun(t)
	if _, err := e.Exec(t.Context(), rc, &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, schemas.ObserversFile)); !os.IsNotExist(err) {
		t.Fatalf("observers.json written with observers off: %v", err)
	}
	if c := callWith(calls(t, logPath), "run -i"); c != "" {
		t.Fatalf("observer started: %s", c)
	}
	s := &stepObservers{e: &dockerWorkspace{cfg: DockerWorkspaceConfig{Logger: schemas.NoopLogger{}}}}
	s.writeOff(rc, (&dockerWorkspace{}).observersOff(&schemas.RunPlan{Egress: []string{"api.example.com:443"}}))
	_, _, statuses, err := ReadObservers(rc.Dir)
	if err != nil || len(statuses) != 2 || statuses[0].State != schemas.ObserverOff || !strings.HasPrefix(statuses[0].Reason, "remote agent") {
		t.Fatalf("statuses %+v %v", statuses, err)
	}
}

// TestParseProcLinesEarlyAndZombie covers signal exits and a missing PID
// counter report.
func TestParseProcLinesEarlyAndZombie(t *testing.T) {
	end := time.Unix(1000, 0).UTC()
	lines := [][]byte{
		[]byte(`{"t":"start","pid":5,"ppid":1,"st":7,"start":900.5,"uid":0,"comm":"x","argv":["x"]}`),
		[]byte(`{"t":"end","pid":5,"st":7,"at":901.25,"signal":9}`),
		[]byte(`not json`),
	}
	recs, st := parseProcLines(lines, "", 2, end, schemas.ObserverStatus{IntervalMS: 20})
	if len(recs) != 1 || recs[0].ExitCode == nil || *recs[0].ExitCode != 137 || recs[0].Session != 2 {
		t.Fatalf("records %+v", recs)
	}
	if !strings.Contains(st.Coverage, "not bounded") {
		t.Fatalf("coverage %q", st.Coverage)
	}
}

// TestWriteJSONLSessions checks indexed per-session files and the
// concatenated file, and observers.json appended across sessions.
func TestWriteJSONLSessions(t *testing.T) {
	dir := t.TempDir()
	for n := 1; n <= 2; n++ {
		if err := writeJSONL(dir, schemas.FSEventsFile, n, []schemas.FileEvent{{Op: "create", Path: fmt.Sprintf("f%d", n), Session: n}}); err != nil {
			t.Fatal(err)
		}
		if err := appendObserverStatus(dir, schemas.ObserverStatus{Observer: schemas.ObserverFS, Session: n, State: schemas.ObserverOn}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := readJSONL[schemas.FileEvent](filepath.Join(dir, schemas.FSEventsFile))
	if err != nil || len(all) != 2 {
		t.Fatalf("concatenated %+v %v", all, err)
	}
	second, _ := readJSONL[schemas.FileEvent](filepath.Join(dir, "fsevents.2.jsonl"))
	if len(second) != 1 || second[0].Path != "f2" {
		t.Fatalf("session 2 %+v", second)
	}
	var statuses []schemas.ObserverStatus
	b, _ := os.ReadFile(filepath.Join(dir, schemas.ObserversFile))
	if json.Unmarshal(b, &statuses) != nil || len(statuses) != 2 {
		t.Fatalf("statuses %s", b)
	}
}

// TestProcessObserverStoppedEarly checks that a process observer whose
// output ended well before the agent (a root agent can kill it) is recorded
// as missing with the reason, keeping the records it made.
func TestProcessObserverStoppedEarly(t *testing.T) {
	rc := observerRun(t)
	agentEnd := time.Now().UTC()
	o := &observer{done: make(chan struct{}), ended: agentEnd.Add(-5 * time.Second),
		lines: [][]byte{[]byte(`{"t":"start","pid":3,"ppid":1,"st":9,"start":1000.5,"argv":["sleep","9"]}`)}}
	close(o.done)
	o.once.Do(func() {})
	s := &stepObservers{e: &dockerWorkspace{cfg: DockerWorkspaceConfig{Logger: schemas.NoopLogger{}}}, procOn: true, interval: 20,
		procDone: make(chan struct{}), stopProc: make(chan struct{}), watching: true, proc: o}
	close(s.procDone)
	s.finish(t.Context(), rc, &schemas.ExecResult{Ended: agentEnd})
	procs, _, statuses, err := ReadObservers(rc.Dir)
	if err != nil || len(statuses) != 1 || len(procs) != 1 {
		t.Fatalf("statuses %+v procs %+v %v", statuses, procs, err)
	}
	if statuses[0].State != schemas.ObserverFailed || !strings.Contains(statuses[0].Reason, "CAP_KILL") {
		t.Fatalf("status %+v", statuses[0])
	}
}

// TestObserversCreateFailure checks that a run whose agent container never
// starts does not hang on the process observer.
func TestObserversCreateFailure(t *testing.T) {
	docker, _ := fakeDocker(t, "create --name")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", ProcessWatch: true})
	rc := observerRun(t)
	done := make(chan error, 1)
	go func() {
		_, err := e.Exec(t.Context(), rc, &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("create failure not reported")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Exec hung after a failed create")
	}
	_, _, statuses, _ := ReadObservers(rc.Dir)
	if len(statuses) != 1 || !strings.Contains(statuses[0].Reason, "never started") {
		t.Fatalf("statuses %+v", statuses)
	}
}
