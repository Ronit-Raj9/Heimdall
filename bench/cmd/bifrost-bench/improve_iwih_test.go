package main

// Group H integration tests: the improvement runner, the engine-backed
// executor and the server hooks meet here for the first time.

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// iwihSkillPath is the skill the improver adds in round 1. Its "{{x}}" must
// be stored and delivered literally.
const iwihSkillPath = "/home/agent/skills/run/SKILL.md"

const iwihSkillText = "---\nname: run\ndescription: run the tests\n---\nRun {{x}} before you finish.\n"

// iwihExec scores and improves like the scripted executor, but loads and
// freezes versions through the engine-backed executor, so every round's
// bundle hash is checked by the real Freeze.
type iwihExec struct {
	*improvetest.Exec
	real improveExecutor

	// realCheck runs the engine-backed CheckImprover at create.
	realCheck bool

	mu      sync.Mutex
	v0      *improve.Version
	frozen  []improve.Version
	freezes int
}

func (h *iwihExec) Load(_ context.Context, _ string, ed improve.Editable) (improve.Version, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.v0 != nil {
		return *h.v0, nil
	}
	v, err := h.real.snapshot("scripted-improver", []byte(improvetest.AgentDoc), ed)
	if err == nil {
		h.v0 = &v
	}
	return v, err
}

func (h *iwihExec) Freeze(ctx context.Context, parent improve.Version, doc []byte, lin improve.Lineage) (improve.Version, error) {
	v, err := h.real.Freeze(ctx, parent, doc, lin)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.freezes++
	if err == nil {
		h.frozen = append(h.frozen, v)
	}
	return v, err
}

func (h *iwihExec) CheckImprover(ctx context.Context, spec *improve.Spec) error {
	if h.realCheck {
		return h.real.CheckImprover(ctx, spec)
	}
	return nil
}

// Improve adds the skill in round 1 (as a new path) on top of the scripted
// honest improver's notes change; later rounds only change the notes.
func (h *iwihExec) Improve(ctx context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	res, err := h.Exec.Improve(ctx, req)
	if err != nil || req.Null {
		return res, err
	}
	if req.Round == 1 {
		res.Bundle[iwihSkillPath] = iwihSkillText
	}
	return res, nil
}

func iwihExecutor(t *testing.T) (*iwihExec, string) {
	t.Helper()
	root := t.TempDir()
	st, err := store.NewFSStore(filepath.Join(root, "runs"), utils.NewStdLogger(io.Discard, false))
	if err != nil {
		t.Fatal(err)
	}
	h := &iwihExec{Exec: &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120},
		real: improveExecutor{a: &app{cfg: &lib.Config{Root: root}, store: st}}}
	return h, root
}

// Item 1 of the group H merge (I39 to I40): a two-round run whose round 1
// adds a skill freezes every idea through the engine-backed Freeze, which
// re-reads the bundle with the spec's editable set and checks its hash; the
// round 2 idea, built on the round 1 version, keeps the skill byte for byte.
func TestImproveTwoRoundsFreezeAndVerifyNewSkill(t *testing.T) {
	h, root := iwihExecutor(t)
	r := &improve.Runner{Root: filepath.Join(root, "runs", improve.DefaultDir), Exec: h,
		Objects: improve.Objects{Root: filepath.Join(root, "runs", improve.DefaultDir, "objects")}}
	spec := &improve.Spec{Name: "freeze-loop", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7, Rounds: 2,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}, Dirs: []string{"/home/agent/skills/"}, Instructions: true},
		Commit:   improve.CommitSpec{Alpha: 0.10, Trials: 2}, Sealed: improve.PhaseSpec{Trials: 2}}
	spec.Defaults()
	rec, err := r.Create(context.Background(), spec, "ana")
	if err != nil {
		t.Fatal(err)
	}
	rec, err = r.Run(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rec.Status != improve.StatusDone {
		t.Fatalf("status %s: %s %s", rec.Status, rec.Reason, rec.Error)
	}
	if len(rec.Rounds) < 2 || rec.Rounds[0].Decision != improve.DecisionCommitted {
		t.Fatalf("rounds %d, round 1 %s (%s)", len(rec.Rounds), rec.Rounds[0].Decision, rec.Rounds[0].Why)
	}
	var r2 *improve.Version
	for _, v := range h.frozen {
		if strings.Contains(v.Bundle[improvetest.NotesPath], "level: 6") {
			vv := v
			r2 = &vv
		}
	}
	if r2 == nil {
		t.Fatalf("no round 2 idea was frozen (%d freezes, %d kept)", h.freezes, len(h.frozen))
	}
	if r2.Bundle[iwihSkillPath] != iwihSkillText {
		t.Errorf("round 2 version lost the round 1 skill: %q", r2.Bundle[iwihSkillPath])
	}
	doc, _, err := agentversions.Read(root, "scripted-improver", r2.Number)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "Run {{x}} before you finish.") || !strings.Contains(string(doc), "files_raw:") {
		t.Errorf("stored version does not hold the skill as a raw file:\n%s", doc)
	}
	again, err := improve.NewVersion(r2.Ref, r2.Number, doc, spec.Editable)
	if err != nil || again.BundleHash != r2.BundleHash {
		t.Errorf("re-read hash %s, frozen %s (%v)", again.BundleHash, r2.BundleHash, err)
	}
}

