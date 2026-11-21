package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// hiddenCanaries are strings planted only in hidden tests, solutions,
// controls, hidden test names and admission details. No response of the
// suites API may ever contain one.
var hiddenCanaries = []string{
	"CANARY_VERIFIER_CONTENT", "test_canary_hidden_name", "test_hidden.py", "CANARY_SOLUTION",
	"CANARY_EMPTY", "CANARY_MUTANT", "CANARY_EXPLOIT", "CANARY_REASON", "CANARY_PRIVATE_DETAIL",
}

// fakeSuites is an in-memory SuiteSource over task folders in a temp dir.
type fakeSuites struct {
	ids      []string
	suites   map[string]*schemas.Suite
	loadErrs map[string]error
	drift    map[string][]schemas.SuiteDrift
	driftErr map[string]error
	fpDrift  map[string][]schemas.FingerprintDrift
	fpErr    map[string]error
	listErr  error
}

func (f *fakeSuites) SuiteIDs(context.Context) ([]string, error) { return f.ids, f.listErr }

func (f *fakeSuites) LoadSuite(_ context.Context, id string) (*schemas.Suite, error) {
	if err := f.loadErrs[id]; err != nil {
		return nil, err
	}
	s, ok := f.suites[id]
	if !ok {
		return nil, fmt.Errorf("suite %s: %w", id, schemas.ErrNotFound)
	}
	return s, nil
}

func (f *fakeSuites) LockDrift(_ context.Context, s *schemas.Suite) ([]schemas.SuiteDrift, error) {
	return f.drift[s.ID], f.driftErr[s.ID]
}

func (f *fakeSuites) FingerprintDrift(_ context.Context, s *schemas.Suite) ([]schemas.FingerprintDrift, error) {
	return f.fpDrift[s.ID], f.fpErr[s.ID]
}

// writeFiles writes rel -> content under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// hiddenFiles is what every test task holds outside its visible tree.
var hiddenFiles = map[string]string{
	"verifier/tests/test_hidden.py": "def test_canary_hidden_name():\n    assert 'CANARY_VERIFIER_CONTENT'\n",
	"verifier/requirements.md":      "CANARY_VERIFIER_CONTENT\n",
	"solution/patch.diff":           "+CANARY_SOLUTION\n",
	"controls/empty.diff":           "CANARY_EMPTY\n",
	"controls/mutant1.diff":         "+CANARY_MUTANT\n",
	"controls/exploit.sh":           "echo CANARY_EXPLOIT\n",
	"fixtures/notes.clean.md":       "notes\n",
}

// newTestTask writes a task folder and returns its task.yaml view.
func newTestTask(t *testing.T, suiteDir, id, repoDir string, visible map[string]string) *schemas.Task {
	t.Helper()
	dir := filepath.Join(suiteDir, "tasks", id)
	writeFiles(t, dir, hiddenFiles)
	writeFiles(t, dir, visible)
	return &schemas.Task{
		ID: id, Version: "1.0.0", Title: "Task " + id, Project: "ledger", Kind: schemas.TaskBugfix,
		Instruction: "instruction.md", RepoDir: repoDir, Dir: dir,
		Notes:        schemas.TaskNotes{Clean: "fixtures/notes.clean.md"},
		Verifier:     schemas.TaskVerifier{Tests: "verifier/tests", Required: []string{"test_hidden.py::test_canary_hidden_name"}},
		Protected:    []string{"tests/**"},
		Controls:     schemas.TaskControls{Reference: "solution/patch.diff", Empty: "controls/empty.diff", Mutants: []string{"controls/mutant1.diff"}, Exploit: "controls/exploit.sh"},
		Timeouts:     schemas.TaskTimeouts{AgentS: 900},
		Capabilities: []string{"test_discipline"},
		Difficulty:   "easy",
	}
}

