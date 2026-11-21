package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeRefsLauncher records what a job asks of the org's launcher and runs
// nothing: hosted, the real one queues the run for a runner.
type fakeRefsLauncher struct {
	planned []*schemas.Experiment
	created []string
	started []string
	active  map[string]int // polls left before the experiment reads as ended
}

func (l *fakeRefsLauncher) Plan(_ context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
	l.planned = append(l.planned, e)
	arms := map[string]schemas.ArmManifest{}
	for id := range e.Arms {
		arms[id] = schemas.ArmManifest{}
	}
	return &schemas.Manifest{ExpID: "e_" + slug(e.Name), Experiment: *e, Arms: arms,
		Slots: []schemas.Slot{{SlotID: "s_1", Arm: "A"}, {SlotID: "s_2", Arm: "B"}}}, nil
}
func (l *fakeRefsLauncher) Create(_ context.Context, m *schemas.Manifest) error {
	l.created = append(l.created, m.ExpID)
	return nil
}
func (l *fakeRefsLauncher) Start(_ context.Context, id string) error {
	l.started = append(l.started, id)
	return nil
}
func (l *fakeRefsLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	ch := make(chan schemas.LiveEvent)
	close(ch)
	return ch, func() {}
}
func (l *fakeRefsLauncher) Active(id string) bool {
	if l.active[id] > 0 {
		l.active[id]--
		return true
	}
	return false
}

var _ handlers.ActivityReporter = (*fakeRefsLauncher)(nil)

