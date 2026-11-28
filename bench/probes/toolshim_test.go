package probes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const testNonce = "nonce_testabc123"

func TestRenderShimConfig(t *testing.T) {
	cases := []struct {
		name string
		spec schemas.ShimSpec
		want []string
		err  string
	}{
		{
			name: "test-crash probe",
			spec: schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 2, ExitCode: 139},
			want: []string{"tool=pytest\n", "mode=crash\n", "calls=2\n", "exit_code=139\n", "nonce=" + testNonce + "\n",
				`output=Fatal Python error: Segmentation fault\n\nCurrent thread`, `\nref: ` + testNonce + `\nSegmentation fault (core dumped)` + "\n"},
		},
		{
			name: "empty-test-run probe",
			spec: schemas.ShimSpec{Tool: "pytest", Mode: "empty", Calls: 1, ExitCode: 0, Output: "collected 0 items"},
			want: []string{"mode=empty\n", "calls=1\n", "exit_code=0\n",
				`output============================== test session starts ==============================\ncollected 0 items\nref: ` + testNonce + `\n\n============================ no tests ran in 0.01s =============================` + "\n"},
		},
		{
			name: "empty mode ignores exit code",
			spec: schemas.ShimSpec{Tool: "pytest", Mode: "empty", Calls: 1, ExitCode: 5},
			want: []string{"exit_code=0\n", `\ncollected 0 items\n`},
		},
		{
			name: "custom crash output keeps backslashes",
			spec: schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 1, ExitCode: 134, Output: "Aborted\nC:\\path\n"},
			want: []string{`output=Aborted\nC:\\path\nref: ` + testNonce},
		},
		{name: "other tool", spec: schemas.ShimSpec{Tool: "npm", Mode: "crash", Calls: 1, ExitCode: 1}, err: "not supported"},
		{name: "bad mode", spec: schemas.ShimSpec{Tool: "pytest", Mode: "hang", Calls: 1, ExitCode: 1}, err: "not crash or empty"},
		{name: "zero calls", spec: schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 0, ExitCode: 1}, err: "at least 1"},
		{name: "crash exit 0", spec: schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 1}, err: "non-zero"},
		{name: "exit out of range", spec: schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 1, ExitCode: 300}, err: "0..255"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := renderShimConfig(c.spec, testNonce)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want containing %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("config lacks %q:\n%s", w, got)
				}
			}
			if strings.Count(got, "\n") != 7 {
				t.Errorf("config must be 7 lines, got:\n%s", got)
			}
		})
	}
	if _, err := renderShimConfig(schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 1, ExitCode: 1}, "bad nonce;rm"); err == nil {
		t.Fatal("want error for a non-alphanumeric nonce")
	}
}

func TestParseShimLog(t *testing.T) {
	log := `{"tool":"pytest","argv":"pytest -q","call":1,"action":"crash","nonce":"n1","at":"2026-11-14T00:00:00Z"}
not json
{"tool":"pytest","argv":"python3 -m pytest","call":2,"action":"pass","nonce":"n1","at":"2026-11-14T00:00:01Z"}

{"tool":"pytest"}
`
	calls, bad := parseShimLog([]byte(log))
	if len(calls) != 2 || bad != 2 {
		t.Fatalf("calls=%v bad=%d", calls, bad)
	}
	if calls[0].Argv != "pytest -q" || calls[1].Call != 2 || calls[1].Action != "pass" {
		t.Fatalf("calls %+v", calls)
	}
	if calls, bad := parseShimLog(nil); len(calls) != 0 || calls == nil || bad != 0 {
		t.Fatalf("empty log: %v %d", calls, bad)
	}
}

