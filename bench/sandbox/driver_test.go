package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fakePlatform scripts a sandbox: it answers each command by what it runs.
type fakePlatform struct {
	mu        sync.Mutex
	spec      Spec
	created   int
	destroyed int
	files     map[string][]byte
	cmds      []string
	exitPolls int
	exitAfter int // exit file appears after this many polls; -1 never (until killed)
	killed    bool
	isolation string
	diff      string
}

func newFakePlatform() *fakePlatform {
	return &fakePlatform{files: map[string][]byte{}, exitAfter: 2, isolation: "blocked",
		diff: "diff --git a/f.txt b/f.txt\nnew file mode 100644\nindex 0000000..45b983b\n--- /dev/null\n+++ b/f.txt\n@@ -0,0 +1 @@\n+hi\n"}
}

func (f *fakePlatform) Name() string                { return "fake" }
func (f *fakePlatform) Check(context.Context) error { return nil }
func (f *fakePlatform) Destroy(_ context.Context, b *Box) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed++
	return nil
}

func (f *fakePlatform) Create(_ context.Context, s Spec) (*Box, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spec, f.created = s, f.created+1
	return &Box{ID: "box-1", Platform: "fake", Data: map[string]string{}}, nil
}

func (f *fakePlatform) Upload(_ context.Context, _ *Box, path string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = append([]byte(nil), data...)
	return nil
}

func (f *fakePlatform) Download(_ context.Context, _ *Box, path string, max int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch path {
	case stdoutFile + ".cap":
		return []byte("agent used sk-live-SECRETKEY\n"), nil
	case stderrFile + ".cap":
		return nil, nil
	case diffFile:
		return []byte(f.diff), nil
	}
	return f.files[path], nil
}

func (f *fakePlatform) Exec(_ context.Context, _ *Box, c Command) (*Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.Join(c.Argv, " ")
	f.cmds = append(f.cmds, cmd)
	switch {
	case strings.Contains(cmd, populateSh):
		return &Result{Output: []byte("Initialized\n0123456789abcdef0123456789abcdef01234567\n")}, nil
	case strings.Contains(cmd, "reach()"):
		return &Result{Output: []byte(f.isolation + "\n")}, nil
	case strings.Contains(cmd, "cat "+exitFile):
		f.exitPolls++
		if f.killed {
			return &Result{Output: []byte("137\n")}, nil
		}
		if f.exitAfter >= 0 && f.exitPolls > f.exitAfter {
			return &Result{Output: []byte("0\n")}, nil
		}
		return &Result{}, nil
	case strings.Contains(cmd, "kill -KILL"):
		f.killed = true
	}
	return &Result{}, nil
}

// fakeLocal is the local Docker driver's image metadata.
type localDriver = environments.Driver

type fakeLocal struct {
	localDriver
	digests []string
}

func (fakeLocal) GetName() string    { return environments.DockerWorkspaceType }
func (fakeLocal) GetVersion() string { return "9.9.9" }
func (fakeLocal) ImageID(context.Context, string) (string, error) {
	return "sha256:local", nil
}
func (l fakeLocal) ImageRepoDigests(context.Context, string) ([]string, error) { return l.digests, nil }