// admittedLock is a lock entry whose details name hidden tests and
// controls, as real admission details do.
func admittedLock(at time.Time) schemas.TaskLock {
	detail := "solution/patch.diff; tests_ran=1 failures=0 errors=0 skipped=0 exit=0 missing_required=test_hidden.py::test_canary_hidden_name reason=CANARY_REASON"
	return schemas.TaskLock{Version: "1.0.0", Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{
		Admitted: true, At: at,
		Checks: []schemas.AdmissionCheck{
			{Name: "reference_1", Want: "pass", Got: "pass", Passed: true, Detail: detail},
			{Name: "empty_patch", Want: "fail", Got: "fail", Passed: true, Detail: "controls/empty.diff; tests_ran=1 failures=1 errors=0 skipped=0 exit=1 reason=CANARY_PRIVATE_DETAIL"},
			{Name: "mutant_1", Want: "fail", Got: "fail", Passed: true, Detail: "controls/mutant1.diff; CANARY_MUTANT"},
			{Name: "exploit", Want: "fail", Got: "fail", Passed: true, Detail: "exploit exit 0; CANARY_EXPLOIT"},
			{Name: "reference_stale_notes", Want: "pass", Got: "pass", Passed: true, Detail: detail},
		},
	}}
}

// testSuites builds three packs: "canary" (locked, three tasks including two
// hostile layouts), "drifted" (stale lock, a reward verifier) and "broken"
// (fails to load).
func testSuites(t *testing.T) *fakeSuites {
	t.Helper()
	root := t.TempDir()
	at := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

	canaryDir := filepath.Join(root, "canary")
	c01 := newTestTask(t, canaryDir, "c01", "repo", map[string]string{
		"instruction.md":           "# Fix it\n\n<script>alert(1)</script> VISIBLE_INSTRUCTION\n",
		"repo/src/app.py":          "print('hi')\n",
		"repo/tests/test_app.py":   "def test_app(): pass\n",
		"repo/.git/HEAD":           "ref: refs/heads/main\n",
		"repo/README.md":           "readme\n",
		"repo/docs/guide/intro.md": "intro\n",
	})
	c01.Provenance = &schemas.TaskProvenance{Source: "git@example.com:acme/ledger.git", BaseCommit: "abc123", ReferenceCommit: "def456", License: "MIT", CreatedBy: "task init"}
	c01.Tags = []string{"money"}
	// A symlink in the visible tree that points at a hidden test is never listed.
	if err := os.Symlink(filepath.Join(c01.Dir, "verifier", "tests", "test_hidden.py"), filepath.Join(c01.Dir, "repo", "peek.py")); err != nil {
		t.Fatal(err)
	}
	// c02's instruction is a symlink to the reference solution.
	c02 := newTestTask(t, canaryDir, "c02", "repo", map[string]string{"repo/a.py": "a\n"})
	if err := os.Symlink(filepath.Join(c02.Dir, "solution", "patch.diff"), filepath.Join(c02.Dir, "instruction.md")); err != nil {
		t.Fatal(err)
	}
	// c03's repo_dir is the whole task folder.
	c03 := newTestTask(t, canaryDir, "c03", ".", map[string]string{"instruction.md": "Do the thing.\n", "src/b.py": "b\n"})
	canary := &schemas.Suite{
		ID: "canary", Version: "1.0.0", Description: "Canary pack", Environment: "docker_workspace", Grader: "hidden_tests",
		BaseImage: schemas.ImageSpec{Tag: "bench/canary:1"}, Root: canaryDir,
		Verifier: schemas.SuiteVerifier{Command: []string{"pytest"}, TimeoutS: 300},
		TaskIDs:  []string{"c01", "c02", "c03"}, Tasks: []*schemas.Task{c01, c02, c03},
		Probes: []*schemas.ProbeSpec{{ID: "secret-canary", Version: "1.0.0", Axis: "safety", PS2Family: schemas.FamilyMisleadingInstruction,
			Layer: schemas.LayerFixture, SafeBehavior: "Do not print the canary.", Apply: schemas.ProbeApply{Markers: []string{"CANARY_PROBE_MARKER"}}}},
		Lock: &schemas.SuiteLock{Suite: "canary", Version: "1.0.0", CreatedAt: at,
			BaseImage: schemas.ImageSpec{Tag: "bench/canary:1", Digest: "sha256:base"},
			Tasks:     map[string]schemas.TaskLock{"c01": admittedLock(at), "c02": admittedLock(at)}},
		LockHash: "sha256:lock",
	}

	driftDir := filepath.Join(root, "drifted")
	d01 := newTestTask(t, driftDir, "d01", "repo", map[string]string{"instruction.md": "Go.\n", "repo/main.go": "package main\n"})
	d02 := newTestTask(t, driftDir, "d02", "repo", map[string]string{"instruction.md": "Go.\n", "repo/main.go": "package main\n"})
	drifted := &schemas.Suite{
		ID: "drifted", Version: "0.2.0", Environment: "docker_workspace", Grader: "verifier_command",
		BaseImage: schemas.ImageSpec{Tag: "bench/drifted:1"}, Root: driftDir,
		Verifier: schemas.SuiteVerifier{Command: []string{"sh", "tests/test.sh"}, Result: schemas.VerifierResultReward, PassThreshold: 0.8},
		TaskIDs:  []string{"d01", "d02"}, Tasks: []*schemas.Task{d01, d02},
		Lock: &schemas.SuiteLock{Suite: "drifted", Version: "0.2.0", CreatedAt: at,
			Tasks: map[string]schemas.TaskLock{"d01": admittedLock(at), "d02": admittedLock(at)}},
		LockHash: "sha256:lock2",
	}
	return &fakeSuites{
		ids:      []string{"broken", "canary", "drifted"},
		suites:   map[string]*schemas.Suite{"canary": canary, "drifted": drifted},
		loadErrs: map[string]error{"broken": errors.New("load suite broken: broken/suite.yaml: schema: grader: required field missing")},
		drift: map[string][]schemas.SuiteDrift{"drifted": {
			{Scope: "task", ID: "d01", Level: "MAJOR", Action: "rerun", Fields: []string{"repo_tree_sha256", "status"}},
			{Scope: "task", ID: "d02", Level: "PATCH", Action: "reuse", Fields: []string{"version"}},
		}},
	}
}

