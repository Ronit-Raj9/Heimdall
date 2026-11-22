package builder

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// fakeDocker emulates the docker CLI on host folders: a container or image
// is a folder whose Workdir is the repository copy. exec runs the decoded
// command with bash in that folder (Workdir rewritten), cp copies folders
// and files, commit copies the container folder to a new image (or, with
// dropCommits, keeps only the image the container came from).
type fakeDocker struct {
	t           *testing.T
	root        string
	dropCommits bool
	refuse      string // a subcommand refused like a policy would

	mu         sync.Mutex
	calls      [][]string
	n          int
	containers map[string]*fakeCtr
	images     map[string]string // id -> folder
	files      map[string]map[string]string
}

type fakeCtr struct {
	dir, image string
	started    bool
	args       []string
}

func newFakeDocker(t *testing.T) *fakeDocker {
	return &fakeDocker{t: t, root: t.TempDir(), containers: map[string]*fakeCtr{}, images: map[string]string{}, files: map[string]map[string]string{}}
}

func (f *fakeDocker) calledWith(sub string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if c[0] == sub {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeDocker) subs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c[0])
	}
	return out
}

func (f *fakeDocker) imageDir(ref string) string {
	if d, ok := f.images[ref]; ok {
		return d
	}
	d := filepath.Join(f.root, "img-"+strings.NewReplacer(":", "_", "/", "_").Replace(ref))
	os.MkdirAll(d, 0o755)
	f.images[ref] = d
	return d
}

func (f *fakeDocker) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.mu.Unlock()
	if f.refuse != "" && args[0] == f.refuse {
		return nil, fmt.Errorf("refused on the shared worker: %s", args[0])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch args[0] {
	case "info":
		return []byte(`{"runc":{}}`), nil
	case "create", "run":
		// image is the arg before "sleep" or "cat"
		img := ""
		for i, a := range args {
			if (a == "sleep" || a == "cat") && i > 0 {
				img = args[i-1]
			}
		}
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("c%d", f.n)
		src := f.imageDir(img)
		dir := filepath.Join(f.root, id)
		f.mu.Unlock()
		if out, err := exec.Command("cp", "-a", src, dir).CombinedOutput(); err != nil {
			f.t.Fatalf("fake create: %v %s", err, out)
		}
		f.mu.Lock()
		f.containers[id] = &fakeCtr{dir: dir, image: img, args: args}
		f.mu.Unlock()
		if args[0] == "run" {
			b, err := os.ReadFile(filepath.Join(dir, "rootfs", args[len(args)-1]))
			if err != nil {
				return []byte("no such file"), &issuebench.ExitError{Code: 1, Output: []byte("no such file")}
			}
			return b, nil
		}
		return []byte(id + "\n"), nil
	case "start":
		f.mu.Lock()
		defer f.mu.Unlock()
		f.containers[args[1]].started = true
		return nil, nil
	case "cp":
		src, dst := args[1], args[2]
		id, p, _ := strings.Cut(dst, ":")
		f.mu.Lock()
		c := f.containers[id]
		f.mu.Unlock()
		target := filepath.Join(c.dir, "rootfs", filepath.FromSlash(p))
		if p == Workdir {
			target = filepath.Join(c.dir, "work")
		}
		var cmd *exec.Cmd
		if strings.HasSuffix(src, "/.") || strings.HasSuffix(p, "/") {
			os.MkdirAll(target, 0o755)
			cmd = exec.Command("cp", "-a", src, target+"/")
		} else if fi, err := os.Stat(target); err == nil && fi.IsDir() {
			cmd = exec.Command("cp", "-a", src, target+"/")
		} else {
			os.MkdirAll(filepath.Dir(target), 0o755)
			cmd = exec.Command("cp", "-a", src, target)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			f.t.Fatalf("fake cp: %v %s", err, out)
		}
		return nil, nil
	case "exec":
		f.mu.Lock()
		var c *fakeCtr
		for _, a := range args[1:] {
			if c = f.containers[a]; c != nil {
				break
			}
		}
		f.mu.Unlock()
		if c == nil || !c.started {
			return nil, &issuebench.ExitError{Code: 1, Output: []byte("container not running")}
		}
		script := args[len(args)-1]
		if strings.HasPrefix(script, "cat > /tmp/.hb_cmd") {
			script = string(stdin)
		} else if strings.HasPrefix(script, "echo ") && strings.Contains(script, "base64 -d") {
			enc := strings.Fields(script)[1]
			b, _ := base64.StdEncoding.DecodeString(enc)
			script = string(b)
		}
		work := filepath.Join(c.dir, "work")
		os.MkdirAll(work, 0o755)
		script = strings.ReplaceAll(script, Workdir, work)
		script = strings.ReplaceAll(script, "/tmp/.heimdall-snapshot-check", filepath.Join(c.dir, "rootfs", "tmp", ".heimdall-snapshot-check"))
		os.MkdirAll(filepath.Join(c.dir, "rootfs", "tmp"), 0o755)
		cmd := exec.CommandContext(ctx, "bash", "-c", script)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out, &issuebench.ExitError{Code: ee.ExitCode(), Output: out}
		}
		return out, err
	case "commit":
		f.mu.Lock()
		defer f.mu.Unlock()
		c := f.containers[args[1]]
		f.n++
		id := fmt.Sprintf("sha256:img%d", f.n)
		if f.dropCommits {
			f.images[id] = f.imageDir(c.image)
			return []byte(id + "\n"), nil
		}
		dir := filepath.Join(f.root, "img"+fmt.Sprint(f.n))
		if out, err := exec.Command("cp", "-a", c.dir, dir).CombinedOutput(); err != nil {
			f.t.Fatalf("fake commit: %v %s", err, out)
		}
		f.images[id] = dir
		return []byte(id + "\n"), nil
	case "rm", "rmi":
		return nil, nil
	}
	return nil, nil
}

