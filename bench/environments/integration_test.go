//go:build integration

package environments

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const testImage = "bench/coding-core-base:1"

func TestMain(m *testing.M) {
	if err := ensureImage(); err != nil {
		os.Stderr.WriteString("integration: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// ensureImage builds the suite base image when it is missing.
func ensureImage() error {
	if exec.Command("docker", "image", "inspect", testImage).Run() == nil {
		return nil
	}
	out, err := exec.Command("docker", "build", "-t", testImage, "../benchmarks/coding-core/base/").CombinedOutput()
	if err != nil {
		return &buildError{out: string(out), err: err}
	}
	return nil
}

type buildError struct {
	out string
	err error
}

func (b *buildError) Error() string {
	return "build " + testImage + ": " + b.err.Error() + "\n" + b.out
}

// newRun returns a fresh environment and run context for the fixture task and
// registers a cleanup that destroys the run and checks nothing is left.
func newRun(t *testing.T, condition string, capBytes int64) (*dockerWorkspace, *schemas.RunContext) {
	t.Helper()
	taskDir, err := filepath.Abs("testdata/task")
	if err != nil {
		t.Fatal(err)
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{HelperImage: testImage, OutputCapBytes: capBytes})
	runID := strings.Replace(utils.NewID("r", 10), "r_", "r_it", 1)
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: "e_envit"},
		Slot:  &schemas.Slot{Condition: condition},
		Suite: &schemas.Suite{Version: "1.0.0", Workdir: "/workspace", BaseImage: schemas.ImageSpec{Tag: testImage}},
		Task: &schemas.Task{
			ID: "tcalc", Dir: taskDir, RepoDir: "repo",
			Notes:     schemas.TaskNotes{Clean: "fixtures/notes.clean.md", Stale: "fixtures/notes.stale.md"},
			Protected: []string{"tests/**"},
		},
		Agent: &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "CLAUDE.md"}},
		Dir:   t.TempDir(),
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if err := e.Destroy(ctx, rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		if err := e.Destroy(ctx, rc); err != nil {
			t.Errorf("second destroy: %v", err)
		}
		assertNoLeftovers(t, runID)
	})
	return e, rc
}

func assertNoLeftovers(t *testing.T, runID string) {
	t.Helper()
	filter := "label=" + utils.LabelRun + "=" + runID
	for _, args := range [][]string{{"ps", "-aq", "--filter", filter}, {"volume", "ls", "-q", "--filter", filter}} {
		out, err := exec.Command("docker", args...).Output()
		if err != nil {
			t.Fatalf("docker %v: %v", args, err)
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			t.Errorf("leftover docker objects for %s (%s): %s", runID, args[0], s)
		}
	}
}

func shPlan(script string, wallS int) *schemas.RunPlan {
	return &schemas.RunPlan{
		Image: testImage, Argv: []string{"sh", "-c", script}, Workdir: "/workspace",
		WallS: wallS, CPUs: 1, MemGB: 1, Pids: 128, Network: "none", User: "1000",
	}
}

const agentScript = `set -e
test "$(id -u)" = 1000
case "$PATH" in /opt/bench/shim:*) ;; *) echo "bad PATH $PATH" >&2; exit 9;; esac
if touch /usr/bench-probe 2>/dev/null; then echo "root fs is writable" >&2; exit 10; fi
test "$HOME" = /home/agent && touch "$HOME/ok"
test "$(cat /tmp/cfg/settings.json)" = '{"a": 1}'
test "$MULTI" = "$(printf 'l1\nl2')"
test "$SECRET" = s3cr3t
test -f /workspace/CLAUDE.md
test -d /opt/bench/shim -a -w /opt/bench/log
cd /workspace
sed -i 's/return a - b/return a + b/' calc/__init__.py
mkdir -p out && echo done > out/result.txt
ln -s /etc/passwd link
mkdir -p calc/__pycache__ && echo junk > calc/__pycache__/x.cpython-312.pyc
echo '{"type":"result","result":"done"}'
echo 'to stderr' >&2
`