// newSuitesClient mounts the suites and agents handlers the way the server does.
func newSuitesClient(t *testing.T, suites SuiteSource, agents AgentSource, index ReadIndex) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	common := lib.BodyLimit(testMode, lib.MaxBodyBytes)
	NewSuitesHandler(testMode, suites).RegisterRoutes(r, common)
	NewAgentsHandler(testMode, agents, index).RegisterRoutes(r, common)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

// suiteByID finds one suite in a decoded list.
func suiteByID(t *testing.T, body map[string]any, id string) map[string]any {
	t.Helper()
	for _, s := range objectsAt(t, body, "suites[]") {
		if s["id"] == id {
			return s
		}
	}
	t.Fatalf("suite %s not in %v", id, body)
	return nil
}

// taskByID finds one task in a decoded suite.
func taskByID(t *testing.T, suite map[string]any, id string) map[string]any {
	t.Helper()
	for _, task := range objectsAt(t, suite, "tasks[]") {
		if task["id"] == id {
			return task
		}
	}
	t.Fatalf("task %s not in suite %v", id, suite["id"])
	return nil
}

func TestSuitesList(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), nil, nil)
	resp := c.do(t, "GET", APIPrefix+"/suites", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	body := decodeBody(t, resp)
	if body["mode"] != string(testMode) {
		t.Errorf("mode = %v", body["mode"])
	}
	t.Run("a broken pack is listed with its error", func(t *testing.T) {
		b := suiteByID(t, body, "broken")
		if !strings.Contains(b["error"].(string), "grader: required field missing") || b["lock_state"] != "unknown" {
			t.Errorf("broken = %v", b)
		}
		if tasks, _ := b["tasks"].([]any); tasks == nil || len(tasks) != 0 {
			t.Errorf("broken tasks = %v, want []", b["tasks"])
		}
	})
	t.Run("a matching lock is locked and counts admitted tasks", func(t *testing.T) {
		s := suiteByID(t, body, "canary")
		want := map[string]any{"lock_state": "locked", "verifier_result": "junit", "task_count": float64(3), "admitted_count": float64(2),
			"lock_hash": "sha256:lock", "pass_threshold": nil, "locked_at": "2026-11-14T09:00:00Z"}
		for k, v := range want {
			if s[k] != v {
				t.Errorf("%s = %v, want %v", k, s[k], v)
			}
		}
		if img := s["base_image"].(map[string]any); img["tag"] != "bench/canary:1" || img["digest"] != "sha256:base" {
			t.Errorf("base_image = %v", img)
		}
		if _, ok := s["error"]; ok {
			t.Errorf("error set on a good pack: %v", s["error"])
		}
	})
	t.Run("tasks carry provenance, counts and per-control checks", func(t *testing.T) {
		task := taskByID(t, suiteByID(t, body, "canary"), "c01")
		want := map[string]any{"status": "admitted", "admitted": true, "stale": false, "hidden_test_files": float64(1),
			"required_tests": float64(1), "protected_globs": float64(1), "mutants": float64(1), "difficulty": "easy",
			"admitted_at": "2026-11-14T09:00:00Z"}
		for k, v := range want {
			if task[k] != v {
				t.Errorf("%s = %v, want %v", k, task[k], v)
			}
		}
		if p := task["provenance"].(map[string]any); p["license"] != "MIT" || p["base_commit"] != "abc123" {
			t.Errorf("provenance = %v", p)
		}
		controls := map[string]string{}
		for _, ch := range objectsAt(t, task, "checks[]") {
			controls[ch["name"].(string)] = ch["control"].(string)
		}
		wantControls := map[string]string{"reference_1": "reference", "empty_patch": "empty", "mutant_1": "mutant", "exploit": "exploit", "reference_stale_notes": "stale_notes"}
		for k, v := range wantControls {
			if controls[k] != v {
				t.Errorf("control of %s = %q, want %q", k, controls[k], v)
			}
		}
		first := objectsAt(t, task, "checks[]")[0]
		if first["summary"] != "tests_ran=1 failures=0 errors=0 skipped=0 exit=0" {
			t.Errorf("summary = %v", first["summary"])
		}
		if task3 := taskByID(t, suiteByID(t, body, "canary"), "c03"); task3["status"] != "not_locked" || task3["admitted"] != false || task3["provenance"] != nil {
			t.Errorf("c03 = %v", task3)
		}
	})
	t.Run("drift marks the lock stale and clears the changed task's admission only", func(t *testing.T) {
		s := suiteByID(t, body, "drifted")
		if s["lock_state"] != "stale" || s["admitted_count"] != float64(1) || s["verifier_result"] != "reward" || s["pass_threshold"] != 0.8 {
			t.Errorf("drifted = %v", s)
		}
		if d01 := taskByID(t, s, "d01"); d01["stale"] != true || d01["admitted"] != false || d01["status"] != "admitted" {
			t.Errorf("d01 = %v", d01)
		}
		if d02 := taskByID(t, s, "d02"); d02["stale"] != false || d02["admitted"] != true {
			t.Errorf("d02 (version-only drift) = %v", d02)
		}
		if drift := objectsAt(t, s, "drift[]"); len(drift) != 2 || drift[0]["level"] != "MAJOR" {
			t.Errorf("drift = %v", drift)
		}
	})
	t.Run("probes leave out the apply block", func(t *testing.T) {
		p := objectsAt(t, suiteByID(t, body, "canary"), "probes[]")[0]
		if p["ps2_family"] != "misleading_instruction" || p["layer"] != "fixture" || p["axis"] != "safety" {
			t.Errorf("probe = %v", p)
		}
		if strings.Contains(string(resp.Body()), "CANARY_PROBE_MARKER") {
			t.Error("probe markers leaked")
		}
	})
}

