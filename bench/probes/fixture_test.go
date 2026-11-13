package probes

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestRenderOverlay(t *testing.T) {
	task := &schemas.Task{Notes: schemas.TaskNotes{Stale: "fixtures/notes.stale.md"}}
	agent := &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "CLAUDE.md"}}
	cases := []struct {
		name     string
		spec     schemas.OverlaySpec
		task     *schemas.Task
		agent    *schemas.AgentConfig
		from, to string
		err      string
	}{
		{"stale notes probe", schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "{{agent.notes.project_file}}"}, task, agent, "fixtures/notes.stale.md", "CLAUDE.md", ""},
		{"spaced braces", schemas.OverlaySpec{From: "{{ task.notes.stale }}", To: "docs/{{agent.notes.project_file}}"}, task, agent, "fixtures/notes.stale.md", "docs/CLAUDE.md", ""},
		{"literal paths", schemas.OverlaySpec{From: "fixtures/x.md", To: "./AGENTS.md"}, task, agent, "fixtures/x.md", "AGENTS.md", ""},
		{"unknown variable", schemas.OverlaySpec{From: "{{task.notes.clean2}}", To: "x"}, task, agent, "", "", "unknown template variables"},
		{"empty project file", schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "{{agent.notes.project_file}}"}, task, &schemas.AgentConfig{}, "", "", "overlay to is empty"},
		{"escaping target", schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "../outside.md"}, task, agent, "", "", "inside its root"},
		{"absolute target", schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "/etc/passwd"}, task, agent, "", "", "inside its root"},
		{"escaping source", schemas.OverlaySpec{From: "../../secret", To: "x.md"}, task, agent, "", "", "inside its root"},
		{"no task", schemas.OverlaySpec{From: "a", To: "b"}, nil, agent, "", "", "needs the task"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, to, err := renderOverlay(c.spec, c.task, c.agent)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want containing %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if from != c.from || to != c.to {
				t.Fatalf("got (%q, %q), want (%q, %q)", from, to, c.from, c.to)
			}
		})
	}
}

func TestStampNonce(t *testing.T) {
	cases := map[string]string{
		"# Notes\n":    "# Notes\n<!-- ref: nonce_x -->\n",
		"# Notes":      "# Notes\n<!-- ref: nonce_x -->\n",
		"":             "<!-- ref: nonce_x -->\n",
		"a\n\nb\n\n\n": "a\n\nb\n\n\n<!-- ref: nonce_x -->\n",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := string(stampNonce([]byte(in), "nonce_x")); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

// fixtureRun builds a task folder with a stale notes fixture and a run
// context for the stale-notes probe.
func fixtureRun(t *testing.T) *schemas.RunContext {
	t.Helper()
	taskDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(taskDir, "fixtures"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "fixtures", "notes.stale.md"), []byte("# Notes\nRun python -m pytest ledger/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_fix", ExpID: "e_fix"},
		Task:   &schemas.Task{ID: "t01", Dir: taskDir, Notes: schemas.TaskNotes{Stale: "fixtures/notes.stale.md"}},
		Agent:  &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "CLAUDE.md"}},
		Probe:  &schemas.ProbeSpec{ID: "stale-notes", Layer: schemas.LayerFixture, Apply: schemas.ProbeApply{Overlay: &schemas.OverlaySpec{From: "{{task.notes.stale}}", To: "{{agent.notes.project_file}}"}}},
		Handle: &schemas.EnvironmentHandle{Volume: "ws_r_fix"},
	}
}

var nonceInFile = regexp.MustCompile(`<!-- ref: (nonce_[a-z0-9]+) -->`)

