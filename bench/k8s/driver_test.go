package k8s

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeAPI is an in-memory Kubernetes API server: objects by path, a run pod
// whose container states follow what the driver did (seeded, attached), and
// exec and attach over WebSocket that act like the prep, bench and agent
// containers would.
type fakeAPI struct {
	t         *testing.T
	mu        sync.Mutex
	objs      map[string]map[string]any
	seeded    bool
	attached  bool
	exit      int
	seedTar   []byte
	execs     []string
	deleted   []string
	key       string
	noEnforce bool
	stall     chan struct{}   // non-nil: attach marks the agent done, then never answers until closed
	workspace string          // the artifact script's workspace root
	laid      []string        // staged tars extracted in prep: destination and member names
	deny      map[string]bool // access reviews answered "denied", by "verb resource[/sub]"
}

const fakeRef = "0123456789abcdef0123456789abcdef01234567"

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, objs: map[string]map[string]any{}}
	f.objs["/api/v1/namespaces/heimdall-gw/services/bifrost"] = map[string]any{
		"metadata": map[string]any{"name": "bifrost"},
		"spec": map[string]any{"clusterIP": "10.96.0.50", "selector": map[string]any{"app.kubernetes.io/name": "bifrost"},
			"ports": []any{map[string]any{"port": 8080, "targetPort": 8080}}},
	}
	return f
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/exec"):
		f.exec(w, r)
		return
	case strings.HasSuffix(p, "/attach"):
		f.attach(w, r)
		return
	case strings.HasSuffix(p, "/pods/egress-probe/log"):
		f.probeLog(w, p)
		return
	case strings.HasSuffix(p, "/selfsubjectaccessreviews"):
		var rv struct {
			Spec struct {
				RA struct{ Verb, Resource, Subresource string } `json:"resourceAttributes"`
			} `json:"spec"`
		}
		json.NewDecoder(r.Body).Decode(&rv)
		name := rv.Spec.RA.Verb + " " + rv.Spec.RA.Resource
		if rv.Spec.RA.Subresource != "" {
			name += "/" + rv.Spec.RA.Subresource
		}
		json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"allowed": !f.deny[name]}})
		return
	case strings.HasSuffix(p, "/events"):
		w.Write([]byte(`{"items":[{"type":"Normal","reason":"Started","message":"Started container agent","involvedObject":{"kind":"Pod","name":"run"}}]}`))
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPost:
		var obj map[string]any
		json.NewDecoder(r.Body).Decode(&obj)
		meta, _ := obj["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		key := p + "/" + name
		if _, ok := f.objs[key]; ok {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"kind":"Status","reason":"AlreadyExists","message":"exists"}`))
			return
		}
		meta["uid"] = "uid-" + name
		f.objs[key] = obj
		json.NewEncoder(w).Encode(obj)
	case http.MethodGet:
		obj, ok := f.objs[p]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"kind":"Status","reason":"NotFound","message":"not found"}`))
			return
		}
		if strings.HasSuffix(p, "/pods/egress-probe") {
			obj["status"] = map[string]any{"phase": "Succeeded"}
		}
		if strings.HasSuffix(p, "/pods/run") {
			obj["status"] = f.podStatus()
			obj["spec"].(map[string]any)["nodeName"] = "node-1"
		}
		json.NewEncoder(w).Encode(obj)
	case http.MethodDelete:
		var opts struct {
			Preconditions struct {
				UID string `json:"uid"`
			} `json:"preconditions"`
		}
		json.NewDecoder(r.Body).Decode(&opts)
		if want := opts.Preconditions.UID; want != "" {
			meta, _ := f.objs[p]["metadata"].(map[string]any)
			if got, _ := meta["uid"].(string); f.objs[p] != nil && got != want {
				w.WriteHeader(http.StatusConflict)
				w.Write([]byte(`{"kind":"Status","reason":"Conflict","message":"uid precondition failed"}`))
				return
			}
		}
		f.deleted = append(f.deleted, p)
		nsScope := ""
		if strings.HasPrefix(p, "/api/v1/namespaces/") && strings.Count(p, "/") == 4 {
			nsScope = "/namespaces/" + strings.TrimPrefix(p, "/api/v1/namespaces/") + "/"
		}
		for k := range f.objs {
			if k == p || strings.HasPrefix(k, p+"/") || (nsScope != "" && strings.Contains(k, nsScope)) {
				delete(f.objs, k)
			}
		}
		w.Write([]byte(`{}`))
	}
}

