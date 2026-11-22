package handlers

import (
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// newImportsClient mounts the imports handler over a fixed importer list.
func newImportsClient(t *testing.T, infos []importers.Info) *testClient {
	t.Helper()
	r := router.New()
	h := NewImportsHandler(testMode)
	h.list = func() []importers.Info { return infos }
	h.RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
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

func TestImportsList(t *testing.T) {
	harbor := importers.Info{
		Name: "harbor", Benchmarks: []string{"terminal-bench@2.1"}, Source: "git clone --depth 1 https://example.com/tb",
		License: "Apache-2.0", Track: schemas.TrackTerminal, Grading: schemas.GradingVerified,
		Needs: []string{"per-task images"}, Summary: "Harbor task folders",
	}
	bare := importers.Info{Name: "locomo", Track: schemas.TrackMemory, Grading: schemas.GradingJudged, License: "CC-BY-NC-4.0"}
	tests := []struct {
		name  string
		infos []importers.Info
		want  []string
	}{
		{"none linked", nil, nil},
		{"two importers", []importers.Info{harbor, bare}, []string{"harbor", "locomo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := newImportsClient(t, tt.infos).do(t, "GET", APIPrefix+"/imports", nil, nil)
			if resp.StatusCode() != fasthttp.StatusOK {
				t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
			}
			body := decodeBody(t, resp)
			checkKeys(t, "ImportList", body, loadKeys(t, "ImportList"), true)
			list, ok := body["importers"].([]any)
			if !ok {
				t.Fatalf("importers = %#v, want an array", body["importers"])
			}
			var names []string
			for i, el := range list {
				obj := el.(map[string]any)
				checkKeys(t, fmt.Sprintf("ImporterInfo #%d", i), obj, loadKeys(t, "ImporterInfo"), false)
				// Nil slices are sent as empty arrays, never null.
				for _, k := range []string{"benchmarks", "needs"} {
					if _, ok := obj[k].([]any); !ok {
						t.Errorf("%s.%s = %#v, want an array", obj["name"], k, obj[k])
					}
				}
				names = append(names, obj["name"].(string))
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("names = %v, want %v", names, tt.want)
			}
			if body["mode"] != string(testMode) {
				t.Errorf("mode = %v", body["mode"])
			}
		})
	}
}

func TestImportsHandlerUsesRegistry(t *testing.T) {
	h := NewImportsHandler(testMode)
	if got, want := len(h.list()), len(importers.All()); got != want {
		t.Errorf("list() has %d importers, registry has %d", got, want)
	}
}

// scenarioSuites is one plain pack and one imported scenario pack with an
// answer output, services and a task that uses every scenario field.
func scenarioSuites(t *testing.T) *fakeSuites {
	t.Helper()
	root := t.TempDir()
	plainDir := filepath.Join(root, "plain")
	p01 := newTestTask(t, plainDir, "p01", "repo", map[string]string{"instruction.md": "Fix.\n", "repo/a.py": "a\n"})
	plain := &schemas.Suite{
		ID: "plain", Version: "1.0.0", Environment: "docker_workspace", Grader: "hidden_tests",
		BaseImage: schemas.ImageSpec{Tag: "bench/plain:1"}, Root: plainDir,
		TaskIDs: []string{"p01"}, Tasks: []*schemas.Task{p01},
	}

	scenDir := filepath.Join(root, "memo")
	m01 := newTestTask(t, scenDir, "m01", "repo", map[string]string{
		"instruction.md":                "Remember.\n",
		"repo/a.py":                     "a\n",
		"skills/notes/SKILL.md":         "# notes\n",
		"skills/search/SKILL.md":        "# search\n",
		"skills/empty/README.md":        "not a skill\n",
		"verifier/skills/peek/SKILL.md": "# hidden\n",
	})
	proxy := true
	m01.Environment = &schemas.TaskEnvironment{Image: "bench/task-memo-m01:abc", WorkspacePath: "/app"}
	m01.Services = []schemas.ServiceSpec{{
		ID: "db", Role: schemas.ServiceRoleData, Image: "postgres:16", Port: 5432, Proxy: &proxy,
		Env:    map[string]string{"POSTGRES_PASSWORD": "CANARY_SERVICE_ENV"},
		Files:  map[string]string{"/seed.sql": "verifier/CANARY_SEED.sql"},
		Argv:   []string{"postgres", "CANARY_ARGV"},
		Export: []schemas.StateExport{{ID: "dump", Argv: []string{"pg_dump", "CANARY_EXPORT_ARGV"}}, {ID: "log", Path: "/var/log/app.log"}},
		MCP:    &schemas.MCPEndpoint{Path: "/mcp"},
	}}
	m01.Sessions = []schemas.SessionSpec{{ID: "learn", Instruction: "s1.md", Files: "chat"}, {ID: "recall", Instruction: "s2.md", KeepWorkspace: true}}
	m01.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer, AnswerPath: "out/answer.txt"}
	m01.Skills = "skills"
	m01.Impossible = true
	m02 := newTestTask(t, scenDir, "m02", "repo", map[string]string{"instruction.md": "Go.\n", "repo/a.py": "a\n"})
	m02.Skills = "verifier/skills"
	at := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	memo := &schemas.Suite{
		ID: "memo", Version: "0.1.0", Environment: "docker_workspace", Grader: "judge",
		BaseImage: schemas.ImageSpec{Tag: "bench/memo:1"}, Root: scenDir,
		TaskIDs: []string{"m01", "m02"}, Tasks: []*schemas.Task{m01, m02},
		Track: schemas.TrackMemory, Grading: schemas.GradingJudged,
		Output:   &schemas.OutputSpec{Kind: schemas.OutputAnswer},
		Services: []schemas.ServiceSpec{{ID: "user", Role: schemas.ServiceRoleUser, Env: map[string]string{"K": "CANARY_SUITE_ENV"}}},
		Imported: &schemas.ImportRecord{Importer: "locomo", Benchmark: "locomo@1", Source: "https://example.com/locomo", Revision: "r1", License: "CC-BY-NC-4.0", At: at},
	}
	return &fakeSuites{
		ids:    []string{"memo", "plain"},
		suites: map[string]*schemas.Suite{"memo": memo, "plain": plain},
	}
}

func TestSuitesScenarioFields(t *testing.T) {
	c := newSuitesClient(t, scenarioSuites(t), fakeAgents{}, testIndex())
	resp := c.do(t, "GET", APIPrefix+"/suites", nil, nil)
	body := decodeBody(t, resp)
	for _, canary := range []string{"CANARY_SERVICE_ENV", "CANARY_SEED", "CANARY_ARGV", "CANARY_EXPORT_ARGV", "CANARY_SUITE_ENV", "peek"} {
		if strings.Contains(string(resp.Body()), canary) {
			t.Errorf("suite list carries %s", canary)
		}
	}

	t.Run("plain suite gets only its defaults", func(t *testing.T) {
		s := suiteByID(t, body, "plain")
		if s["track"] != schemas.TrackCoding || s["grading"] != schemas.GradingVerified {
			t.Errorf("track %v grading %v", s["track"], s["grading"])
		}
		// The suites handler names the default output; this test only pins that it is diff when set.
		if out, ok := s["output"]; ok && out != schemas.OutputDiff {
			t.Errorf("output = %v", out)
		}
		for _, k := range []string{"services", "imported"} {
			if _, ok := s[k]; ok {
				t.Errorf("plain suite has %s = %v", k, s[k])
			}
		}
		task := taskByID(t, s, "p01")
		for _, k := range []string{"environment", "services", "sessions", "output", "skills", "impossible"} {
			if _, ok := task[k]; ok {
				t.Errorf("plain task has %s = %v", k, task[k])
			}
		}
	})

	t.Run("scenario suite", func(t *testing.T) {
		s := suiteByID(t, body, "memo")
		if s["track"] != schemas.TrackMemory || s["grading"] != schemas.GradingJudged || s["output"] != schemas.OutputAnswer {
			t.Errorf("track %v grading %v output %v", s["track"], s["grading"], s["output"])
		}
		if fmt.Sprint(s["services"]) != "[user]" {
			t.Errorf("services = %v", s["services"])
		}
		imp, _ := s["imported"].(map[string]any)
		checkKeys(t, "ImportRecord", imp, loadKeys(t, "ImportRecord"), false)
		if imp["benchmark"] != "locomo@1" || imp["license"] != "CC-BY-NC-4.0" || imp["revision"] != "r1" {
			t.Errorf("imported = %v", imp)
		}
	})

	t.Run("scenario task", func(t *testing.T) {
		task := taskByID(t, suiteByID(t, body, "memo"), "m01")
		env, _ := task["environment"].(map[string]any)
		if env["image"] != "bench/task-memo-m01:abc" || env["workspace_path"] != "/app" {
			t.Errorf("environment = %v", env)
		}
		svcs, _ := task["services"].([]any)
		if len(svcs) != 1 {
			t.Fatalf("services = %v", task["services"])
		}
		svc := svcs[0].(map[string]any)
		for _, k := range []string{"env", "files", "argv"} {
			if _, ok := svc[k]; ok {
				t.Errorf("service carries %s", k)
			}
		}
		if svc["id"] != "db" || svc["role"] != "data" || svc["image"] != "postgres:16" || svc["proxy"] != true {
			t.Errorf("service = %v", svc)
		}
		if fmt.Sprint(svc["export"]) != "[map[id:dump] map[id:log path:/var/log/app.log]]" {
			t.Errorf("export = %v", svc["export"])
		}
		sessions, _ := task["sessions"].([]any)
		if len(sessions) != 2 || sessions[1].(map[string]any)["keep_workspace"] != true {
			t.Errorf("sessions = %v", task["sessions"])
		}
		if out, _ := task["output"].(map[string]any); out["kind"] != "answer" || out["answer_path"] != "out/answer.txt" {
			t.Errorf("output = %v", task["output"])
		}
		if fmt.Sprint(task["skills"]) != "[notes search]" {
			t.Errorf("skills = %v", task["skills"])
		}
		if task["impossible"] != true {
			t.Errorf("impossible = %v", task["impossible"])
		}
	})

	t.Run("skills in a hidden folder are not named", func(t *testing.T) {
		task := taskByID(t, suiteByID(t, body, "memo"), "m02")
		if fmt.Sprint(task["skills"]) != "[verifier/skills]" {
			t.Errorf("skills = %v", task["skills"])
		}
	})

	t.Run("task detail carries the same fields", func(t *testing.T) {
		detail := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/memo/tasks/m01", nil, nil))
		checkKeys(t, "SuiteTaskDetail", detail, loadKeys(t, "SuiteTaskDetail"), true)
		task, _ := detail["task"].(map[string]any)
		checkKeys(t, "SuiteTask", task, loadKeys(t, "SuiteTask"), false)
		if task["impossible"] != true || task["output"] == nil {
			t.Errorf("detail task = %v", task)
		}
	})

	t.Run("keys match the contract", func(t *testing.T) {
		checkKeys(t, "SuiteList", body, loadKeys(t, "SuiteList"), true)
		for i, obj := range objectsAt(t, body, "suites[]") {
			checkKeys(t, fmt.Sprintf("SuiteSummary #%d", i), obj, loadKeys(t, "SuiteSummary"), false)
		}
		for i, obj := range objectsAt(t, body, "suites[].tasks[]") {
			checkKeys(t, fmt.Sprintf("SuiteTask #%d", i), obj, loadKeys(t, "SuiteTask"), false)
		}
	})
}

// TestWorkersListMatchesUIContract pins GET /workers, which the UI's workers
// page reads, to the WorkerList key list, with worker mode off.
func TestWorkersListMatchesUIContract(t *testing.T) {
	r := router.New()
	NewWorkersHandler(testMode, nil).RegisterRoutes(r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	c := &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/workers", nil, nil))
	checkKeys(t, "WorkerList", body, loadKeys(t, "WorkerList"), true)
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false with no coordinator", body["enabled"])
	}
}

// TestSuitesArtifactSummary pins the suite's declared output artifacts in the
// suite list: copied for an artifact suite, absent for any other.
func TestSuitesArtifactSummary(t *testing.T) {
	suites := scenarioSuites(t)
	dir := filepath.Join(t.TempDir(), "plots")
	a01 := newTestTask(t, dir, "a01", "repo", map[string]string{"instruction.md": "Plot.\n", "repo/a.py": "a\n"})
	declared := []schemas.ArtifactSpec{
		{ID: "chart", Path: "out/*.svg", Required: true, MaxBytes: 1 << 20},
		{ID: "table", Path: "out/table.csv"},
	}
	plots := &schemas.Suite{
		ID: "plots", Version: "1.0.0", Environment: "docker_workspace", Grader: "hidden_tests",
		BaseImage: schemas.ImageSpec{Tag: "bench/plots:1"}, Root: dir,
		TaskIDs: []string{"a01"}, Tasks: []*schemas.Task{a01},
		Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: declared},
	}
	suites.ids = append(suites.ids, "plots")
	suites.suites["plots"] = plots
	c := newSuitesClient(t, suites, fakeAgents{}, testIndex())
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil))

	s := suiteByID(t, body, "plots")
	checkKeys(t, "SuiteSummary plots", s, loadKeys(t, "SuiteSummary"), false)
	if s["output"] != schemas.OutputArtifact {
		t.Errorf("output = %v", s["output"])
	}
	got, _ := s["artifacts"].([]any)
	if len(got) != 2 {
		t.Fatalf("artifacts = %v", s["artifacts"])
	}
	chart, _ := got[0].(map[string]any)
	if chart["id"] != "chart" || chart["path"] != "out/*.svg" || chart["required"] != true || chart["max_bytes"] != float64(1<<20) {
		t.Errorf("artifacts[0] = %v", chart)
	}
	table, _ := got[1].(map[string]any)
	if _, ok := table["required"]; ok || table["id"] != "table" {
		t.Errorf("artifacts[1] = %v", table)
	}
	// The summary holds a copy: changing it never reaches the loaded suite.
	var sum schemas.SuiteSummary
	scenarioSummary(plots, &sum)
	sum.Artifacts[0].ID = "changed"
	if plots.Output.Artifacts[0].ID != "chart" {
		t.Errorf("summary shares the suite's artifacts: %v", plots.Output.Artifacts)
	}
	for _, id := range []string{"plain", "memo"} {
		if _, ok := suiteByID(t, body, id)["artifacts"]; ok {
			t.Errorf("suite %s has artifacts", id)
		}
	}
}

// J12.draft: a hosted readiness run stages a draft as benchmark draft-<set> for a while; the list
// (and every picker built on it) leaves it out, while a team benchmark named draft-... stays.
func TestSuitesListHidesStagingDrafts(t *testing.T) {
	suites := scenarioSuites(t)
	for _, id := range []string{"draft-20261122-150634-a4b821", "draft-notes"} {
		s := *suites.suites["plain"]
		s.ID = id
		suites.ids = append(suites.ids, id)
		suites.suites[id] = &s
	}
	c := newSuitesClient(t, suites, fakeAgents{}, testIndex())
	resp := c.do(t, "GET", APIPrefix+"/suites", nil, nil)
	if strings.Contains(string(resp.Body()), "draft-20261122-150634-a4b821") {
		t.Errorf("GET /suites lists the staged draft: %s", resp.Body())
	}
	body := decodeBody(t, resp)
	suiteByID(t, body, "draft-notes")
	suiteByID(t, body, "plain")
}