func TestWorkspaceLifecycle(t *testing.T) {
	ctx := context.Background()
	e, rc := newRun(t, schemas.ConditionClean, 0)
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	notesSum, err := utils.SHA256File(filepath.Join(rc.Task.Dir, "fixtures/notes.clean.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !refRe.MatchString(h.BaselineRef) || h.FileHashes["CLAUDE.md"] != notesSum || len(h.Protected) != 1 || h.Protected["tests/test_calc.py"] == "" {
		t.Fatalf("handle = %+v", h)
	}
	plan := shPlan(agentScript, 60)
	plan.Env = map[string]string{"MULTI": "l1\nl2", "SECRET": "s3cr3t"}
	plan.Files = map[string]string{"/tmp/cfg/settings.json": `{"a": 1}`}
	res, err := e.Exec(ctx, rc, plan)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	stdout, stderr := readFile(t, res.StdoutPath), readFile(t, res.StderrPath)
	if res.ExitCode != 0 || res.TimedOut || res.Truncated {
		t.Fatalf("exec result %+v\nstdout: %s\nstderr: %s", res, stdout, stderr)
	}
	if !strings.Contains(string(stdout), `"result":"done"`) || !strings.Contains(string(stderr), "to stderr") {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
	assertContainerGone(t, agentContainer(rc.Run.RunID))

	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	wantFiles := []schemas.DiffFile{{Path: "calc/__init__.py", Status: StatusModified}, {Path: "out/result.txt", Status: StatusAdded}}
	if !reflect.DeepEqual(d.Files, wantFiles) {
		t.Fatalf("files = %+v", d.Files)
	}
	wantRejected := []schemas.DiffReject{{Path: "calc/__pycache__/x.cpython-312.pyc", Reason: RejectCache}, {Path: "link", Reason: RejectSymlink}}
	if !reflect.DeepEqual(d.Rejected, wantRejected) {
		t.Fatalf("rejected = %+v", d.Rejected)
	}
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != filepath.Join(rc.Dir, "workspace.diff") || utils.SHA256Bytes(raw) != d.SHA256 || !strings.Contains(string(raw), "+    return a + b") {
		t.Fatalf("diff %s sha %s:\n%s", d.Path, d.SHA256, raw)
	}
}

func TestBaselineIsDeterministic(t *testing.T) {
	ctx := context.Background()
	e1, rc1 := newRun(t, schemas.ConditionClean, 0)
	e2, rc2 := newRun(t, schemas.ConditionClean, 0)
	h1, err := e1.Create(ctx, rc1)
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	h2, err := e2.Create(ctx, rc2)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if h1.BaselineRef != h2.BaselineRef {
		t.Fatalf("baseline refs differ: %s vs %s", h1.BaselineRef, h2.BaselineRef)
	}
}

func TestExecTimeoutAndOutputCap(t *testing.T) {
	ctx := context.Background()
	e, rc := newRun(t, schemas.ConditionClean, 1000)
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	start := time.Now()
	res, err := e.Exec(ctx, rc, shPlan(`head -c 100000 /dev/zero | tr '\0' x; sleep 60`, 3))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !res.TimedOut || !res.Truncated || time.Since(start) > 30*time.Second {
		t.Fatalf("result %+v after %s", res, time.Since(start))
	}
	st, err := os.Stat(res.StdoutPath)
	if err != nil || st.Size() != 1000 {
		t.Fatalf("stdout size %v, %v", st, err)
	}
	assertContainerGone(t, agentContainer(rc.Run.RunID))
}

func TestStaleNotesSkipsCleanFile(t *testing.T) {
	ctx := context.Background()
	e, rc := newRun(t, "stale-notes", 0)
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	if len(h.FileHashes) != 0 {
		t.Fatalf("file hashes %v, want none", h.FileHashes)
	}
	res, err := e.Exec(ctx, rc, shPlan(`test ! -e /workspace/CLAUDE.md`, 30))
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("CLAUDE.md present under stale-notes: %+v %v", res, err)
	}
}

// TestExportIgnoresAgentGitState plants git state that would hide or rewrite
// a change if the export trusted the workspace's .git folder.
func TestExportIgnoresAgentGitState(t *testing.T) {
	ctx := context.Background()
	e, rc := newRun(t, schemas.ConditionClean, 0)
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	script := `set -e
cd /workspace
sed -i 's/return a - b/return a + b/' calc/__init__.py
git update-index --assume-unchanged calc/__init__.py
git config filter.evil.clean "cat /dev/null"
git config core.fsmonitor "echo pwned"
mkdir -p .git/info && echo '* filter=evil' > .git/info/attributes
git -c user.name=a -c user.email=a@b commit -q --allow-empty -m "agent"
mkdir -p sub && cd sub && git init -q && git -c user.name=a -c user.email=a@b commit -q --allow-empty -m x
`
	res, err := e.Exec(ctx, rc, shPlan(script, 60))
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v: %s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "+    return a + b") {
		t.Fatalf("change hidden from the export:\n%s", raw)
	}
	if !reflect.DeepEqual(d.Rejected, []schemas.DiffReject{{Path: "sub", Reason: RejectSubmodule}}) {
		t.Fatalf("rejected = %+v", d.Rejected)
	}
}