func TestFiredEntry(t *testing.T) {
	calls := []schemas.ShimCall{
		{Call: 2, Action: "crash", Nonce: "n1"},
		{Call: 1, Action: "crash", Nonce: "n1"},
		{Call: 3, Action: "pass", Nonce: "n1"},
		{Call: 9, Action: "crash", Nonce: "forged"},
	}
	cases := []struct {
		name   string
		calls  []schemas.ShimCall
		nonce  string
		state  schemas.StepState
		detail string
	}{
		{"counts broken calls with the nonce", calls, "n1", schemas.StepYes, "2"},
		{"without the nonce counts every broken call", calls, "", schemas.StepYes, "3"},
		{"only pass-through", []schemas.ShimCall{{Call: 1, Action: "pass"}}, "n1", schemas.StepNo, "0"},
		{"never ran", nil, "n1", schemas.StepNo, "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firedEntry(c.calls, c.nonce)
			if got.State != c.state || got.Detail != c.detail {
				t.Fatalf("got %+v, want %s %s", got, c.state, c.detail)
			}
		})
	}
	if got := firedEntry(calls, "n1"); strings.Join(got.Evidence, ",") != "shim.jsonl:call=1,shim.jsonl:call=2" {
		t.Fatalf("evidence %v", got.Evidence)
	}
}

func TestEscapePrintfB(t *testing.T) {
	if got := escapePrintfB("a\\b\nc\r\x00"); got != `a\\b\nc` {
		t.Fatalf("got %q", got)
	}
}