func testDriver(t *testing.T, p Platform) *Driver {
	t.Helper()
	d, err := NewDriver(DriverConfig{Platform: p, Local: fakeLocal{digests: []string{"other/repo@sha256:9", "reg.example/agent@sha256:1"}},
		AgentGatewayURL: "http://gateway:8080", PublicGatewayURL: "https://gw.example.com", PollEvery: time.Millisecond,
		ImageConfig: func(context.Context, string) (*ImageConfig, error) {
			return &ImageConfig{Entrypoint: []string{"/entry"}, Env: []string{"PATH=/usr/bin:/bin"}}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func testRun(t *testing.T) *schemas.RunContext {
	t.Helper()
	task := t.TempDir()
	if err := os.MkdirAll(filepath.Join(task, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task, "repo", "main.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &schemas.RunContext{Run: &schemas.Run{RunID: "r-0001", ExpID: "e1"}, Task: &schemas.Task{Dir: task, RepoDir: "repo"},
		Dir: t.TempDir(), Key: &schemas.RunKey{Value: "sk-live-SECRETKEY"}}
}

func testPlan() *schemas.RunPlan {
	return &schemas.RunPlan{Image: "reg.example/agent:1", Argv: []string{"/agent", "--task", "it's"}, User: "1000:1000", WallS: 60,
		Env: map[string]string{"OPENAI_BASE_URL": "http://gateway:8080/v1", "OPENAI_API_KEY": "sk-live-SECRETKEY"}}
}

func TestDriverRunsAPlanInASandbox(t *testing.T) {
	fp := newFakePlatform()
	d := testDriver(t, fp)
	rc := testRun(t)
	ctx := context.Background()
	h, err := d.Create(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if h.Network != "sandbox:fake@gw.example.com" || fp.created != 0 {
		t.Fatalf("handle = %+v, created %d", h, fp.created)
	}
	rc.Handle = h
	res, err := d.Exec(ctx, rc, testPlan())
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("result = %+v", res)
	}
	if fp.spec.Image != "reg.example/agent@sha256:1" || fp.spec.Egress.BlockAll || strings.Join(fp.spec.Egress.AllowHosts, ",") != "gw.example.com" {
		t.Fatalf("spec = %+v", fp.spec)
	}
	if fp.spec.Lifetime < time.Minute+setupWindow || fp.spec.Labels["heimdall.run"] != "r-0001" || fp.spec.Name != "heimdall-r-0001" {
		t.Fatalf("spec = %+v", fp.spec)
	}
	if _, ok := fp.spec.Env["OPENAI_API_KEY"]; ok {
		t.Fatal("secrets must not be set on the sandbox itself")
	}
	env := string(fp.files[agentEnv])
	if !strings.Contains(env, "OPENAI_BASE_URL=https://gw.example.com/v1\n") || !strings.Contains(env, "HOME=") {
		t.Fatalf("launch env = %q", env)
	}
	launch := string(fp.files[launchSh])
	if !strings.Contains(launch, `/agent --task 'it'\''s'`) || !strings.Contains(launch, "U=1000; G=1000") {
		t.Fatalf("launch script = %s", launch)
	}
	if rc.Handle.BaselineRef != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("baseline = %q", rc.Handle.BaselineRef)
	}
	out, _ := os.ReadFile(res.StdoutPath)
	if strings.Contains(string(out), "SECRETKEY") || !strings.Contains(string(out), "agent used") {
		t.Fatalf("stdout not redacted: %q", out)
	}
	var rec record
	b, _ := os.ReadFile(filepath.Join(rc.Dir, FileRecord))
	if err := json.Unmarshal(b, &rec); err != nil || rec.SandboxID != "box-1" || rec.Isolation != "blocked" || rec.Platform != "fake" {
		t.Fatalf("record = %s", b)
	}
	if strings.Contains(string(b), "SECRETKEY") {
		t.Fatal("record holds a secret")
	}
	diff, err := d.ExportDiff(ctx, rc)
	if err != nil || len(diff.Files) != 1 {
		t.Fatalf("diff = %+v, %v", diff, err)
	}
	// A new driver (a resumed runner) still finds the sandbox to delete.
	d2 := testDriver(t, fp)
	if err := d2.Destroy(ctx, rc); err != nil || fp.destroyed != 1 {
		t.Fatalf("destroy = %v, %d", err, fp.destroyed)
	}
	b, _ = os.ReadFile(filepath.Join(rc.Dir, FileRecord))
	if !strings.Contains(string(b), `"destroyed": true`) {
		t.Fatalf("record after destroy = %s", b)
	}
}

func TestDriverWallBudgetKillsAgent(t *testing.T) {
	fp := newFakePlatform()
	fp.exitAfter = -1
	d := testDriver(t, fp)
	rc := testRun(t)
	h, err := d.Create(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	rc.Handle = h
	plan := testPlan()
	plan.WallS = 1
	plan.Network = "none"
	res, err := d.Exec(context.Background(), rc, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.ExitCode != 137 || !fp.killed || !fp.spec.Egress.BlockAll {
		t.Fatalf("result = %+v, killed %v, egress %+v", res, fp.killed, fp.spec.Egress)
	}
}

func TestDriverRefusesOpenSandbox(t *testing.T) {
	fp := newFakePlatform()
	fp.isolation = "open"
	d := testDriver(t, fp)
	rc := testRun(t)
	rc.Handle, _ = d.Create(context.Background(), rc)
	if _, err := d.Exec(context.Background(), rc, testPlan()); err == nil || !strings.Contains(err.Error(), "reaches the internet") {
		t.Fatalf("open sandbox = %v", err)
	}
	if strings.Contains(strings.Join(fp.cmds, "\n"), "setsid") {
		t.Fatal("the agent started in a sandbox that reaches the internet")
	}
}

func TestDriverImageAndRefusals(t *testing.T) {
	d, _ := NewDriver(DriverConfig{Platform: newFakePlatform(), Local: fakeLocal{}})
	if _, err := d.resolveImage(context.Background(), nil, "local-only:1", &record{}); err == nil || !strings.Contains(err.Error(), "needs an image registry it can pull from") {
		t.Fatalf("local image = %v", err)
	}
	why := d.Refusals(environments.DriverSlot{Task: &schemas.Task{Sessions: []schemas.SessionSpec{{}}}, Probe: &schemas.ProbeSpec{Layer: schemas.LayerFixture},
		Agent: &schemas.AgentConfig{Adapter: "remote_http"}})
	got := strings.Join(why, "\n")
	for _, want := range []string{RefuseNoPublicGateway, RefuseSessions, RefuseRemote, "fault layer fixture"} {
		if !strings.Contains(got, want) {
			t.Fatalf("refusals %q miss %q", got, want)
		}
	}
	ok := testDriver(t, newFakePlatform())
	if why := ok.Refusals(environments.DriverSlot{Task: &schemas.Task{}, Probe: &schemas.ProbeSpec{Layer: schemas.LayerGateway}}); len(why) != 0 {
		t.Fatalf("a gateway fault on a repo task must run: %v", why)
	}
	if ok.PlansAs() != environments.DriverDocker || environments.PlanIdentity(ok) != environments.DriverDocker {
		t.Fatal("the sandbox driver must plan as docker")
	}
}

func TestHelpers(t *testing.T) {
	if u, err := parseUser("1000:1001"); err != nil || u.uid != 1000 || u.gid != 1001 {
		t.Fatalf("parseUser = %+v %v", u, err)
	}
	if _, err := parseUser("agent"); err == nil {
		t.Fatal("named user accepted")
	}
	if got := string(envFile(map[string]string{"B": "x y", "A": "1", "bad-name": "z"})); got != "A=1\nB='x y'\n" {
		t.Fatalf("envFile = %q", got)
	}
	if sandboxName("R_1.x") != "heimdall-r-1-x" {
		t.Fatal(sandboxName("R_1.x"))
	}
	ip := testDriver(t, newFakePlatform())
	ip.cfg.PublicGatewayURL = "http://203.0.113.5:8080"
	if eg, _ := ip.gatewayEgress(); strings.Join(eg.AllowCIDRs, ",") != "203.0.113.5/32" {
		t.Fatalf("ip egress = %+v", eg)
	}
}

func TestEgressVerdict(t *testing.T) {
	d := testDriver(t, newFakePlatform())
	all := func(v string) map[string]string {
		m := map[string]string{}
		for _, t := range d.probeTargets() {
			m[t.Name] = v
		}
		return m
	}
	good := all("blocked")
	good["gateway"] = "open"
	if s, _ := d.EgressVerdict(&EgressReport{Results: good}); s != EgressBlocked {
		t.Fatalf("verdict = %s", s)
	}
	if s, _ := d.EgressVerdict(&EgressReport{Results: all("blocked")}); s != EgressNotRun {
		t.Fatalf("no control = %s", s)
	}
	leak := all("open")
	if s, why := d.EgressVerdict(&EgressReport{Results: leak}); s != EgressOpen || !strings.Contains(why, "internet") {
		t.Fatalf("leak = %s %s", s, why)
	}
}