func TestExecRefusesRoot(t *testing.T) {
	e, rc := newRun(t, schemas.ConditionClean, 0)
	rc.Handle = &schemas.EnvironmentHandle{Volume: workspaceVolume(rc.Run.RunID), AuxVolume: auxVolume(rc.Run.RunID)}
	plan := shPlan("id", 10)
	plan.User = "0"
	if _, err := e.Exec(context.Background(), rc, plan); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("err = %v", err)
	}
}

func TestDestroyAfterFailedCreate(t *testing.T) {
	e, rc := newRun(t, schemas.ConditionClean, 0)
	rc.Task.RepoDir = "missing"
	if _, err := e.Create(context.Background(), rc); err == nil {
		t.Fatal("create succeeded on a missing repo")
	}
	rc.Task.RepoDir = "repo"
	// The helper fails after both volumes exist: the notes path runs through
	// a regular file. The cleanup's Destroy must still remove everything.
	rc.Agent.Notes.ProjectFile = "calc/__init__.py/CLAUDE.md"
	if _, err := e.Create(context.Background(), rc); err == nil {
		t.Fatal("create succeeded with an impossible notes path")
	}
	out, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "label="+utils.LabelRun+"="+rc.Run.RunID).Output()
	if err != nil || len(strings.Fields(string(out))) != 2 {
		t.Fatalf("want the 2 run volumes before destroy, got %q %v", out, err)
	}
}

func assertContainerGone(t *testing.T, name string) {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "name=^"+name+"$").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("container %s still exists", name)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

// TestCreateRefusesARebuiltImage checks that a run whose agent tag no longer
// names the image the manifest froze is refused at Create, before any volume
// exists, and that the frozen id passes.
func TestCreateRefusesARebuiltImage(t *testing.T) {
	ctx := context.Background()
	e, rc := newRun(t, "clean", 0)
	id, err := e.ImageID(ctx, testImage)
	if err != nil || !strings.HasPrefix(id, "sha256:") {
		t.Fatalf("image id %q %v", id, err)
	}
	rc.Agent.Image = testImage
	rc.Slot.Arm = "A"
	rc.Manifest = &schemas.Manifest{ExpID: "e_envit", Arms: map[string]schemas.ArmManifest{"A": {ImageID: "sha256:0000000000000000"}}}
	if _, err := e.Create(ctx, rc); err == nil || !strings.Contains(err.Error(), "was planned with 000000000000") {
		t.Fatalf("create with a moved tag: %v", err)
	}
	rc.Manifest.Arms["A"] = schemas.ArmManifest{ImageID: id}
	if _, err := e.Create(ctx, rc); err != nil {
		t.Fatalf("create with the frozen image: %v", err)
	}
	if _, err := InspectImage(ctx, e.cfg.Docker, "heimdall-test/no-such-image:0"); !errors.Is(err, ErrImageNotFound) {
		t.Fatalf("missing image: %v", err)
	}
}

// Scenario integration tests: services and the tool proxy, sessions with
// memory, snapshot grading and a task's own image. Every docker object they
// create carries a run id starting hd-env-, or a name with hd-env- in it.

// writeTree writes files (relative path -> content) under dir; a path ending
// in .sh is executable.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
}