// podStatus moves the run pod along: prep runs until seeded, then the agent
// runs until it was attached, then it is terminated.
func (f *fakeAPI) podStatus() map[string]any {
	running := map[string]any{"running": map[string]any{"startedAt": "2026-11-18T00:00:00Z"}}
	waiting := map[string]any{"waiting": map[string]any{"reason": "PodInitializing"}}
	agent := waiting
	prep := running
	if f.seeded {
		prep = map[string]any{"terminated": map[string]any{"exitCode": 0}}
		agent = running
	}
	if f.attached {
		agent = map[string]any{"terminated": map[string]any{"exitCode": f.exit, "reason": "Completed"}}
	}
	return map[string]any{"phase": "Running",
		"initContainerStatuses": []any{map[string]any{"name": "prep", "state": prep, "imageID": "sha256:helper"},
			map[string]any{"name": "fswatch", "state": running, "restartCount": 0}, map[string]any{"name": "procwatch", "state": running}, map[string]any{"name": "collector", "state": running}},
		"containerStatuses": []any{map[string]any{"name": "agent", "state": agent, "imageID": "sha256:agent"},
			map[string]any{"name": "bench", "state": running}}}
}

func (f *fakeAPI) exec(w http.ResponseWriter, r *http.Request) {
	cmd := r.URL.Query()["command"]
	container := r.URL.Query().Get("container")
	s, ok := acceptWS(w, r, []string{protoV5})
	if !ok {
		return
	}
	var in []byte
	if r.URL.Query().Get("stdin") == "true" {
		in = s.readStdin(f.t)
	}
	all := strings.Join(cmd, " ")
	f.mu.Lock()
	f.execs = append(f.execs, container+": "+clip(all, 60))
	f.mu.Unlock()
	switch {
	case container == ctrPrep && strings.Contains(all, "git -c init.defaultBranch=main init"):
		f.mu.Lock()
		f.seedTar, f.seeded = in, true
		f.mu.Unlock()
		s.send(streamStdout, []byte(fakeRef+"\n"))
	case container == ctrBench && strings.Contains(all, "BENCH_BASE="+fakeRef) && strings.Contains(all, "git"):
		s.send(streamStdout, []byte("diff --git a/x.py b/x.py\nnew file mode 100644\n--- /dev/null\n+++ b/x.py\n@@ -0,0 +1 @@\n+print(1)\n"))
	case container == ctrPrep && len(cmd) == 6 && cmd[0] == "tar" && cmd[1] == "-x":
		f.mu.Lock()
		f.laid = append(f.laid, cmd[5]+":"+strings.Join(tarList(f.t, in), ","))
		f.mu.Unlock()
	case container == ctrBench && strings.Contains(all, "cd /bench-aux"):
		s.send(streamStdout, makeTar(f.t, map[string]string{"log/shim.jsonl": `{"tool":"pytest","action":"crash"}` + "\n"}))
	case container == ctrBench && strings.Contains(all, "PPid"):
		s.send(streamStdout, []byte("40\n"))
	case container == ctrBench && strings.Contains(all, "BENCH_SPECS="):
		// Run the real artifact script on the test's workspace folder.
		var env []string
		i := 1
		for ; i < len(cmd) && strings.Contains(cmd[i], "="); i++ {
			env = append(env, cmd[i])
		}
		py := exec.Command("python3", "-I", "-c", cmd[i+3], f.workspace)
		py.Env = append(os.Environ(), env...)
		out, err := py.Output()
		if err != nil {
			f.t.Errorf("artifact script: %v", err)
		}
		for len(out) > 0 {
			n := min(len(out), 16<<10)
			s.send(streamStdout, out[:n])
			out = out[n:]
		}
	case container == ctrBench && strings.Contains(all, "python3 -I -c") && strings.Contains(all, "missing.txt"):
		s.finish(`{"status":"Failure","reason":"NonZeroExitCode","message":"exit 3","details":{"causes":[{"reason":"ExitCode","message":"3"}]}}`)
		return
	case container == ctrBench && strings.Contains(all, "python3 -I -c"):
		s.send(streamStdout, []byte("42\n"))
	case container == ctrBench && len(cmd) == 4 && cmd[0] == "head" && strings.HasSuffix(cmd[3], "procwatch.jsonl"):
		lines := []string{`{"t":"ready","pid":30,"interval_ms":20,"last_pid":30}`,
			`{"t":"start","pid":1,"ppid":0,"st":1,"start":1700000000,"seen":1700000000,"uid":65535,"comm":"pause","argv":["/pause"],"cwd":"/"}`,
			`{"t":"start","pid":9,"ppid":0,"st":2,"start":1700000000,"seen":1700000000,"uid":0,"comm":"sleep","argv":["sleep","2147483647"],"cwd":"/"}`,
			`{"t":"start","pid":40,"ppid":0,"st":3,"start":1700000001,"seen":1700000001,"uid":1000,"comm":"sh","argv":["/bin/sh","-c","gate"],"cwd":"/workspace"}`,
			`{"t":"start","pid":41,"ppid":40,"st":4,"start":1700000001.5,"seen":1700000001.5,"uid":1000,"comm":"git","argv":["git","apply","key=` + f.key + `"],"cwd":"/workspace"}`,
			`{"t":"end","pid":41,"st":4,"at":1700000002,"exit":0}`,
			`{"t":"stats","alloc":44,"seen":40,"interval_ms":20,"final":true}`}
		s.send(streamStdout, []byte(strings.Join(lines, "\n")+"\n"))
	case container == ctrBench && len(cmd) == 4 && cmd[0] == "head" && strings.HasSuffix(cmd[3], "fswatch.jsonl"):
		s.send(streamStdout, []byte(`{"t":"ready","watches":3}`+"\n"+`{"t":"fs","ts":1700000001.6,"op":"create","path":"x.py","size":9}`+"\n"+`{"t":"stats","events":1,"dropped":0,"watches":3}`+"\n"))
	}
	s.finish(`{"status":"Success"}`)
}