func TestSuitesLockStates(t *testing.T) {
	f := testSuites(t)
	f.driftErr = map[string]error{"canary": errors.New("hash suite canary: stat repo: permission denied")}
	f.suites["drifted"].Lock = nil
	f.suites["drifted"].LockHash = ""
	body := decodeBody(t, newSuitesClient(t, f, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil))
	if s := suiteByID(t, body, "canary"); s["lock_state"] != "unknown" || !strings.Contains(fmt.Sprint(s["lock_error"]), "permission denied") {
		t.Errorf("canary = %v", s)
	}
	s := suiteByID(t, body, "drifted")
	if s["lock_state"] != "unlocked" || s["locked_at"] != nil || s["admitted_count"] != float64(0) {
		t.Errorf("drifted = %v", s)
	}
	if task := taskByID(t, s, "d01"); task["status"] != "not_locked" || task["admitted_at"] != nil {
		t.Errorf("d01 = %v", task)
	}
}

func TestSuitesLockFingerprints(t *testing.T) {
	f := testSuites(t)
	fps := &schemas.LockFingerprints{Version: schemas.LockFingerprintsVersion, Suite: schemas.Fingerprint{Settings: "s", Files: "f"}}
	f.suites["canary"].Lock.Fingerprints = fps
	body := decodeBody(t, newSuitesClient(t, f, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil))
	t.Run("a legacy lock is legacy, locked and has no fingerprint drift", func(t *testing.T) {
		s := suiteByID(t, body, "drifted")
		if s["lock_kind"] != "legacy" {
			t.Errorf("lock_kind = %v, want legacy", s["lock_kind"])
		}
		if _, ok := s["fingerprint_drift"]; ok {
			t.Errorf("fingerprint_drift on a legacy lock: %v", s["fingerprint_drift"])
		}
	})
	t.Run("an unchanged fingerprinted lock is locked", func(t *testing.T) {
		s := suiteByID(t, body, "canary")
		if s["lock_kind"] != "fingerprinted" || s["lock_state"] != "locked" {
			t.Errorf("canary = lock_kind %v, lock_state %v", s["lock_kind"], s["lock_state"])
		}
		if _, ok := s["fingerprint_drift"]; ok {
			t.Errorf("fingerprint_drift = %v, want none", s["fingerprint_drift"])
		}
		checkKeys(t, "SuiteSummary canary", s, loadKeys(t, "SuiteSummary"), false)
	})
	t.Run("fingerprint drift lists the part and marks the lock stale", func(t *testing.T) {
		f.fpDrift = map[string][]schemas.FingerprintDrift{"canary": {{Scope: "suite", ID: "canary", Part: "settings", Fields: []string{"verifier"}}}}
		body := decodeBody(t, newSuitesClient(t, f, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil))
		s := suiteByID(t, body, "canary")
		if s["lock_state"] != "stale" || s["lock_kind"] != "fingerprinted" {
			t.Errorf("canary = lock_kind %v, lock_state %v", s["lock_kind"], s["lock_state"])
		}
		drift := objectsAt(t, s, "fingerprint_drift[]")
		if len(drift) != 1 || drift[0]["scope"] != "suite" || drift[0]["part"] != "settings" || fmt.Sprint(drift[0]["fields"]) != "[verifier]" {
			t.Errorf("fingerprint_drift = %v", drift)
		}
		checkKeys(t, "FingerprintDrift", drift[0], loadKeys(t, "FingerprintDrift"), false)
		checkKeys(t, "SuiteSummary canary", s, loadKeys(t, "SuiteSummary"), false)
	})
	t.Run("a fingerprint check error is unknown with its reason", func(t *testing.T) {
		f.fpDrift = nil
		f.fpErr = map[string]error{"canary": errors.New("check fingerprints of suite canary: version 2")}
		body := decodeBody(t, newSuitesClient(t, f, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil))
		s := suiteByID(t, body, "canary")
		if s["lock_state"] != "unknown" || !strings.Contains(fmt.Sprint(s["lock_error"]), "version 2") {
			t.Errorf("canary = lock_state %v, lock_error %v", s["lock_state"], s["lock_error"])
		}
	})
}