// scenarioRun returns an environment and a run context for a task folder
// built from files, with a cleanup that destroys the run and checks that no
// container, volume, network or image of it is left.
func scenarioRun(t *testing.T, cfg DockerWorkspaceConfig, files map[string]string) (*dockerWorkspace, *schemas.RunContext) {
	t.Helper()
	taskDir := t.TempDir()
	writeTree(t, taskDir, files)
	if cfg.HelperImage == "" {
		cfg.HelperImage = testImage
	}
	e := newDockerWorkspace(cfg)
	runID := strings.Replace(utils.NewID("r", 8), "r_", "hd-env-", 1)
	rc := &schemas.RunContext{
		Run:   &schemas.Run{RunID: runID, ExpID: "e_hdenv"},
		Slot:  &schemas.Slot{Condition: schemas.ConditionClean},
		Suite: &schemas.Suite{ID: "hd-env-suite", Version: "1.0.0", Workdir: "/workspace", BaseImage: schemas.ImageSpec{Tag: testImage}, Root: taskDir},
		Task:  &schemas.Task{ID: "t1", Dir: taskDir, RepoDir: "repo", Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Agent: &schemas.AgentConfig{},
		Dir:   t.TempDir(),
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if err := e.Destroy(ctx, rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		if err := e.Destroy(ctx, rc); err != nil {
			t.Errorf("second destroy: %v", err)
		}
		assertNoLeftovers(t, runID)
		filter := "label=" + utils.LabelRun + "=" + runID
		for _, args := range [][]string{{"network", "ls", "-q", "--filter", filter}, {"image", "ls", "-q", "--filter", filter}} {
			out, err := exec.Command("docker", args...).Output()
			if err != nil || strings.TrimSpace(string(out)) != "" {
				t.Errorf("leftover %s for %s: %q %v", args[0], runID, out, err)
			}
		}
	})
	return e, rc
}

// gradeWith runs the named grader of the graders package on the run.
func gradeWith(t *testing.T, rc *schemas.RunContext, d *schemas.Diff) *schemas.Grade {
	t.Helper()
	for _, g := range graders.Builtin(graders.HiddenTestsConfig{Image: testImage}) {
		if g.GetName() != graders.VerifierCommandType {
			continue
		}
		grade, err := g.Grade(context.Background(), rc, d)
		if err != nil {
			t.Fatalf("grade: %v", err)
		}
		return grade
	}
	t.Fatal("verifier_command grader not registered")
	return nil
}

// fakeToolProxy builds a stand-in for the bench image: a bifrost-bench
// program that answers /healthz, answers 200 "pong" for any routed service
// and appends one ledger line per call, as the real tool proxy does.
func fakeToolProxy(t *testing.T) string {
	t.Helper()
	const tag = "hd-env-toolproxy:test"
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"Dockerfile": "FROM python:3.12-slim\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\nRUN chmod 755 /usr/local/bin/bifrost-bench\nUSER 10001:10001\n",
		"bifrost-bench": `#!/usr/local/bin/python3
import http.server, json, sys
a = sys.argv[1:]
opts = dict(zip(a[1::2], a[2::2]))
routes = {r["service"] for r in json.load(open(opts["-routes"]))["routes"]}
ledger = opts["-ledger"]
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        p = self.path.split("/")
        if self.path == "/healthz":
            self.send_response(200); self.end_headers(); return
        if len(p) > 2 and p[1] == "s" and p[2] in routes:
            with open(ledger, "a") as f:
                f.write(json.dumps({"seq": 1, "server": p[2], "tool": "GET /" + "/".join(p[3:]), "protocol": "http"}) + "\n")
            self.send_response(200); self.end_headers(); self.wfile.write(b"pong"); return
        self.send_response(404); self.end_headers()
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("", 9200), H).serve_forever()
`,
	})
	if out, err := exec.Command("docker", "build", "--network", "none", "-t", tag, dir).CombinedOutput(); err != nil {
		t.Fatalf("build fake tool proxy: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "image", "rm", "-f", tag).CombinedOutput(); err != nil {
			t.Errorf("remove %s: %v %s", tag, err, out)
		}
	})
	return tag
}