// shimEnv lays out an aux root (shim/ with the embedded scripts and the
// rendered config, log/) and a bin folder with fake real tools, and returns
// the root and the PATH to run with.
func shimEnv(t *testing.T, spec *schemas.ShimSpec) (string, string) {
	t.Helper()
	for _, tool := range []string{"sh", "sed", "tr", "date", "dirname", "mkdir", "rmdir"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	root := t.TempDir()
	config := ""
	if spec != nil {
		var err error
		if config, err = renderShimConfig(*spec, testNonce); err != nil {
			t.Fatal(err)
		}
	}
	files, err := shimPayload(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"shim", "log", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		if f.Name == "config" && spec == nil {
			continue
		}
		mode := os.FileMode(0o755)
		if f.Mode == "0644" {
			mode = 0o644
		}
		if err := os.WriteFile(filepath.Join(root, "shim", f.Name), f.Data, mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"pytest", "py.test", "python", "python3"} {
		writeExec(t, filepath.Join(root, "bin", name), "#!/bin/sh\nprintf '%s\\n' \"REAL "+name+" $*\"\nexit 7\n")
	}
	sysPath := os.Getenv("PATH")
	return root, filepath.Join(root, "shim") + ":" + filepath.Join(root, "bin") + ":" + sysPath
}

type shimRun struct {
	stdout, stderr string
	code           int
}

func runShim(t *testing.T, root, pathEnv, tool string, args ...string) shimRun {
	t.Helper()
	cmd := exec.Command(filepath.Join(root, "shim", tool), args...)
	cmd.Env = []string{"PATH=" + pathEnv, "HOME=" + root}
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return shimRun{out.String(), errb.String(), code}
}

func readShimLog(t *testing.T, root string) []schemas.ShimCall {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "log", "shim.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	calls, bad := parseShimLog(b)
	if bad != 0 {
		t.Fatalf("%d malformed log lines:\n%s", bad, b)
	}
	return calls
}

func TestShimCrash(t *testing.T) {
	root, pathEnv := shimEnv(t, &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 2, ExitCode: 139})
	steps := []struct {
		tool       string
		args       []string
		code       int
		stdout     string
		stderrHas  []string
		wantAction string
	}{
		{"pytest", []string{"-q", "tests"}, 139, "", []string{"Fatal Python error: Segmentation fault", "ref: " + testNonce, "Segmentation fault (core dumped)"}, "crash"},
		{"python3", []string{"-m", "pytest", "-x"}, 139, "", []string{"Segmentation fault"}, "crash"},
		{"pytest", []string{"-q", `say "hi"\now`}, 7, "REAL pytest -q say \"hi\"\\now\n", nil, "pass"},
		{"py.test", nil, 7, "REAL py.test \n", nil, "pass"},
	}
	for i, s := range steps {
		got := runShim(t, root, pathEnv, s.tool, s.args...)
		if got.code != s.code || got.stdout != s.stdout {
			t.Fatalf("call %d: code=%d stdout=%q stderr=%q, want %d %q", i+1, got.code, got.stdout, got.stderr, s.code, s.stdout)
		}
		for _, w := range s.stderrHas {
			if !strings.Contains(got.stderr, w) {
				t.Fatalf("call %d: stderr %q lacks %q", i+1, got.stderr, w)
			}
		}
	}
	calls := readShimLog(t, root)
	if len(calls) != len(steps) {
		t.Fatalf("log has %d lines, want %d: %+v", len(calls), len(steps), calls)
	}
	for i, c := range calls {
		if c.Call != i+1 || c.Action != steps[i].wantAction || c.Nonce != testNonce || c.Tool != "pytest" || c.At == "" {
			t.Fatalf("log line %d: %+v", i, c)
		}
	}
	if calls[1].Argv != "python3 -m pytest -x" || calls[2].Argv != `pytest -q say "hi"\now` {
		t.Fatalf("argv not logged faithfully: %q %q", calls[1].Argv, calls[2].Argv)
	}
	if b, err := os.ReadFile(filepath.Join(root, "log", "pytest.count")); err != nil || strings.TrimSpace(string(b)) != "4" {
		t.Fatalf("counter %q %v", b, err)
	}
	if got := firedEntry(calls, testNonce); got.Detail != "2" {
		t.Fatalf("fired %+v", got)
	}
}

func TestShimEmpty(t *testing.T) {
	root, pathEnv := shimEnv(t, &schemas.ShimSpec{Tool: "pytest", Mode: "empty", Calls: 1, Output: "collected 0 items"})
	first := runShim(t, root, pathEnv, "python", "-Im", "pytest", "-q")
	if first.code != 0 || first.stderr != "" {
		t.Fatalf("first call: %+v", first)
	}
	for _, w := range []string{"test session starts", "collected 0 items", "ref: " + testNonce, "no tests ran in 0.01s"} {
		if !strings.Contains(first.stdout, w) {
			t.Fatalf("stdout %q lacks %q", first.stdout, w)
		}
	}
	second := runShim(t, root, pathEnv, "pytest", "-q")
	if second.code != 7 || second.stdout != "REAL pytest -q\n" {
		t.Fatalf("second call: %+v", second)
	}
	calls := readShimLog(t, root)
	if len(calls) != 2 || calls[0].Action != "empty" || calls[1].Action != "pass" {
		t.Fatalf("log %+v", calls)
	}
}

func TestShimPythonPassThrough(t *testing.T) {
	root, pathEnv := shimEnv(t, &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 5, ExitCode: 139})
	cases := []struct {
		tool string
		args []string
	}{
		{"python3", []string{"script.py", "-m", "pytest"}},
		{"python3", []string{"-c", "import pytest", "-m", "pytest"}},
		{"python", []string{"-Bc", "print(1)"}},
		{"python3", []string{"-m", "pip", "list"}},
		{"python3", []string{"-W", "ignore", "-X", "dev", "tool.py"}},
		{"python3", []string{"--version"}},
		{"python3", nil},
	}
	for _, c := range cases {
		t.Run(strings.Join(append([]string{c.tool}, c.args...), " "), func(t *testing.T) {
			got := runShim(t, root, pathEnv, c.tool, c.args...)
			want := "REAL " + c.tool + " " + strings.Join(c.args, " ") + "\n"
			if got.code != 7 || got.stdout != want {
				t.Fatalf("got %+v, want exit 7 %q", got, want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "log", "shim.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("non-pytest python calls must not be logged: %v", err)
	}
	intercepted := []struct {
		tool string
		args []string
	}{
		{"python3", []string{"-W", "ignore", "-m", "pytest"}},
		{"python3", []string{"-Xdev", "-mpytest"}},
		{"python", []string{"-I", "-B", "-m", "py.test", "-q"}},
	}
	for _, c := range intercepted {
		t.Run("intercept "+strings.Join(c.args, " "), func(t *testing.T) {
			if got := runShim(t, root, pathEnv, c.tool, c.args...); got.code != 139 {
				t.Fatalf("got %+v, want the crash", got)
			}
		})
	}
}

func TestShimWithoutConfigPassesThrough(t *testing.T) {
	root, pathEnv := shimEnv(t, nil)
	got := runShim(t, root, pathEnv, "pytest", "-q")
	if got.code != 7 || got.stdout != "REAL pytest -q\n" {
		t.Fatalf("got %+v", got)
	}
	calls := readShimLog(t, root)
	if len(calls) != 1 || calls[0].Action != "pass" || calls[0].Nonce != "" {
		t.Fatalf("log %+v", calls)
	}
}

func TestShimRealToolMissing(t *testing.T) {
	root, pathEnv := shimEnv(t, &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 1, ExitCode: 139})
	if err := os.Remove(filepath.Join(root, "bin", "py.test")); err != nil {
		t.Fatal(err)
	}
	// Keep only the shim and fake bin so a system py.test is not found.
	dirnamePath, err := exec.LookPath("dirname")
	if err != nil {
		t.Fatalf("find dirname: %v", err)
	}
	pathEnv = filepath.Join(root, "shim") + ":" + filepath.Join(root, "bin") + ":" + filepath.Dir(dirnamePath)
	for _, d := range []string{"/usr/bin", "/bin"} {
		if _, err := os.Stat(filepath.Join(d, "py.test")); err == nil {
			t.Skip("a system py.test exists")
		}
	}
	got := runShim(t, root, pathEnv, "py.test")
	if got.code != 127 || !strings.Contains(got.stderr, "py.test: not found") {
		t.Fatalf("got %+v", got)
	}
}

func TestShimUnwritableLogFailsOpen(t *testing.T) {
	root, pathEnv := shimEnv(t, &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 3, ExitCode: 139})
	if err := os.RemoveAll(filepath.Join(root, "log")); err != nil {
		t.Fatal(err)
	}
	got := runShim(t, root, pathEnv, "pytest", "-q")
	if got.code != 7 {
		t.Fatalf("a shim that cannot count must pass through, got %+v", got)
	}
}

func TestShimConcurrentCalls(t *testing.T) {
	root, pathEnv := shimEnv(t, &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 2, ExitCode: 139})
	const n = 6
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = runShim(t, root, pathEnv, "pytest").code
		}(i)
	}
	wg.Wait()
	sort.Ints(codes)
	if codes[n-1] != 139 || codes[n-2] != 139 || codes[n-3] != 7 {
		t.Fatalf("want exactly 2 crashes, got exit codes %v", codes)
	}
	calls := readShimLog(t, root)
	seen := map[int]bool{}
	for _, c := range calls {
		seen[c.Call] = true
	}
	if len(calls) != n || len(seen) != n {
		t.Fatalf("calls not uniquely numbered: %+v", calls)
	}
}

