package main

// Group H integration tests: the improvement runner, the engine-backed
// executor and the server hooks meet here for the first time.

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
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