// kvServer is a data service: it seeds its state from /srv/seed.json, adds
// the MODE variable, and stores every /set query in /data/state.json.
const kvServer = `import http.server, json, os, urllib.parse
os.makedirs("/data", exist_ok=True)
state = json.load(open("/srv/seed.json"))
state["mode"] = os.environ.get("MODE", "")
def save():
    json.dump(state, open("/data/state.json", "w"), sort_keys=True)
save()
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        u = urllib.parse.urlparse(self.path)
        if u.path == "/healthz":
            self.send_response(200); self.end_headers(); return
        if u.path == "/set":
            state.update({k: v[0] for k, v in urllib.parse.parse_qs(u.query).items()}); save()
            self.send_response(200); self.end_headers(); self.wfile.write(b"ok"); return
        self.send_response(404); self.end_headers()
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("", 8080), H).serve_forever()
`

const serviceAgent = `set -e
python3 - <<'PY'
import socket, urllib.request
assert urllib.request.urlopen("http://kv:8080/set?answer=42", timeout=10).read() == b"ok"
assert urllib.request.urlopen("http://toolproxy:9200/s/echo/ping", timeout=10).read() == b"pong"
try:
    socket.gethostbyname("secret")
    raise SystemExit("hidden service resolved")
except socket.gaierror:
    pass
PY
echo 42 > /workspace/answer.txt
`

// TestServicesStateAndVerifier starts a data service (seeded file, probe
// variable, http ready check), a proxied tool service behind a stand-in tool
// proxy and a hidden service; the agent reaches the first two and not the
// third; the state, the ledger and the answer are exported; and a
// verifier_command grade reads /grader/state and /grader/answer.txt.
func TestServicesStateAndVerifier(t *testing.T) {
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{ToolProxyImage: fakeToolProxy(t)}, map[string]string{
		"repo/README.md":          "task\n",
		"svc/seed.json":           `{"seeded": "yes"}`,
		"verifier/tests/check.sh": "set -e\ngrep -q '\"answer\": \"42\"' /grader/state/kv/dump\ngrep -q '\"mode\": \"hostile\"' /grader/state/kv/dump\ntest -s /grader/state/kv/srv\ngrep -q 42 /grader/answer.txt\n",
	})
	rc.Suite.Grader = graders.VerifierCommandType
	rc.Suite.Verifier = schemas.SuiteVerifier{Command: []string{"sh", "/grader/tests/check.sh"}, Result: schemas.VerifierResultExitCode, TimeoutS: 60}
	rc.Task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	rc.Task.Services = []schemas.ServiceSpec{
		{ID: "kv", Role: schemas.ServiceRoleData, Image: "python:3.12-slim", Argv: []string{"python3", "-c", kvServer}, Port: 8080,
			Files: map[string]string{"/srv/seed.json": "svc/seed.json"}, Env: map[string]string{"MODE": "clean"},
			Ready:  &schemas.ReadyCheck{HTTP: "/healthz", TimeoutS: 30},
			Export: []schemas.StateExport{{ID: "dump", Argv: []string{"cat", "/data/state.json"}}, {ID: "srv", Path: "/srv"}}},
		{ID: "echo", Role: schemas.ServiceRoleTool, Image: "python:3.12-slim", Argv: []string{"python3", "-m", "http.server", "8081"}, Port: 8081},
		{ID: "secret", Role: schemas.ServiceRoleData, Image: "python:3.12-slim", Argv: []string{"python3", "-m", "http.server", "8082"}, Port: 8082, Hidden: true},
	}
	rc.Probe = &schemas.ProbeSpec{ID: "hostile-data", Layer: schemas.LayerService,
		Apply: schemas.ProbeApply{ServiceEnv: map[string]map[string]string{"kv": {"MODE": "hostile"}}}}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	handles, results, err := e.StartServices(ctx, rc)
	if err != nil {
		t.Fatalf("start services: %v (results %+v)", err, results)
	}
	rc.Services = handles
	urls := map[string]string{}
	for i, r := range results {
		if !r.Ready || r.ImageID == "" {
			t.Fatalf("result %+v", r)
		}
		urls[handles[i].Spec.ID] = handles[i].URL
	}
	want := map[string]string{"kv": "http://kv:8080", "echo": "http://toolproxy:9200/s/echo", "secret": ""}
	if !reflect.DeepEqual(urls, want) || !results[1].Proxied || results[0].Proxied {
		t.Fatalf("urls %v results %+v", urls, results)
	}
	res, err := e.Exec(ctx, rc, shPlan(serviceAgent, 60))
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v\n%s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	files, err := e.ExportState(ctx, rc)
	if err != nil || len(files) != 2 || files[0].Error != "" || files[1].Error != "" {
		t.Fatalf("export state: %+v %v", files, err)
	}
	dump := readFile(t, filepath.Join(rc.Dir, "state", "kv", "dump"))
	if files[0].SHA256 != utils.SHA256Bytes(dump) || !strings.Contains(string(dump), `"seeded": "yes"`) {
		t.Fatalf("dump %s / %+v", dump, files[0])
	}
	if ledger := readFile(t, filepath.Join(rc.Dir, ToolsFile)); !strings.Contains(string(ledger), `"server": "echo"`) {
		t.Fatalf("ledger %s", ledger)
	}
	a, err := e.ExportAnswer(ctx, rc)
	if err != nil || a.Source != "file" || a.Text != "42\n" {
		t.Fatalf("answer %+v %v", a, err)
	}
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export diff: %v", err)
	}
	if g := gradeWith(t, rc, d); !g.Passed {
		t.Fatalf("grade %+v", g)
	}
}