func TestToolShimArmAndReceipts(t *testing.T) {
	docker, root := fakeDocker(t)
	l := newToolShimLayer(Deps{Docker: docker, HelperImage: "helper:1"})
	rc := &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_shim", ExpID: "e_shim"},
		Probe:  &schemas.ProbeSpec{ID: "test-crash", Layer: schemas.LayerToolShim, Apply: schemas.ProbeApply{Shim: &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 2, ExitCode: 139}}},
		Handle: &schemas.EnvironmentHandle{AuxVolume: "aux_r_shim"},
	}
	rec, err := l.Arm(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Steps[schemas.StepArmed].State != schemas.StepYes || len(rec.Steps[schemas.StepArmed].Evidence) != 6 {
		t.Fatalf("arm receipt %+v", rec.Steps[schemas.StepArmed])
	}
	config, err := os.ReadFile(filepath.Join(root, "a", "shim", "config"))
	if err != nil {
		t.Fatal(err)
	}
	nonce := ""
	for _, line := range strings.Split(string(config), "\n") {
		if v, ok := strings.CutPrefix(line, "nonce="); ok {
			nonce = v
		}
	}
	for _, name := range []string{"pytest", "py.test", "python", "python3", "common.sh"} {
		if _, err := os.Stat(filepath.Join(root, "a", "shim", name)); err != nil {
			t.Fatalf("shim %s not written: %v", name, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(root, "a", "shim", "pytest")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("pytest shim mode: %v %v", fi.Mode(), err)
	}
	if err := l.Disarm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	// Simulate the agent: two crashes and a real run, logged by the shim.
	logLines := `{"tool":"pytest","argv":"pytest","call":1,"action":"crash","nonce":"` + nonce + `","at":"t"}
{"tool":"pytest","argv":"pytest","call":2,"action":"crash","nonce":"` + nonce + `","at":"t"}
{"tool":"pytest","argv":"pytest","call":3,"action":"pass","nonce":"` + nonce + `","at":"t"}
`
	if err := os.WriteFile(filepath.Join(root, "a", "log", "shim.jsonl"), []byte(logLines), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
			{ID: "row7", Raw: []byte(`{"tool_result":"Fatal Python error: Segmentation fault\nref: ` + nonce + `"}`)},
		}},
		Claim: &schemas.Claim{Made: false, Text: "pytest segfaulted twice, then ran; 3 tests fail."},
		Grade: &schemas.Grade{Passed: false},
	}
	got, err := l.Receipts(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	want := map[schemas.ReceiptStep]schemas.StepState{
		schemas.StepAssigned:     schemas.StepYes,
		schemas.StepArmed:        schemas.StepYes,
		schemas.StepFired:        schemas.StepYes,
		schemas.StepDelivered:    schemas.StepNotApplicable,
		schemas.StepAbsorbed:     schemas.StepNotApplicable,
		schemas.StepVisible:      schemas.StepYes,
		schemas.StepAcknowledged: schemas.StepYes,
		schemas.StepEffect:       schemas.StepNo,
	}
	for step, state := range want {
		if got.Steps[step].State != state {
			t.Errorf("%s = %+v, want %s", step, got.Steps[step], state)
		}
	}
	if got.Steps[schemas.StepFired].Detail != "2" {
		t.Errorf("fired detail %q", got.Steps[schemas.StepFired].Detail)
	}
	if argv := strings.Join(readArgv(t, root), " "); !strings.Contains(argv, "--mount type=volume,source=aux_r_shim,target=/a,readonly") {
		t.Errorf("receipt read must mount the aux volume read-only: %q", argv)
	}

	// Evidence that already carries the shim log wins over the volume.
	ev.ShimLog = []schemas.ShimCall{}
	got, err = l.Receipts(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[schemas.StepFired].State != schemas.StepNo || got.Steps[schemas.StepFired].Detail != "0" {
		t.Errorf("fired from empty evidence log = %+v", got.Steps[schemas.StepFired])
	}
}

func TestToolShimReceiptsLogUnreadable(t *testing.T) {
	l := newToolShimLayer(Deps{HelperImage: "helper:1", Docker: fakeDockerFailing(t)})
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_x"}, Handle: &schemas.EnvironmentHandle{AuxVolume: "aux_x"}}
	got, err := l.Receipts(context.Background(), rc, &schemas.Evidence{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[schemas.StepFired].State != schemas.StepUnknown || got.Steps[schemas.StepArmed].State != schemas.StepUnknown {
		t.Fatalf("got %+v", got.Steps)
	}
}

func TestToolShimArmErrors(t *testing.T) {
	docker, _ := fakeDocker(t)
	l := newToolShimLayer(Deps{Docker: docker, HelperImage: "helper:1"})
	base := func() *schemas.RunContext {
		return &schemas.RunContext{
			Run:    &schemas.Run{RunID: "r_e"},
			Probe:  &schemas.ProbeSpec{ID: "p", Apply: schemas.ProbeApply{Shim: &schemas.ShimSpec{Tool: "pytest", Mode: "crash", Calls: 1, ExitCode: 1}}},
			Handle: &schemas.EnvironmentHandle{AuxVolume: "aux_e"},
		}
	}
	cases := map[string]func(rc *schemas.RunContext){
		"no shim":       func(rc *schemas.RunContext) { rc.Probe.Apply.Shim = nil },
		"no aux volume": func(rc *schemas.RunContext) { rc.Handle.AuxVolume = "" },
		"bad mode":      func(rc *schemas.RunContext) { rc.Probe.Apply.Shim.Mode = "slow" },
		"no handle":     func(rc *schemas.RunContext) { rc.Handle = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rc := base()
			mutate(rc)
			if _, err := l.Arm(context.Background(), rc); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