func TestSuiteTaskDetail(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), nil, nil)
	get := func(path string) *fasthttp.Response { return c.do(t, "GET", APIPrefix+path, nil, nil) }
	t.Run("instruction text and visible files", func(t *testing.T) {
		resp := get("/suites/canary/tasks/c01")
		if resp.StatusCode() != 200 {
			t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
		}
		body := decodeBody(t, resp)
		if !strings.Contains(body["instruction"].(string), "<script>alert(1)</script> VISIBLE_INSTRUCTION") {
			t.Errorf("instruction = %q", body["instruction"])
		}
		var paths []string
		for _, f := range objectsAt(t, body, "files[]") {
			paths = append(paths, f["path"].(string))
		}
		want := "README.md,docs/guide/intro.md,src/app.py,tests/test_app.py"
		if strings.Join(paths, ",") != want {
			t.Errorf("files = %v, want %s (no .git, no symlink)", paths, want)
		}
		if body["files_total"] != float64(4) || body["files_truncated"] != false || body["suite"] != "canary" {
			t.Errorf("detail = %v", body)
		}
		if task := body["task"].(map[string]any); task["id"] != "c01" || task["admitted"] != true {
			t.Errorf("task = %v", task)
		}
	})
	t.Run("repo_dir of the whole task folder hides verifier, solution and controls", func(t *testing.T) {
		body := decodeBody(t, get("/suites/canary/tasks/c03"))
		var paths []string
		for _, f := range objectsAt(t, body, "files[]") {
			paths = append(paths, f["path"].(string))
		}
		if got := strings.Join(paths, ","); got != "fixtures/notes.clean.md,instruction.md,src/b.py" {
			t.Errorf("files = %s", got)
		}
	})
	t.Run("an instruction that links into the solution is refused", func(t *testing.T) {
		resp := get("/suites/canary/tasks/c02")
		if resp.StatusCode() != 500 || !strings.Contains(string(resp.Body()), "hidden from the agent") {
			t.Errorf("status = %d: %s", resp.StatusCode(), resp.Body())
		}
	})
	t.Run("unknown ids", func(t *testing.T) {
		for path, status := range map[string]int{
			"/suites/nope/tasks/c01":         404,
			"/suites/canary/tasks/c99":       404,
			"/suites/canary/tasks/..":        404,
			"/suites/broken/tasks/c01":       422,
			"/suites/canary/tasks/c01%2F..":  404,
			"/suites/c%40n/tasks/c01":        404,
			"/suites/canary/tasks/c01/extra": 404,
		} {
			if resp := get(path); resp.StatusCode() != status {
				t.Errorf("%s: status = %d, want %d: %s", path, resp.StatusCode(), status, resp.Body())
			}
		}
	})
}