// TestSessionsMemoryAndReset runs three sessions: the second resets the
// workspace and still finds /memory and the agent's memory path, the third
// keeps the workspace, and the final diff holds only the third session's
// change.
func TestSessionsMemoryAndReset(t *testing.T) {
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{}, map[string]string{
		"repo/calc.py":        "def add(a, b):\n    return a - b\n",
		"sess1/s1.txt":        "one\n",
		"sess2/s2.txt":        "two\n",
		"verifier/tests/x.sh": "true\n",
	})
	rc.Task.Sessions = []schemas.SessionSpec{{ID: "s1", Files: "sess1"}, {ID: "s2", Files: "sess2"}, {ID: "s3", KeepWorkspace: true}}
	rc.Agent.Memory = &schemas.AgentMemory{Paths: []string{"/home/agent/.mem"}}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	scripts := []string{
		`set -e; cd /workspace; test -f s1.txt; test ! -e s2.txt; test -z "$(ls -A /memory)"; echo m1 > /memory/note; echo p1 > /home/agent/.mem/x; echo junk > ws1.txt; sed -i 's/a - b/a + b/' calc.py; echo s1`,
		`set -e; cd /workspace; test "$(cat /memory/note)" = m1; test "$(cat /home/agent/.mem/x)" = p1; test ! -e ws1.txt; test ! -e s1.txt; test -f s2.txt; grep -q 'a - b' calc.py; echo two > ws2.txt; echo s2`,
		`set -e; cd /workspace; test -f ws2.txt; test -f s2.txt; echo three > ws3.txt; echo s3`,
	}
	for i := range rc.Task.Sessions {
		if err := e.BeginSession(ctx, rc, i); err != nil {
			t.Fatalf("begin session %d: %v", i, err)
		}
		rc.Session, rc.SessionIndex = &rc.Task.Sessions[i], i
		res, err := e.Exec(ctx, rc, shPlan(scripts[i], 60))
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("session %d: %+v %v\n%s", i+1, res, err, readFile(t, res.StderrPath))
		}
		if filepath.Base(res.StdoutPath) != "agent.stream."+strconv.Itoa(i+1)+".jsonl" {
			t.Fatalf("stdout path %s", res.StdoutPath)
		}
		assertContainerGone(t, agentContainer(rc.Run.RunID)+"-s"+strconv.Itoa(i+1))
	}
	if got := string(readFile(t, filepath.Join(rc.Dir, "agent.stream.jsonl"))); got != "s1\ns2\ns3\n" {
		t.Fatalf("concatenated stream %q", got)
	}
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export diff: %v", err)
	}
	if want := []schemas.DiffFile{{Path: "ws3.txt", Status: StatusAdded}}; !reflect.DeepEqual(d.Files, want) {
		t.Fatalf("diff files %+v", d.Files)
	}
}