var fakeSeq int
var fakeSeqMu sync.Mutex

// testConfig gives each test its own snapshot check cache key.
func testConfig(f *fakeDocker) DockerConfig {
	fakeSeqMu.Lock()
	fakeSeq++
	n := fakeSeq
	fakeSeqMu.Unlock()
	return DockerConfig{Docker: fmt.Sprintf("fake-docker-%d", n), Run: f.Run, Network: "heimdall-ib-test", Log: func(string, ...any) {}}
}

func repoDir(t *testing.T) string {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "hello.txt"), []byte("hi\n"), 0o644)
	return d
}

func TestSandboxCreateCopyStartOrder(t *testing.T) {
	f := newFakeDocker(t)
	cfg := testConfig(f)
	cfg.Labels = map[string]string{issuebench.LabelJob: "j1", "heimdall.runner": "r1"}
	cfg.ExtraEnv = []string{"HTTPS_PROXY=http://egress:3128"}
	cfg.Files = map[string][]byte{"/etc/heimdall-egress/ca.crt": []byte("CERT")}
	sb, err := DockerFactory(cfg)(context.Background(), "img:a", repoDir(t), []string{"UV_EXCLUDE_NEWER=x"})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(context.Background())
	// after the snapshot check (create, start, exec, commit, run, rmi, rm):
	subs := f.subs()
	var after []string
	for i, s := range subs {
		if s == "create" && i > 0 && subs[i-1] != "info" {
			after = subs[i:]
		}
	}
	want := []string{"create", "cp", "cp", "start", "exec"}
	if strings.Join(after, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v (all %v)", after, want, subs)
	}
	creates := f.calledWith("create")
	c := strings.Join(creates[len(creates)-1], " ")
	for _, w := range []string{"--network heimdall-ib-test", "-e UV_EXCLUDE_NEWER=x", "-e HTTPS_PROXY=http://egress:3128", "--label heimdall.issuebench=build", "--label heimdall.issuebench.job=j1", "--label heimdall.runner=r1", "--security-opt no-new-privileges", "img:a sleep infinity"} {
		if !strings.Contains(c, w) {
			t.Fatalf("create lacks %q: %s", w, c)
		}
	}
	if code, out, err := sb.Exec(context.Background(), "cat "+Workdir+"/hello.txt", time.Minute); err != nil || code != 0 || out != "hi\n" {
		t.Fatalf("exec %d %q %v", code, out, err)
	}
	ds := sb.(*dockerSandbox)
	f.mu.Lock()
	dir := f.containers[ds.id].dir
	f.mu.Unlock()
	if b, err := os.ReadFile(filepath.Join(dir, "rootfs", "etc", "heimdall-egress", "ca.crt")); err != nil || string(b) != "CERT" {
		t.Fatalf("file not copied: %q %v", b, err)
	}
}