func (f *fakeAPI) attach(w http.ResponseWriter, r *http.Request) {
	s, ok := acceptWS(w, r, []string{protoV5})
	if !ok {
		return
	}
	in := s.readStdin(f.t)
	if string(in) != "\n" {
		f.t.Errorf("attach stdin %q, want one newline", in)
	}
	if f.stall != nil {
		f.mu.Lock()
		f.attached = true
		f.mu.Unlock()
		<-f.stall
		return
	}
	s.send(streamStdout, []byte(`{"type":"result","text":"done with `+f.key+`"}`+"\n"))
	s.send(streamStderr, []byte("agent stderr\n"))
	f.mu.Lock()
	f.attached = true
	f.mu.Unlock()
	s.finish(`{"status":"Success"}`)
}

// fakeDocker is a docker CLI that answers `image inspect` with a config.
func fakeDocker(t *testing.T) utils.Docker {
	dir := t.TempDir()
	p := filepath.Join(dir, "docker")
	script := `#!/bin/sh
if [ "$1" = image ] && [ "$2" = inspect ]; then
  echo '{"Env":["PATH=/usr/local/bin:/usr/bin:/bin"],"Entrypoint":["/entry"],"Cmd":["serve"]}'
  exit 0
fi
echo "unexpected docker $*" >&2
exit 1
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: p}
}

func testDriver(t *testing.T, f *fakeAPI) *Driver {
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	dk := fakeDocker(t)
	d, err := New(Config{Client: testClient(t, srv), GatewayURL: "http://bifrost.heimdall-gw.svc:8080", AgentGatewayURL: "http://bifrost:8080",
		EvalNetwork: "bench-evalnet", HelperImage: "helper:1", ProcessWatch: true, WorkspaceWatch: true, EventsCollector: true,
		Local: environments.NewDockerDriver(environments.DockerWorkspaceConfig{Docker: dk}), Docker: dk})
	if err != nil {
		t.Fatal(err)
	}
	d.policySettle = 0
	return d
}

func testRun(t *testing.T) *schemas.RunContext {
	taskDir := t.TempDir()
	os.MkdirAll(filepath.Join(taskDir, "repo", "src"), 0o755)
	os.WriteFile(filepath.Join(taskDir, "repo", "src", "app.py"), []byte("x = 1\n"), 0o644)
	os.MkdirAll(filepath.Join(taskDir, "fixtures"), 0o755)
	os.WriteFile(filepath.Join(taskDir, "fixtures", "notes.clean.md"), []byte("notes\n"), 0o644)
	return &schemas.RunContext{
		Run:   &schemas.Run{RunID: "r_test1", ExpID: "e_test"},
		Suite: &schemas.Suite{ID: "coding-core", Environment: environments.DockerWorkspaceType},
		Task: &schemas.Task{ID: "t01", Dir: taskDir, RepoDir: "repo", Protected: []string{"src/**"},
			Notes: schemas.TaskNotes{Clean: "fixtures/notes.clean.md"}},
		Agent: &schemas.AgentConfig{ID: "oracle", Adapter: "scripted", Notes: schemas.AgentNotes{ProjectFile: "CLAUDE.md"}},
		Dir:   t.TempDir(),
		Key:   &schemas.RunKey{Value: "sk-bf-runkey"},
	}
}

func TestDriverLifecycle(t *testing.T) {
	f := newFakeAPI(t)
	f.key = "sk-bf-runkey"
	d := testDriver(t, f)
	rc := testRun(t)
	ctx := t.Context()
	h, err := d.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.Network != d.EvalIdentity() || h.BaselineRef != "" || h.Protected["src/app.py"] == "" || h.FileHashes["CLAUDE.md"] == "" {
		t.Fatalf("handle: %+v", h)
	}
	ns := "/api/v1/namespaces/heimdall-run-r-test1"
	for _, want := range []string{"/api/v1/namespaces/heimdall-run-r-test1", ns + "/configmaps/run", "/apis/networking.k8s.io/v1/namespaces/heimdall-run-r-test1/networkpolicies/deny-all"} {
		if _, ok := f.objs[want]; !ok {
			t.Fatalf("create made no %s", want)
		}
	}
	rc.Handle = h
	plan := &schemas.RunPlan{Image: "bench/coding-core-base:1", Argv: []string{"git", "apply", "/tmp/bench/reference.diff"},
		Files: map[string]string{"/tmp/bench/reference.diff": "patch"}, Env: map[string]string{"BENCH_RUN_KEY": "sk-bf-runkey"}, WallS: 60}
	res, err := d.Exec(ctx, rc, plan)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 || res.TimedOut || rc.Handle.BaselineRef != fakeRef {
		t.Fatalf("result %+v ref %s", res, rc.Handle.BaselineRef)
	}
	out, _ := os.ReadFile(res.StdoutPath)
	errOut, _ := os.ReadFile(res.StderrPath)
	if strings.Contains(string(out), "sk-bf-runkey") || !strings.Contains(string(out), "[redacted]") || string(errOut) != "agent stderr\n" {
		t.Fatalf("streams: %q %q", out, errOut)
	}
	// The pod: owned by the anchor, gated agent with the wrapper argv, the
	// gateway reachable through the policy and /etc/hosts.
	var pod Pod
	raw, _ := json.Marshal(f.objs[ns+"/pods/run"])
	json.Unmarshal(raw, &pod)
	if len(pod.Metadata.OwnerReferences) != 1 || pod.Metadata.OwnerReferences[0].UID != "uid-run" {
		t.Fatalf("pod owner: %+v", pod.Metadata.OwnerReferences)
	}
	agent := pod.Spec.Containers[0]
	if agent.Command[4] != "/bin/sh" || agent.Command[5] != "-c" || agent.Command[7] != "bench-wrap" || agent.Command[8] != "git" {
		t.Fatalf("agent argv: %q", agent.Command)
	}
	if len(pod.Spec.HostAliases) != 1 || pod.Spec.HostAliases[0].IP != "10.96.0.50" {
		t.Fatalf("host aliases: %+v", pod.Spec.HostAliases)
	}
	if _, ok := f.objs["/apis/networking.k8s.io/v1/namespaces/heimdall-run-r-test1/networkpolicies/allow-gateway"]; !ok {
		t.Fatal("no gateway egress policy for a networked plan")
	}
	var sec Secret
	raw, _ = json.Marshal(f.objs[ns+"/secrets/agent-env"])
	json.Unmarshal(raw, &sec)
	if string(sec.Data["PATH"]) != "/opt/bench/shim:/usr/local/bin:/usr/bin:/bin" || string(sec.Data["HOME"]) != "/home/agent" || string(sec.Data["BENCH_RUN_KEY"]) != "sk-bf-runkey" {
		t.Fatalf("agent env: PATH %q HOME %q", sec.Data["PATH"], sec.Data["HOME"])
	}
	if _, ok := sec.Data["BENCH_WRAP_F0_PATH"]; !ok {
		t.Fatal("plan files must ride in the Docker driver's wrapper variables")
	}
	// The seed tar holds the repo and the clean notes.
	names := tarNames(t, f.seedTar)
	if !names["src/app.py"] || !names["CLAUDE.md"] {
		t.Fatalf("seed tar: %v", names)
	}
	// Observers: pause and root processes left out, the agent main is PID 1.
	procs, evs, statuses, err := environments.ReadObservers(rc.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 || procs[0].PID != 1 || procs[1].PPID != 1 || strings.Contains(strings.Join(procs[1].Argv, " "), "sk-bf-runkey") {
		t.Fatalf("processes: %+v", procs)
	}
	if len(evs) != 1 || evs[0].Path != "x.py" || len(statuses) != 2 || statuses[0].State != schemas.ObserverOn || !strings.Contains(statuses[0].Coverage, "pause") {
		t.Fatalf("events %+v statuses %+v", evs, statuses)
	}
	if b, _ := os.ReadFile(filepath.Join(rc.Dir, environments.CollectorStateFile)); !strings.Contains(string(b), "missing") {
		t.Fatalf("collector state: %s", b)
	}
	var rec record
	b, _ := os.ReadFile(filepath.Join(rc.Dir, FileRecord))
	if err := json.Unmarshal(b, &rec); err != nil || rec.Node != "node-1" || rec.ImageIDs["agent"] != "sha256:agent" || rec.BaselineRef != fakeRef || len(rec.Events) != 1 {
		t.Fatalf("kubernetes.json: %s", b)
	}
	diff, err := d.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "x.py" {
		t.Fatalf("diff: %+v", diff)
	}
	if err := d.Destroy(ctx, rc); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := d.Destroy(ctx, rc); err != nil {
		t.Fatalf("destroy twice: %v", err)
	}
	if strings.Join(f.deleted[:2], " ") != ns+"/configmaps/run "+ns {
		t.Fatalf("delete order: %v", f.deleted)
	}
	if len(f.objs) != 1 {
		t.Fatalf("objects left: %d", len(f.objs))
	}
}

func TestDriverNoNetworkPlanGetsNoGateway(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	rc := testRun(t)
	h, err := d.Create(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	rc.Handle = h
	if _, err := d.Exec(t.Context(), rc, &schemas.RunPlan{Image: "img", Argv: []string{"true"}, Network: "none"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objs["/apis/networking.k8s.io/v1/namespaces/heimdall-run-r-test1/networkpolicies/allow-gateway"]; ok {
		t.Fatal("a plan with no network got a gateway policy")
	}
	var pod Pod
	raw, _ := json.Marshal(f.objs["/api/v1/namespaces/heimdall-run-r-test1/pods/run"])
	json.Unmarshal(raw, &pod)
	if len(pod.Spec.HostAliases) != 0 {
		t.Fatal("a plan with no network got the gateway's address")
	}
}

func TestDriverRefusesAtRunTime(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	rc := testRun(t)
	rc.Probe = &schemas.ProbeSpec{ID: "hook", Layer: schemas.LayerAdapter}
	if _, err := d.Create(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "probe layer adapter") {
		t.Fatalf("a refused slot reached the cluster: %v", err)
	}
	rc = testRun(t)
	rc.Manifest = &schemas.Manifest{ExpID: "e_test"}
	if _, err := d.Create(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "planned for the docker driver") {
		t.Fatalf("a Docker experiment run on kubernetes: %v", err)
	}
	if len(f.objs) != 1 {
		t.Fatal("a refused run created objects")
	}
	rc = testRun(t)
	h, _ := d.Create(t.Context(), rc)
	rc.Handle = h
	for _, plan := range []*schemas.RunPlan{
		{Image: "img", Argv: []string{"x"}, User: "0"},
		{Image: "img", Argv: []string{"x"}, Network: "host"},
		{Image: "img", Argv: []string{"x"}, Dirs: map[string]string{"/skills": "/tmp"}},
		{Argv: []string{"x"}},
	} {
		if _, err := d.Exec(t.Context(), rc, plan); err == nil {
			t.Errorf("plan %+v accepted", plan)
		}
	}
}

func tarNames(t *testing.T, b []byte) map[string]bool {
	out := map[string]bool{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = true
	}
}

// TestDriverTaskEnv checks that a task's environment variables
// (environment.env) reach the agent's env secret under the plan's own, as
// under Docker, and that plan-time refusals let such a task through while a
// compose task (whose only service is the workspace) stays refused.
func TestDriverTaskEnv(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	rc := testRun(t)
	rc.Task.Environment = &schemas.TaskEnvironment{Env: map[string]string{"TASK_FLAG": "on", "BENCH_RUN_KEY": "from-task"}}
	slot := environments.DriverSlot{Suite: rc.Suite, Task: rc.Task, Agent: rc.Agent}
	if r := refusals(slot); len(r) != 0 {
		t.Fatalf("a task that only sets environment.env is refused: %v", r)
	}
	h, err := d.Create(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	rc.Handle = h
	plan := &schemas.RunPlan{Image: "img", Argv: []string{"true"}, Network: "none", Env: map[string]string{"BENCH_RUN_KEY": "sk-bf-runkey"}}
	if _, err := d.Exec(t.Context(), rc, plan); err != nil {
		t.Fatal(err)
	}
	var sec Secret
	raw, _ := json.Marshal(f.objs["/api/v1/namespaces/heimdall-run-r-test1/secrets/agent-env"])
	json.Unmarshal(raw, &sec)
	if string(sec.Data["TASK_FLAG"]) != "on" || string(sec.Data["BENCH_RUN_KEY"]) != "sk-bf-runkey" {
		t.Fatalf("agent env: TASK_FLAG %q BENCH_RUN_KEY %q", sec.Data["TASK_FLAG"], sec.Data["BENCH_RUN_KEY"])
	}
	if len(plan.Env) != 1 {
		t.Fatalf("the plan was changed: %v", plan.Env)
	}
	composed := slot
	composed.Task = &schemas.Task{Compose: "compose.yaml", Environment: &schemas.TaskEnvironment{Image: "app:1", WorkspacePath: "/app", Env: map[string]string{"A": "b"}}}
	if got := strings.Join(refusals(composed), "|"); !strings.Contains(got, RefuseCompose) || !strings.Contains(got, RefuseTaskImage) {
		t.Fatalf("compose task refusals: %q", got)
	}
}

func TestDriverCollectorSidecar(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	d.cfg.CollectorImage = "bench/bifrost-bench:dev"
	rc := testRun(t)
	h, err := d.Create(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	rc.Handle = h
	if _, err := d.Exec(t.Context(), rc, &schemas.RunPlan{Image: "img", Argv: []string{"true"}, Network: "none"}); err != nil {
		t.Fatal(err)
	}
	var sec Secret
	raw, _ := json.Marshal(f.objs["/api/v1/namespaces/heimdall-run-r-test1/secrets/agent-env"])
	json.Unmarshal(raw, &sec)
	if string(sec.Data["BENCH_EVENTS_URL"]) != "http://toolproxy:9200/events" || string(sec.Data["OTEL_RESOURCE_ATTRIBUTES"]) != "heimdall.run_id=r_test1" {
		t.Fatalf("collector env: %q %q", sec.Data["BENCH_EVENTS_URL"], sec.Data["OTEL_RESOURCE_ATTRIBUTES"])
	}
	if _, ok := sec.Data["BENCH_EVENTS_FILE"]; ok {
		t.Fatal("an events file nobody reads must not be offered")
	}
	if b, _ := os.ReadFile(filepath.Join(rc.Dir, environments.CollectorStateFile)); !strings.Contains(string(b), `"on"`) {
		t.Fatalf("collector state: %s", b)
	}
}

// probeLog answers like an enforcing network plugin: with the deny-all
// policy in the probe's namespace every forbidden target is blocked and the
// gateway (allowed by its policy) answers; without it everything answers.
// enforce false plays a plugin that ignores policies.
func (f *fakeAPI) probeLog(w http.ResponseWriter, p string) {
	ns := strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/namespaces/"), "/pods/egress-probe/log")
	f.mu.Lock()
	_, policed := f.objs["/apis/networking.k8s.io/v1/namespaces/"+ns+"/networkpolicies/deny-all"]
	pod := f.objs["/api/v1/namespaces/"+ns+"/pods/egress-probe"]
	enforce := !f.noEnforce
	f.mu.Unlock()
	raw, _ := json.Marshal(pod)
	var pd Pod
	json.Unmarshal(raw, &pd)
	var targets []probeTarget
	json.Unmarshal([]byte(pd.Spec.Containers[0].Command[3]), &targets)
	for _, t := range targets {
		state := "open"
		if policed && enforce && t.Name != "gateway" {
			state = "blocked"
		}
		w.Write([]byte(t.Name + " " + state + "\n"))
	}
}

func TestEgressProbeAndPreflight(t *testing.T) {
	f := newFakeAPI(t)
	f.objs["/api/v1/namespaces/kube-system/services/kube-dns"] = map[string]any{"metadata": map[string]any{"name": "kube-dns"}, "spec": map[string]any{"clusterIP": "10.96.0.10"}}
	f.objs["/api/v1/namespaces/default/services/kubernetes"] = map[string]any{"metadata": map[string]any{"name": "kubernetes"}, "spec": map[string]any{"clusterIP": "10.96.0.1"}}
	d := testDriver(t, f)
	rows := d.policyRows(t.Context(), true)
	if len(rows) != 1 || rows[0].Status != schemas.DoctorPass || !strings.Contains(rows[0].Detail, "enforced") || !strings.Contains(rows[0].Detail, "gateway answers") {
		t.Fatalf("enforcing plugin: %+v", rows)
	}
	inner := &fakeGW{m: &schemas.GatewayManifest{Settings: map[string]string{"egress_probe": "blocked", "eval_network": "bench-evalnet"},
		Problems: []string{"egress from bench-evalnet is open: dns", "provider x: kept"}}}
	m, err := WrapGateway(inner, d).Preflight(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if m.Settings["egress_probe"] != EgressBlocked || m.Settings["eval_network"] != d.EvalIdentity() || len(m.Problems) != 1 || m.Problems[0] != "provider x: kept" {
		t.Fatalf("wrapped preflight: %+v", m)
	}
	for k := range f.objs {
		if strings.Contains(k, "check-") {
			t.Fatalf("probe namespace left behind: %s", k)
		}
	}
	f.noEnforce = true
	rows = d.policyRows(t.Context(), true)
	if rows[0].Status != schemas.DoctorFail || !strings.Contains(rows[0].Detail, "NOT enforced") {
		t.Fatalf("a plugin that ignores policies must fail the check: %+v", rows)
	}
	m, _ = WrapGateway(inner, d).Preflight(t.Context())
	if m.Settings["egress_probe"] != EgressOpen || !strings.Contains(m.Settings["egress_probe_error"], "1.1.1.1") {
		t.Fatalf("open egress must be recorded: %+v", m.Settings)
	}
}

// fakeGW is a gateway whose preflight returns a fixed manifest.
type fakeGW struct {
	schemas.Gateway
	m *schemas.GatewayManifest
}

func (g *fakeGW) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	cp := *g.m
	cp.Settings = map[string]string{}
	for k, v := range g.m.Settings {
		cp.Settings[k] = v
	}
	cp.Problems = append([]string(nil), g.m.Problems...)
	return &cp, nil
}

func TestDriverExportAnswer(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	rc := testRun(t)
	rc.Handle = &schemas.EnvironmentHandle{}
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer, AnswerPath: "out/answer.txt"}
	a, err := d.ExportAnswer(t.Context(), rc)
	if err != nil || a.Source != "file" || a.Text != "42\n" {
		t.Fatalf("answer: %+v %v", a, err)
	}
	if b, _ := os.ReadFile(filepath.Join(rc.Dir, environments.AnswerFile)); string(b) != "42\n" {
		t.Fatalf("answer.txt %q", b)
	}
	rc.Task.Output.AnswerPath = "missing.txt"
	if a, err := d.ExportAnswer(t.Context(), rc); err != nil || a.Source != "missing" {
		t.Fatalf("missing answer: %+v %v", a, err)
	}
	rc.Task.Output.AnswerPath = "/etc/passwd"
	if _, err := d.ExportAnswer(t.Context(), rc); err == nil {
		t.Fatal("an answer path outside the workspace was read")
	}
}

func TestDriverExportArtifacts(t *testing.T) {
	f := newFakeAPI(t)
	f.workspace = t.TempDir()
	os.MkdirAll(filepath.Join(f.workspace, "out"), 0o755)
	os.WriteFile(filepath.Join(f.workspace, "out", "report.csv"), []byte("a,b\n1,2\n"), 0o644)
	os.Symlink("/etc/hostname", filepath.Join(f.workspace, "out", "leak.csv"))
	d := testDriver(t, f)
	rc := testRun(t)
	rc.Handle = &schemas.EnvironmentHandle{}
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "report", Path: "out/*.csv"}}}
	files, err := d.ExportArtifacts(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]schemas.ArtifactFile{}
	for _, f := range files {
		byName[f.Name] = f
	}
	if byName["report.csv"].Error != "" || byName["leak.csv"].Error == "" {
		t.Fatalf("artifacts: %+v", files)
	}
	if b, err := os.ReadFile(filepath.Join(rc.Dir, "artifacts", "report", "report.csv")); err != nil || string(b) != "a,b\n1,2\n" {
		t.Fatalf("copied file: %q %v", b, err)
	}
}

func makeTar(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	return buf.Bytes()
}

func tarList(t *testing.T, b []byte) []string {
	var out []string
	for name := range tarNames(t, b) {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// stagingDocker is a docker CLI for the staging volumes: volume and ps
// commands succeed, a staged volume read prints a tar holding one file named
// after the volume, and the log copy-back stores its stdin in a file.
func stagingDocker(t *testing.T) (utils.Docker, string) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ws.tar"), makeTar(t, map[string]string{"CLAUDE.md": "stale notes"}), 0o644)
	os.WriteFile(filepath.Join(dir, "aux.tar"), makeTar(t, map[string]string{"shim/pytest": "#!/bin/sh"}), 0o644)
	p := filepath.Join(dir, "docker")
	script := `#!/bin/sh
D=` + dir + `
case "$1" in
volume|ps) exit 0 ;;
image) echo '{"Env":["PATH=/usr/bin:/bin"]}'; exit 0 ;;
run)
  case "$*" in
  *bench-k8s-ws-*:/s:ro*) cat $D/ws.tar ;;
  *bench-k8s-aux-*:/s:ro*) cat $D/aux.tar ;;
  *"-i "*) cat > $D/log.tar ;;
  esac
  exit 0 ;;