func quickRefsEnv(t *testing.T) (jobEnv, *fakeRefsLauncher, string, string, string) {
	t.Helper()
	dir, base, ref := initQuickRepo(t)
	cfg, err := lib.LoadConfig(filepath.Join(dir, ".heimdall", "deploy", "bench.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fl := &fakeRefsLauncher{active: map[string]int{}}
	a := &app{cfg: cfg, logger: utils.NewStdLogger(os.Stderr, false)}
	return jobEnv{App: a, OrgID: "local", ProjectRoot: cfg.Root, Services: handlers.Services{OrgID: "local", Launcher: fl}}, fl, dir, base, ref
}

func runRefsJob(t *testing.T, env jobEnv, params string) (*compareRefsParams, any, error) {
	t.Helper()
	ctx := context.Background()
	norm, _, err := validateCompareRefsJob(ctx, env, json.RawMessage(params))
	if err != nil {
		return nil, nil, err
	}
	raw, _ := json.Marshal(norm)
	p := norm.(compareRefsParams)
	res, err := runCompareRefsJob(ctx, env, &jobs.Job{Params: raw}, &lineLog{}, func(any) {})
	return &p, res, err
}

func TestCompareRefsJobLocalRepo(t *testing.T) {
	env, fl, dir, base, ref := quickRefsEnv(t)
	params := `{"agent":"agents/claude-code.yaml","ref_a":"` + base + `","ref_b":"` + ref + `","suite":"lib","trials":2,"budget_usd":1,"name":"web-refs","tasks":["t1"]}`
	_, res, err := runRefsJob(t, env, params)
	if err != nil {
		t.Fatal(err)
	}
	r := res.(compareRefsResult)
	if !r.Started || len(fl.started) != 1 || fl.started[0] != r.ExpID || len(fl.created) != 1 {
		t.Fatalf("not started through the launcher: %+v %+v", r, fl)
	}
	if r.File != "experiments/web-refs.yaml" || !strings.Contains(r.Subject, base[:12]) {
		t.Fatalf("result = %+v", r)
	}
	e, err := loadExperiment(filepath.Join(dir, ".heimdall", r.File))
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := e.Arms["B"].Set["notes.user_file"].(string); n != "experiments/fixtures/web-refs/B/AGENTS.md" {
		t.Fatalf("arm B notes = %q", n)
	}
	for _, c := range e.Conditions {
		if len(c.Tasks.IDs) != 1 || c.Tasks.IDs[0] != "t1" {
			t.Fatalf("tasks not pinned: %+v", c.Tasks)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".heimdall", "experiments", "fixtures", "web-refs", "B", "AGENTS.md")); !strings.Contains(string(b), "Never edit tests") {
		t.Errorf("candidate notes = %q", b)
	}
	// Same name again: refused before anything is planned.
	if _, _, err := runRefsJob(t, env, params); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("second run with the same name: %v", err)
	}
	if len(fl.planned) != 1 {
		t.Errorf("planned %d times", len(fl.planned))
	}
}

func TestCompareRefsJobValidate(t *testing.T) {
	env, _, _, _, _ := quickRefsEnv(t)
	ctx := context.Background()
	bad := map[string]string{
		"same":    `{"agent":"agents/claude-code.yaml","ref_a":"main","ref_b":"main","suite":"lib"}`,
		"escape":  `{"agent":"../x.yaml","ref_a":"a","ref_b":"b","suite":"lib"}`,
		"ref":     `{"agent":"agents/claude-code.yaml","ref_a":"-x","ref_b":"b","suite":"lib"}`,
		"dots":    `{"agent":"agents/claude-code.yaml","ref_a":"a..b","ref_b":"b","suite":"lib"}`,
		"repo":    `{"agent":"agents/claude-code.yaml","ref_a":"a","ref_b":"b","suite":"lib","repo":"nope"}`,
		"unknown": `{"agent":"agents/claude-code.yaml","ref_a":"a","ref_b":"b","suite":"lib","shell":1}`,
		"path":    `{"agent":"agents/claude-code.yaml","ref_a":"a","ref_b":"b","suite":"lib","paths":["../etc"]}`,
	}
	for name, raw := range bad {
		if _, _, err := validateCompareRefsJob(ctx, env, json.RawMessage(raw)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s: want ErrInvalidParams, got %v", name, err)
		}
	}
	env.Hosted = true
	if _, _, err := validateCompareRefsJob(ctx, env, json.RawMessage(`{"agent":"agents/claude-code.yaml","ref_a":"a","ref_b":"b","suite":"lib"}`)); err == nil || !strings.Contains(err.Error(), "GitHub") {
		t.Errorf("hosted without a repository: %v", err)
	}
}

// fakeTree serves two refs of a repository whose AGENTS.md differs.
type fakeTree struct{ blobs map[string]string }

func (f fakeTree) Tree(_ context.Context, tok, owner, repo, ref string) (string, []auth.GitHubTreeEntry, error) {
	if tok != "tok" || owner != "acme" || repo != "agent" {
		return "", nil, errors.New("wrong repository or token")
	}
	sha := map[string]string{"main": "1111111111111111111111111111111111111111", "feature/x": "2222222222222222222222222222222222222222"}[ref]
	if sha == "" {
		return "", nil, errors.New("no such ref")
	}
	return "c" + sha[1:], []auth.GitHubTreeEntry{
		{Path: "AGENTS.md", Mode: "100644", Type: "blob", SHA: sha},
		{Path: "src/main.go", Mode: "100644", Type: "blob", SHA: "3333333333333333333333333333333333333333"},
	}, nil
}
func (f fakeTree) Blob(_ context.Context, _, _, _, sha string) ([]byte, error) {
	if sha[0] == '3' {
		return nil, errors.New("a file outside the agent paths was fetched")
	}
	return []byte(f.blobs[sha]), nil
}

func TestCompareRefsJobGitHubHosted(t *testing.T) {
	env, fl, dir, _, _ := quickRefsEnv(t)
	env.Hosted = true
	old := compareRefsGitHub
	compareRefsGitHub = func(context.Context, jobEnv) (githubTreeReader, string, error) {
		return fakeTree{blobs: map[string]string{
			"1111111111111111111111111111111111111111": "Be careful.\n",
			"2222222222222222222222222222222222222222": "Be careful. Never edit tests.\n",
		}}, "tok", nil
	}
	t.Cleanup(func() { compareRefsGitHub = old })
	_, res, err := runRefsJob(t, env, `{"agent":"agents/claude-code.yaml","ref_a":"main","ref_b":"feature/x","suite":"lib","budget_usd":2,"repo":"acme/agent","name":"gh"}`)
	if err != nil {
		t.Fatal(err)
	}
	r := res.(compareRefsResult)
	if !r.Started || r.Repo != "acme/agent" || len(fl.started) != 1 {
		t.Fatalf("result = %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".heimdall", "experiments", "fixtures", "gh", "B", "AGENTS.md"))
	if string(b) != "Be careful. Never edit tests.\n" {
		t.Fatalf("candidate notes = %q", b)
	}
	e := fl.planned[0]
	if lbl := e.Arms["A"].Label; !strings.HasPrefix(lbl, "main (") {
		t.Errorf("arm A label = %q", lbl)
	}
}

func TestBlameReplayJob(t *testing.T) {
	a, root := jobsProject(t)
	fl := &fakeRefsLauncher{active: map[string]int{}}
	env := jobEnv{App: a, OrgID: "local", ProjectRoot: root, Services: handlers.Services{Launcher: fl}}
	ctx := context.Background()
	if _, _, err := validateBlameReplayJob(ctx, env, json.RawMessage(`{"exp_id":"e_demo"}`)); !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "stub") {
		t.Fatalf("no stub: %v", err)
	}
	for _, raw := range []string{`{"exp_id":"e_demo","stub":"../evil.yaml"}`, `{"exp_id":"e_demo","stub":"agents/mine.yaml","max":99}`, `{"exp_id":"e_demo","run_id":"../r"}`} {
		if _, _, err := validateBlameReplayJob(ctx, env, json.RawMessage(raw)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if _, _, err := validateBlameReplayJob(ctx, env, json.RawMessage(`{"exp_id":"e_nope","stub":"agents/mine.yaml"}`)); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("unknown experiment: %v", err)
	}
	norm, target, err := validateBlameReplayJob(ctx, env, json.RawMessage(`{"exp_id":"e_demo","stub":"agents/mine.yaml"}`))
	if err != nil || target != "analysis:e_demo" {
		t.Fatalf("valid: %v %q", err, target)
	}
	raw, _ := json.Marshal(norm)
	res, err := runBlameReplayJob(ctx, env, &jobs.Job{Params: raw}, &lineLog{}, func(any) {})
	if err != nil {
		t.Fatal(err)
	}
	if r := res.(blameReplayResult); r.Candidates != 0 || len(r.Replays) != 0 || len(fl.planned) != 0 {
		t.Fatalf("an experiment without failures replayed something: %+v %+v", r, fl)
	}
	// waitExperiment follows the launcher until it reports the replay ended.
	fl.active["e_r"] = 2
	old := replayPoll
	replayPoll = 1
	t.Cleanup(func() { replayPoll = old })
	if err := waitExperiment(ctx, fl, "e_r", replayTimeout, replayPoll, &lineLog{}); err != nil || fl.active["e_r"] != 0 {
		t.Fatalf("wait: %v, polls left %d", err, fl.active["e_r"])
	}
}

func TestGapJobKindsRegistered(t *testing.T) {
	want := map[string]bool{kindExperimentCompareRefs: false, kindBlameReplay: false}
	for _, k := range jobKinds {
		if _, ok := want[k.Kind]; ok {
			want[k.Kind] = k.Local && k.Hosted && k.Validate != nil && k.Run != nil
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("%s not registered for local and hosted", k)
		}
	}
}