func TestSandboxExtraEnvNeverInDockerfile(t *testing.T) {
	repo, c := fixture(t)
	f := newFakeDocker(t)
	cfg := testConfig(f)
	cfg.ExtraEnv = []string{"HTTPS_PROXY=http://egress:3128", "SSL_CERT_FILE=/etc/heimdall-egress/bundle.crt"}
	m := &ScriptedModel{Replies: []Reply{
		turn(Call("1", "bash", map[string]any{"cmd": "touch .installed"})),
		turn(Call("2", "run_tests", map[string]any{"state": "base+tests"})),
		turn(Call("3", "run_tests", map[string]any{"state": "gold"})),
		turn(Call("4", "submit", map[string]any{})),
	}}
	b := New(Config{Model: m, Sandbox: DockerFactory(cfg), LogPath: filepath.Join(t.TempDir(), "b.jsonl")})
	res := b.Build(context.Background(), Request{Repo: "acme/tiny", Version: "v1", RepoDir: repo, Candidates: []issuebench.Candidate{c}})
	if res.Stop != issuebench.StopOK || res.Env == nil {
		t.Fatalf("stop %s: %s", res.Stop, res.Detail)
	}
	df := res.Env.Dockerfile
	if strings.Contains(df, "PROXY") || strings.Contains(df, "egress") || strings.Contains(df, "SSL_CERT_FILE") {
		t.Fatalf("worker env leaked into the Dockerfile:\n%s", df)
	}
	if !strings.Contains(df, "RUN touch .installed") || !strings.Contains(df, "UV_EXCLUDE_NEWER") {
		t.Fatalf("dockerfile:\n%s", df)
	}
	seen := false
	for _, cr := range f.calledWith("create") {
		if strings.Contains(strings.Join(cr, " "), "-e HTTPS_PROXY=http://egress:3128") {
			seen = true
		}
	}
	if !seen {
		t.Fatal("ExtraEnv not given to the container")
	}
}

