//go:build integration

package environments

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