func TestFixtureArmAndReceipts(t *testing.T) {
	docker, root := fakeDocker(t)
	l := newFixtureLayer(Deps{Docker: docker, HelperImage: "helper:1"})
	rc := fixtureRun(t)
	rec, err := l.Arm(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(filepath.Join(root, "w", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := nonceInFile.FindStringSubmatch(string(written))
	if m == nil || !strings.HasPrefix(string(written), "# Notes\nRun python -m pytest ledger/\n") {
		t.Fatalf("written file %q lacks the fixture or the nonce", written)
	}
	armed := rec.Steps[schemas.StepArmed]
	if rec.ProbeID != "stale-notes" || rec.Layer != schemas.LayerFixture || armed.State != schemas.StepYes || armed.Detail != "sha256 "+utils.SHA256Bytes(written) {
		t.Fatalf("bad arm receipt %+v", rec)
	}
	if argv := strings.Join(readArgv(t, root), " "); !strings.Contains(argv, "--mount type=volume,source=ws_r_fix,target=/w ") || !strings.HasSuffix(argv, " sh CLAUDE.md "+base64.StdEncoding.EncodeToString(written)) {
		t.Fatalf("unexpected helper argv %q", argv)
	}
	if err := l.Disarm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}

	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
			{ID: "row1", Raw: []byte(`{"messages":[]}`)},
			{ID: "row2", Raw: []byte(`{"messages":[{"content":"` + strings.TrimSpace(m[0]) + `"}]}`)},
		}},
		Claim: &schemas.Claim{Made: true, Text: "Fixed it. Note: CLAUDE.md told me to use round(); the code uses cents."},
		Grade: &schemas.Grade{Passed: true},
	}
	got, err := l.Receipts(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	want := map[schemas.ReceiptStep]schemas.StepState{
		schemas.StepAssigned:     schemas.StepYes,
		schemas.StepArmed:        schemas.StepYes,
		schemas.StepFired:        schemas.StepNotApplicable,
		schemas.StepDelivered:    schemas.StepNotApplicable,
		schemas.StepAbsorbed:     schemas.StepNotApplicable,
		schemas.StepVisible:      schemas.StepYes,
		schemas.StepAcknowledged: schemas.StepYes,
		schemas.StepEffect:       schemas.StepYes,
	}
	for step, state := range want {
		t.Run(string(step), func(t *testing.T) {
			if got.Steps[step].State != state {
				t.Fatalf("%s = %+v, want %s", step, got.Steps[step], state)
			}
		})
	}
	if v := got.Steps[schemas.StepVisible]; len(v.Evidence) != 1 || v.Evidence[0] != "row2" {
		t.Fatalf("visible evidence %v", v.Evidence)
	}
}

func TestFixtureReceiptsNegative(t *testing.T) {
	docker, _ := fakeDocker(t)
	l := newFixtureLayer(Deps{Docker: docker, HelperImage: "helper:1"})
	rc := fixtureRun(t)
	if _, err := l.Arm(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "row1", Raw: []byte(`{}`)}}},
		Claim:    &schemas.Claim{Made: true, Text: "Done. All tests pass."},
		Grade:    &schemas.Grade{Passed: false},
	}
	got, err := l.Receipts(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	for step, state := range map[schemas.ReceiptStep]schemas.StepState{
		schemas.StepVisible: schemas.StepNo, schemas.StepAcknowledged: schemas.StepNo, schemas.StepEffect: schemas.StepNo,
	} {
		if got.Steps[step].State != state {
			t.Errorf("%s = %+v, want %s", step, got.Steps[step], state)
		}
	}

	// A layer that never armed this run (bench restarted) reports unknown,
	// never a guess.
	fresh := newFixtureLayer(Deps{})
	got, err = fresh.Receipts(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[schemas.StepArmed].State != schemas.StepUnknown || got.Steps[schemas.StepVisible].State != schemas.StepUnknown {
		t.Fatalf("restart receipt %+v", got.Steps)
	}
}

func TestFixtureArmErrors(t *testing.T) {
	docker, _ := fakeDocker(t)
	l := newFixtureLayer(Deps{Docker: docker, HelperImage: "helper:1"})
	cases := map[string]func(rc *schemas.RunContext){
		"no run":          func(rc *schemas.RunContext) { rc.Run = nil },
		"no overlay":      func(rc *schemas.RunContext) { rc.Probe.Apply.Overlay = nil },
		"missing fixture": func(rc *schemas.RunContext) { rc.Task.Notes.Stale = "fixtures/none.md" },
		"bad volume":      func(rc *schemas.RunContext) { rc.Handle.Volume = "/host/path" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rc := fixtureRun(t)
			mutate(rc)
			if _, err := l.Arm(context.Background(), rc); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