func TestSandboxReplayFallback(t *testing.T) {
	f := newFakeDocker(t)
	f.dropCommits = true
	cfg := testConfig(f)
	var logged []string
	cfg.Log = func(format string, a ...any) {
		logged = append(logged, fmt.Sprintf(format, a...))
		t.Log(fmt.Sprintf(format, a...))
	}
	ctx := context.Background()
	fac := DockerFactory(cfg)
	sb, err := fac(ctx, "img:a", repoDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	if len(logged) != 1 || !strings.Contains(logged[0], "does not keep changes on docker commit; the builder replays commands instead") {
		t.Fatalf("log %q", logged)
	}
	sb.Exec(ctx, "echo one > a.txt", time.Minute)
	id, err := sb.Snapshot(ctx)
	if err != nil || id != "replay:1" {
		t.Fatalf("snapshot %q %v", id, err)
	}
	sb.Exec(ctx, "echo two > b.txt", time.Minute)
	if err := sb.Rollback(ctx, id); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := sb.Exec(ctx, "test -f a.txt && ! test -f b.txt && test -f hello.txt", time.Minute); code != 0 {
		t.Fatal("replay did not restore the snapshot state")
	}
	if n := len(f.calledWith("commit")); n != 1 {
		t.Fatalf("commits %d (only the check should commit)", n)
	}
	// a second sandbox of the same (runtime, docker) does not check again
	sb2, err := fac(ctx, "img:a", repoDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	sb2.Close(ctx)
	if len(logged) != 1 || len(f.calledWith("commit")) != 1 {
		t.Fatalf("check ran again: log %d commits %d", len(logged), len(f.calledWith("commit")))
	}
}

func TestSandboxCommitSnapshotsWhenRuntimeKeepsChanges(t *testing.T) {
	f := newFakeDocker(t)
	cfg := testConfig(f)
	ctx := context.Background()
	sb, err := DockerFactory(cfg)(ctx, "img:a", repoDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	sb.Exec(ctx, "echo one > a.txt", time.Minute)
	id, err := sb.Snapshot(ctx)
	if err != nil || strings.HasPrefix(id, "replay:") {
		t.Fatalf("snapshot %q %v", id, err)
	}
	sb.Exec(ctx, "echo two > b.txt", time.Minute)
	if err := sb.Rollback(ctx, id); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := sb.Exec(ctx, "test -f a.txt && ! test -f b.txt", time.Minute); code != 0 {
		t.Fatal("rollback did not restore the commit")
	}
	subs := strings.Join(f.subs(), ",")
	if !strings.HasSuffix(subs, "commit,exec,rm,create,start,exec") {
		t.Fatalf("rollback calls: %s", subs)
	}
}

func TestSandboxEveryCallGoesThroughRun(t *testing.T) {
	f := newFakeDocker(t)
	f.refuse = "commit"
	cfg := testConfig(f)
	var logged int
	cfg.Log = func(string, ...any) { logged++ }
	ctx := context.Background()
	sb, err := DockerFactory(cfg)(ctx, "img:a", repoDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	// The refusing Run made the snapshot check fail: no commit ran outside it.
	if logged != 1 {
		t.Fatalf("a refused commit must switch to replay, logged %d", logged)
	}
	if id, _ := sb.Snapshot(ctx); !strings.HasPrefix(id, "replay:") {
		t.Fatalf("snapshot %q", id)
	}
	if len(f.calledWith("info")) != 1 {
		t.Fatal("runtime detection did not go through Run")
	}
}

func TestSandboxExecExitCodes(t *testing.T) {
	f := newFakeDocker(t)
	ctx := context.Background()
	sb, err := DockerFactory(testConfig(f))(ctx, "img:a", repoDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	if code, _, err := sb.Exec(ctx, "exit 3", time.Minute); code != 3 || err != nil {
		t.Fatalf("code %d err %v", code, err)
	}
	if code, out, err := sb.Exec(ctx, "sleep 5", 200*time.Millisecond); code != 124 || err != nil || !strings.Contains(out, "timed out") {
		t.Fatalf("timeout: %d %v %q", code, err, out)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := sb.Exec(cctx, "true", time.Minute); err == nil {
		t.Fatal("cancelled exec must return an error")
	}
}

func TestSandboxNeedsNetwork(t *testing.T) {
	cfg := testConfig(newFakeDocker(t))
	cfg.Network = ""
	if _, err := DockerFactory(cfg)(context.Background(), "img:a", t.TempDir(), nil); err == nil {
		t.Fatal("no network must refuse")
	}
}

func TestSandboxRealDocker(t *testing.T) {
	if os.Getenv("HEIMDALL_TEST_REAL_DOCKER") == "" {
		t.Skip("set HEIMDALL_TEST_REAL_DOCKER=1 (and HEIMDALL_TEST_IMAGE) for the real Docker test")
	}
	img := os.Getenv("HEIMDALL_TEST_IMAGE")
	if img == "" {
		img = "python:3.12-slim"
	}
	ctx := context.Background()
	netName := fmt.Sprintf("heimdall-ib-w04test-%d", os.Getpid())
	if out, err := exec.Command("docker", "network", "create", "--internal", "--label", "heimdall.issuebench=test", netName).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	defer exec.Command("docker", "network", "rm", netName).Run()
	cfg := DockerConfig{Docker: "docker", Network: netName, Labels: map[string]string{issuebench.LabelJob: "w04test"},
		ExtraEnv: []string{"HEIMDALL_EXTRA=1"}, Files: map[string][]byte{"/etc/heimdall-egress/ca.crt": []byte("CERT\n")}}
	sb, err := DockerFactory(cfg)(ctx, img, repoDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	code, out, err := sb.Exec(ctx, "cat hello.txt /etc/heimdall-egress/ca.crt && echo $HEIMDALL_EXTRA && stat -c %a / /etc", time.Minute)
	if err != nil || code != 0 || !strings.Contains(out, "hi\nCERT\n1\n755\n755") {
		t.Fatalf("exec %d %v %q", code, err, out)
	}
	sb.Exec(ctx, "echo one > a.txt", time.Minute)
	id, err := sb.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("snapshot %s", id)
	sb.Exec(ctx, "echo two > b.txt", time.Minute)
	if err := sb.Rollback(ctx, id); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := sb.Exec(ctx, "test -f a.txt && ! test -f b.txt && cat /etc/heimdall-egress/ca.crt", time.Minute); code != 0 {
		t.Fatalf("rollback state wrong: %q", out)
	}
}