esac
exit 1
`
	os.WriteFile(p, []byte(script), 0o755)
	return utils.Docker{Binary: p}, dir
}

func TestDriverStagesProbeFiles(t *testing.T) {
	f := newFakeAPI(t)
	d := testDriver(t, f)
	dk, dir := stagingDocker(t)
	d.cfg.Docker = dk
	rc := testRun(t)
	rc.Probe = &schemas.ProbeSpec{ID: "test-crash", Layer: schemas.LayerToolShim}
	h, err := d.Create(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if h.Volume != "bench-k8s-ws-r_test1" || h.AuxVolume != "bench-k8s-aux-r_test1" {
		t.Fatalf("staging volumes: %q %q", h.Volume, h.AuxVolume)
	}
	rc.Handle = h
	if _, err := d.Exec(t.Context(), rc, &schemas.RunPlan{Image: "img", Argv: []string{"true"}, Network: "none"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.laid, " ") != "/workspace:CLAUDE.md /opt/bench:shim/pytest" {
		t.Fatalf("staged files laid: %v", f.laid)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "log.tar"))
	if names := tarNames(t, b); !names["log/shim.jsonl"] {
		t.Fatalf("shim log not copied back: %v", names)
	}
	// The pod is released only after the staged files are in.
	last := -1
	for i, e := range f.execs {
		if strings.HasPrefix(e, "prep: touch") {
			last = i
		}
	}
	if last < 0 || !strings.HasPrefix(f.execs[last-1], "prep: tar -x") {
		t.Fatalf("release order: %v", f.execs)
	}
}

// TestOOMKilledReason checks the kubelet's OOMKilled reason marks an
// out-of-memory exit and nothing else does.
func TestOOMKilledReason(t *testing.T) {
	var st ContainerState
	if oomKilled(st) {
		t.Fatal("a running container is not out of memory")
	}
	st.Terminated = &struct {
		ExitCode   int    `json:"exitCode"`
		Signal     int    `json:"signal,omitempty"`
		Reason     string `json:"reason,omitempty"`
		Message    string `json:"message,omitempty"`
		StartedAt  string `json:"startedAt,omitempty"`
		FinishedAt string `json:"finishedAt,omitempty"`
	}{ExitCode: 137, Reason: "OOMKilled"}
	if !oomKilled(st) {
		t.Fatal("OOMKilled not detected")
	}
	st.Terminated.Reason = "Error"
	if oomKilled(st) {
		t.Fatal("exit 137 with reason Error taken for out of memory")
	}
}