func TestSuitesNeverServeHiddenContent(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), nil, nil)
	paths := []string{"/suites"}
	for _, task := range []string{"c01", "c02", "c03"} {
		paths = append(paths, "/suites/canary/tasks/"+task)
	}
	paths = append(paths, "/suites/drifted/tasks/d01", "/suites/drifted/tasks/d02", "/suites/broken/tasks/x")
	for _, p := range paths {
		body := string(c.do(t, "GET", APIPrefix+p, nil, nil).Body())
		for _, canary := range hiddenCanaries {
			if strings.Contains(body, canary) {
				t.Errorf("GET %s leaked %q: %s", p, canary, body)
			}
		}
	}
}

func TestSuitesUnavailableAndListError(t *testing.T) {
	if resp := newSuitesClient(t, nil, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("nil source: status = %d", resp.StatusCode())
	}
	f := testSuites(t)
	f.listErr = errors.New("read benchmarks folder: permission denied")
	if resp := newSuitesClient(t, f, nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil); resp.StatusCode() != 500 {
		t.Errorf("list error: status = %d", resp.StatusCode())
	}
}

func TestSuitesMatchContract(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), fakeAgents{}, testIndex())
	tests := []struct {
		path, typ string
		nested    map[string]string
	}{
		{"/suites", "SuiteList", map[string]string{
			"suites[]": "SuiteSummary", "suites[].base_image": "SuiteImage", "suites[].drift[]": "SuiteDrift",
			"suites[].probes[]": "SuiteProbe", "suites[].tasks[]": "SuiteTask", "suites[].tasks[].checks[]": "SuiteAdmissionCheck",
			"suites[].tasks[].provenance": "TaskProvenance",
		}},
		{"/suites/canary/tasks/c01", "SuiteTaskDetail", map[string]string{
			"task": "SuiteTask", "task.checks[]": "SuiteAdmissionCheck", "task.provenance": "TaskProvenance", "files[]": "SuiteRepoFile",
		}},
		{"/agents", "AgentList", map[string]string{"agents[]": "AgentSummary", "agents[].budgets": "AgentBudgets", "agents[].gateway": "AgentGateway"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			body := decodeBody(t, c.do(t, "GET", APIPrefix+tt.path, nil, nil))
			checkKeys(t, tt.typ, body, loadKeys(t, tt.typ), true)
			for path, typ := range tt.nested {
				for i, obj := range objectsAt(t, body, path) {
					checkKeys(t, fmt.Sprintf("%s.%s #%d", tt.typ, path, i), obj, loadKeys(t, typ), false)
				}
			}
		})
	}
}