// TestSnapshotGradeSeesFileOutsideWorkspace freezes an agent (uid 1000, the
// default) that wrote a file outside the workspace and grades the snapshot:
// the verifier sees the file and the workspace, not the agent's secret
// variable, and may write into its working folder, a copy: the run's
// workspace volume stays as the agent left it.
func TestSnapshotGradeSeesFileOutsideWorkspace(t *testing.T) {
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{}, map[string]string{
		"repo/README.md":          "task\n",
		"verifier/tests/check.sh": "set -e\ntest \"$(cat /tmp/outside.txt)\" = out\ntest -f /workspace/in.txt\ntest -z \"$SECRET\"\necho v > /workspace/verifier-wrote.txt\n",
	})
	rc.Suite.Verifier = schemas.SuiteVerifier{Command: []string{"sh", "/grader/tests/check.sh"}, Result: schemas.VerifierResultExitCode, TimeoutS: 60, Mode: schemas.VerifierSnapshot}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	plan := shPlan(`set -e; test "$(id -u)" = 1000; echo out > /tmp/outside.txt; echo in > /workspace/in.txt`, 60)
	plan.Env = map[string]string{"SECRET": "s3cr3t"}
	if res, err := e.Exec(ctx, rc, plan); err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v", res, err)
	}
	if plan.User != "1000" {
		t.Fatalf("default task plan user %q", plan.User)
	}
	ref, err := e.Snapshot(ctx, rc)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := strings.TrimSpace(string(readFile(t, filepath.Join(rc.Dir, SnapshotRefFile)))); got != ref {
		t.Fatalf("snapshot.ref %q, want %q", got, ref)
	}
	assertContainerGone(t, agentContainer(rc.Run.RunID))
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export diff: %v", err)
	}
	g := gradeWith(t, rc, d)
	if !g.Passed || g.Mode != schemas.VerifierSnapshot {
		t.Fatalf("grade %+v", g)
	}
	out, err := exec.Command("docker", "run", "--rm", "--network", "none", "-v", h.Volume+":/w:ro", "--entrypoint", "sh", testImage,
		"-c", "test -f /w/in.txt && test ! -e /w/verifier-wrote.txt").CombinedOutput()
	if err != nil {
		t.Fatalf("run workspace changed by the verifier: %v %s", err, out)
	}
}

// TestRootTaskSnapshot runs a task with its own image and environment.user
// root: the agent is root and writes under /etc, the plan records the user,
// and the snapshot verifier, also root, sees the file and writes to /root.
func TestRootTaskSnapshot(t *testing.T) {
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{}, map[string]string{
		"repo/README.md":          "task\n",
		"verifier/tests/check.sh": "set -e\ntest \"$(id -u)\" = 0\ntest \"$(cat /etc/hd-env-marker)\" = hd\necho x > /root/verifier.txt\necho y > /workspace/v.txt\n",
	})
	rc.Task.Environment = &schemas.TaskEnvironment{Image: "python:3.12-slim", User: "root"}
	rc.Suite.Verifier = schemas.SuiteVerifier{Command: []string{"sh", "/grader/tests/check.sh"}, Result: schemas.VerifierResultExitCode, TimeoutS: 60, Mode: schemas.VerifierSnapshot}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	plan := shPlan(`set -e; test "$(id -u)" = 0; echo hd > /etc/hd-env-marker; echo in > /workspace/in.txt`, 60)
	plan.Image = "python:3.12-slim"
	if res, err := e.Exec(ctx, rc, plan); err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v\n%s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	if plan.User != rootUser {
		t.Fatalf("plan user %q, want %q", plan.User, rootUser)
	}
	if _, err := e.Snapshot(ctx, rc); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export diff: %v", err)
	}
	if g := gradeWith(t, rc, d); !g.Passed {
		t.Fatalf("grade %+v", g)
	}
}