// Item 2 (I41 to I39): the server build embeds the shipped improver suite.
func TestImproverSuiteFSSet(t *testing.T) {
	if improverSuiteFS == nil {
		t.Fatal("improverSuiteFS is nil")
	}
	for _, p := range []string{"suite.yaml", "README.md", "tasks/reflect-edit/task.yaml", "tasks/reflect-edit/instruction.md", "tasks/reflect-edit/repo/.keep"} {
		if _, err := fs.Stat(improverSuiteFS, p); err != nil {
			t.Errorf("embedded suite misses %s: %v", p, err)
		}
	}
}

// withSuiteFS swaps the shipped suite for the test.
func withSuiteFS(t *testing.T, f fs.FS) {
	t.Helper()
	old := improverSuiteFS
	improverSuiteFS = f
	t.Cleanup(func() { improverSuiteFS = old })
}

// admittedSuiteFS is the embedded suite plus a suite.lock, as the build is
// once the lead commits the lock.
func admittedSuiteFS(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{"suite.lock": {Data: []byte("lock v1\n")}}
	err := fs.WalkDir(improverSuiteFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(improverSuiteFS, p)
		m[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Item 2: with an admitted build, a project that has never seen the
// improver suite gets it on first use, with no setup.
func TestEnsureImproverSuiteZeroSetup(t *testing.T) {
	withSuiteFS(t, admittedSuiteFS(t))
	root := t.TempDir()
	if err := ensureImproverSuite(root); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, improverSuiteDir)
	for _, p := range []string{"suite.lock", "suite.yaml", "tasks/reflect-edit/task.yaml", "tasks/reflect-edit/repo/.keep"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); err != nil {
			t.Errorf("not copied: %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "embed.go")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Go source copied into the project: %v", err)
	}
	if err := ensureImproverSuite(root); err != nil {
		t.Errorf("second call: %v", err)
	}
}

// Item 13: the build has no suite.lock until the lead admits the suite; the
// improver check then refuses in plain words and copies nothing, unless the
// project holds an admitted copy of its own.
func TestEnsureImproverSuiteNotAdmitted(t *testing.T) {
	root := t.TempDir()
	if _, err := fs.Stat(improverSuiteFS, "suite.lock"); err == nil {
		t.Skip("this build ships an admitted improver suite")
	}
	if err := ensureImproverSuite(root); !errors.Is(err, errImproverNotAdmitted) {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, improverSuiteDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("suite copied without a lock: %v", err)
	}
	own := filepath.Join(root, improverSuiteDir, "suite.lock")
	if err := os.MkdirAll(filepath.Dir(own), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("admitted here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureImproverSuite(root); err != nil {
		t.Errorf("project copy refused: %v", err)
	}
}

// Security review: create refuses, before any version is stored and before
// any Score or Improve call, an agent that sets its own gateway spending
// limit and a default improver whose benchmark was never admitted.
func TestImproveCreateRefusesBeforeSpend(t *testing.T) {
	const reflector = "id: heimdall-reflector\nadapter: command\nimage: img\nargv: [run]\nmodel: m\n"
	const capped = "gateway:\n  routing:\n    limits:\n      budget_usd: 5\n"
	for _, c := range []struct {
		name, agent, doc, reflector string
		want                        error
	}{
		{"own limit", "agents/capped.yaml", "id: capped\nadapter: scripted\nmodel: m\n" + capped, reflector, errOwnSpendLimit},
		{"reflector limit", "agents/scripted-improver.yaml", improvetest.AgentDoc, reflector + capped, errReflectorSpendLimit},
		{"not admitted", "agents/scripted-improver.yaml", improvetest.AgentDoc, reflector, errImproverNotAdmitted},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.want == errImproverNotAdmitted {
				if _, err := fs.Stat(improverSuiteFS, "suite.lock"); err == nil {
					t.Skip("this build ships an admitted improver suite")
				}
			}
			h, root := iwihExecutor(t)
			h.realCheck = true
			writeIwihFile(t, root, c.agent, c.doc)
			writeIwihFile(t, root, "agents/heimdall-reflector.yaml", c.reflector)
			r := &improve.Runner{Root: filepath.Join(root, "runs", improve.DefaultDir), Exec: h}
			spec := &improve.Spec{Name: "refused", Agent: c.agent, Suite: "scripted@1.0.0", Seed: 7,
				Editable: improve.Editable{Files: []string{improvetest.NotesPath}}}
			_, err := r.Create(context.Background(), spec, "ana")
			if !errors.Is(err, improve.ErrBadImprover) || !errors.Is(err, c.want) || err.Error() != c.want.Error() {
				t.Fatalf("err %v, want %v", err, c.want)
			}
			if h.v0 != nil || len(h.Calls) != 0 {
				t.Errorf("work done before the refusal: version %v calls %v", h.v0, h.Calls)
			}
			if recs, _ := r.List(); len(recs) != 0 {
				t.Errorf("records after the refusal: %d", len(recs))
			}
		})
	}
}

// Item 3 (I42 to I39): the server's runner sends improvement.paused when a
// real run pauses at a tripwire, through the hook improveOnEvent.
func TestPausedImprovementDispatchesItsEvent(t *testing.T) {
	if improveOnEvent == nil {
		t.Fatal("improveOnEvent is not set")
	}
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	root := t.TempDir()
	url := srv.URL
	if _, err := webhookStore(auth.LocalOrgID, root, false).Create(webhooks.Input{URL: &url,
		Events: &[]string{webhooks.EventImprovementFinished, webhooks.EventImprovementPaused}}, "u1", true); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFSStore(filepath.Join(root, "runs"), utils.NewStdLogger(io.Discard, false))
	if err != nil {
		t.Fatal(err)
	}
	r := newImproveRunner(&app{cfg: &lib.Config{Root: root}, store: st})
	if r.OnEvent == nil {
		t.Fatal("newImproveRunner left OnEvent nil")
	}
	x := &improvetest.Exec{Behavior: improvetest.Cheat, Base: 2, NTasks: 40}
	r.Exec = x
	spec := &improve.Spec{Name: "pause-ev", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7, Rounds: 2,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}},
		Commit:   improve.CommitSpec{Alpha: 0.10, Trials: 2}, Sealed: improve.PhaseSpec{Trials: 2}}
	rec, err := r.Create(context.Background(), spec, "ana")
	if err != nil {
		t.Fatal(err)
	}
	rec, err = r.Run(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != improve.StatusAwaiting {
		t.Fatalf("status %s (%s), want a pause", rec.Status, rec.Reason)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || !strings.Contains(got[0], `"improvement.paused"`) || !strings.Contains(got[0], rec.ID) {
		t.Fatalf("events %q", got)
	}
}

func writeIwihFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Security review: a run whose improver agent is not installed is refused
// at create, before any version is stored or any run is paid for.
func TestImproveCreateRefusesMissingReflector(t *testing.T) {
	t.Setenv(suite.LibraryEnv, "")
	h, root := iwihExecutor(t)
	h.realCheck = true
	writeIwihFile(t, root, "agents/scripted-improver.yaml", improvetest.AgentDoc)
	r := &improve.Runner{Root: filepath.Join(root, "runs", improve.DefaultDir), Exec: h}
	spec := &improve.Spec{Name: "no-reflector", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}}}
	_, err := r.Create(context.Background(), spec, "ana")
	if !errors.Is(err, improve.ErrBadImprover) || !strings.Contains(err.Error(), "the improver agent heimdall-reflector is not installed") {
		t.Fatalf("err %v", err)
	}
	if h.v0 != nil || len(h.Calls) != 0 {
		t.Errorf("work done before the refusal: version %v calls %v", h.v0, h.Calls)
	}
}