func TestSuiteHelpers(t *testing.T) {
	t.Run("controlOf", func(t *testing.T) {
		for name, want := range map[string]string{
			"reference_3": "reference", "reference_stale_notes": "stale_notes", "empty_patch": "empty", "mutant_12": "mutant",
			"exploit": "exploit", "null_noop": "null_agent", "noop": "null_agent", "refuse": "null_agent",
			"secret_scan": "secrets", "verifier_network": "network", "custom": "other",
		} {
			if got := controlOf(name); got != want {
				t.Errorf("controlOf(%q) = %q, want %q", name, got, want)
			}
		}
	})
	t.Run("checkSummary keeps counts only", func(t *testing.T) {
		got := checkSummary("controls/a.diff; tests_ran=3 failures=2 errors=0 skipped=1 exit=1 missing_required=x::y reason=exit 1; 2 failures")
		if got != "tests_ran=3 failures=2 errors=0 skipped=1 exit=1" {
			t.Errorf("summary = %q", got)
		}
		if got := checkSummary("exploit exit 0"); got != "" {
			t.Errorf("summary = %q, want empty", got)
		}
	})
	t.Run("readCapped cuts on a rune boundary", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "i.md")
		writeFiles(t, filepath.Dir(p), map[string]string{"i.md": "aé" + strings.Repeat("x", 10)})
		text, truncated, err := readCapped(p, 2)
		if err != nil || !truncated || text != "a" {
			t.Errorf("readCapped = %q, %v, %v", text, truncated, err)
		}
	})
	t.Run("files are capped", func(t *testing.T) {
		dir := t.TempDir()
		files := map[string]string{"instruction.md": "x"}
		for i := 0; i < FilesLimit+3; i++ {
			files[fmt.Sprintf("repo/f%04d.txt", i)] = "x"
		}
		writeFiles(t, dir, files)
		task := &schemas.Task{ID: "big", Dir: dir, Instruction: "instruction.md", RepoDir: "repo"}
		d, err := taskDetail(&schemas.Suite{ID: "s"}, task, schemas.SuiteSummary{})
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Files) != FilesLimit || d.FilesTotal != FilesLimit+3 || !d.FilesTruncated {
			t.Errorf("files = %d of %d, truncated %v", len(d.Files), d.FilesTotal, d.FilesTruncated)
		}
	})
}

// libraryFakeSuites marks some packs as loaded from the built-in library.
type libraryFakeSuites struct {
	*fakeSuites
	builtin map[string]bool
}

func (l libraryFakeSuites) SuiteBuiltin(id string) bool { return l.builtin[id] }

func TestSuitesListBuiltinFlag(t *testing.T) {
	src := testSuites(t)
	if len(src.ids) == 0 {
		t.Fatal("no fixture suites")
	}
	lib := src.ids[0]
	c := newSuitesClient(t, libraryFakeSuites{fakeSuites: src, builtin: map[string]bool{lib: true}}, nil, nil)
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil))
	for _, id := range src.ids {
		s := suiteByID(t, body, id)
		if id == lib {
			if s["builtin"] != true || s["source"] != "library" {
				t.Errorf("%s: builtin=%v source=%v", id, s["builtin"], s["source"])
			}
		} else if _, ok := s["builtin"]; ok {
			t.Errorf("%s: org pack marked builtin", id)
		}
	}
}

// A write route on a built-in library suite answers 403 and never runs.
func TestRefuseBuiltinWrite(t *testing.T) {
	src := libraryFakeSuites{fakeSuites: &fakeSuites{}, builtin: map[string]bool{"hd-harbor-tb2": true}}
	ran := false
	h := refuseBuiltin(testMode, src, func(*fasthttp.RequestCtx) { ran = true })
	var ctx fasthttp.RequestCtx
	ctx.SetUserValue("id", "hd-harbor-tb2")
	h(&ctx)
	if ran || ctx.Response.StatusCode() != fasthttp.StatusForbidden || !strings.Contains(string(ctx.Response.Body()), "Built-in benchmark, read-only") {
		t.Fatalf("library write: ran=%v status=%d body=%s", ran, ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var own fasthttp.RequestCtx
	own.SetUserValue("id", "my-suite")
	h(&own)
	if !ran {
		t.Fatal("org suite write refused")
	}
}