// TestTaskDockerfileImage builds a task's own image, seeds the workspace from
// its /app folder, runs the agent in it, and grades clean-room in the task
// image with the pristine copy taken from the image.
func TestTaskDockerfileImage(t *testing.T) {
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{}, map[string]string{
		"environment/Dockerfile":  "FROM python:3.12-slim\nRUN mkdir -p /app && printf 'v1\\n' > /app/hello.txt\nWORKDIR /app\n",
		"verifier/tests/check.sh": "set -e\ntest \"$(cat /app/hello.txt)\" = v2\n",
	})
	rc.Task.ID, rc.Task.RepoDir = "timg", ""
	rc.Task.Environment = &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile", WorkspacePath: "/app"}
	rc.Task.Protected = []string{"hello.txt"}
	rc.Suite.Verifier = schemas.SuiteVerifier{Command: []string{"sh", "/grader/tests/check.sh"}, Result: schemas.VerifierResultExitCode, TimeoutS: 60}
	tag, id, err := EnsureTaskImage(ctx, utils.Docker{}, rc.Suite, rc.Task, nil)
	if err != nil || !strings.HasPrefix(tag, "bench/task-hd-env-suite-timg:") || !strings.HasPrefix(id, "sha256:") {
		t.Fatalf("ensure task image: %q %q %v", tag, id, err)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "image", "rm", "-f", tag).CombinedOutput(); err != nil {
			t.Errorf("remove %s: %v %s", tag, err, out)
		}
	})
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	if h.Workdir != "/app" || h.Protected["hello.txt"] != utils.SHA256Bytes([]byte("v1\n")) {
		t.Fatalf("handle %+v", h)
	}
	plan := shPlan(`set -e; test "$(cat /app/hello.txt)" = v1; test -f /workspace/hello.txt; echo v2 > /app/hello.txt`, 60)
	plan.Image, plan.Workdir = tag, "/app"
	if res, err := e.Exec(ctx, rc, plan); err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v\n%s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	d, err := e.ExportDiff(ctx, rc)
	if err != nil {
		t.Fatalf("export diff: %v", err)
	}
	if want := []schemas.DiffFile{{Path: "hello.txt", Status: StatusModified}}; !reflect.DeepEqual(d.Files, want) {
		t.Fatalf("diff files %+v", d.Files)
	}
	if g := gradeWith(t, rc, d); !g.Passed {
		t.Fatalf("grade %+v", g)
	}
}

// TestDirsAndMemoryUnderHome installs a plan folder and a memory path under
// the agent's home: both arrive through links, so the folders above them
// stay the agent's and the agent writes its own config beside them, while
// the plan folder stays read-only and the memory path writable.
func TestDirsAndMemoryUnderHome(t *testing.T) {
	ctx := context.Background()
	e, rc := scenarioRun(t, DockerWorkspaceConfig{}, map[string]string{"repo/README.md": "task\n", "verifier/tests/x.sh": "true\n"})
	skills := t.TempDir()
	writeTree(t, skills, map[string]string{"SKILL.md": "skill"})
	rc.Agent.Memory = &schemas.AgentMemory{Paths: []string{"/home/agent/.claude/projects"}}
	h, err := e.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	plan := shPlan(`set -e
test "$(cat /home/agent/.claude/skills/pdf/SKILL.md)" = skill
test "$(cat /home/agent/.codex/config.toml)" = "x = 1"
echo '{}' > /home/agent/.claude/settings.json
echo '{}' > /home/agent/.claude.json
mkdir -p /home/agent/.claude/skills/.system
test "$(stat -c %u /home/agent/.claude)" = 1000
if touch /home/agent/.claude/skills/pdf/x 2>/dev/null; then echo "plan folder is writable" >&2; exit 7; fi
echo mem > /home/agent/.claude/projects/note
test "$(cat /home/agent/.claude/projects/note)" = mem
`, 60)
	plan.Dirs = map[string]string{"/home/agent/.claude/skills/pdf": skills}
	plan.Files = map[string]string{"/home/agent/.codex/config.toml": "x = 1"}
	res, err := e.Exec(ctx, rc, plan)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("exec: %+v %v\n%s", res, err, readFile(t, filepath.Join(rc.Dir, "agent.stderr.txt")))
	}
	res, err = e.Exec(ctx, rc, shPlan(`test "$(cat /home/agent/.claude/projects/note)" = mem`, 30))
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("memory path not kept: %+v %v", res, err)
	}
}
