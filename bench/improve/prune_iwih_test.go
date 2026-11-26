package improve_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

const pruneSkill = "/home/agent/skills/old/SKILL.md"

// pruneExec is the scripted executor with an agent that ships a skill, an
// improver that answers with edits (one rule per round), and a score that
// counts the agent's playbook rules: each rule is worth three levels.
type pruneExec struct {
	*improvetest.Exec
}

var pruneDoc = strings.Replace(improvetest.AgentDoc, "  /etc/agent/policy.txt: fixed\n",
	"  /etc/agent/policy.txt: fixed\n  "+pruneSkill+": |\n    ---\n    name: old\n    description: an old skill\n    ---\n    Do the old thing.\n", 1)

func (x pruneExec) Load(ctx context.Context, ref string, ed improve.Editable) (improve.Version, error) {
	if _, err := x.Exec.Load(ctx, ref, ed); err != nil {
		return improve.Version{}, err
	}
	return improve.NewVersion("agents/scripted-improver.yaml@v1", 1, []byte(pruneDoc), ed)
}

func (x pruneExec) Freeze(ctx context.Context, parent improve.Version, doc []byte, lin improve.Lineage) (improve.Version, error) {
	v, err := x.Exec.Freeze(ctx, parent, doc, lin)
	if err != nil {
		return v, err
	}
	return improve.NewVersion(v.Ref, v.Number, doc, lin.Editable)
}

func (x pruneExec) Improve(ctx context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	res, err := x.Exec.Improve(ctx, req)
	if err != nil || req.Null {
		return res, err
	}
	res.Bundle = req.Incumbent.Bundle.Clone()
	res.Edits = &improve.EditsFile{Edits: []improve.Edit{{Op: "add_item", File: improvetest.NotesPath, Section: "Checks",
		Text: fmt.Sprintf("Check the work twice, round %d.", req.Round), Evidence: []string{"dev:task-pool-01#1/A"}, Reason: "more care"}}}
	return res, nil
}

func (x pruneExec) Score(ctx context.Context, req improve.ScoreRequest) (*improve.ScoreResult, error) {
	arms := make([]improve.Version, len(req.Arms))
	for i, a := range req.Arms {
		a.Bundle = a.Bundle.Clone()
		a.Bundle[improvetest.NotesPath] = fmt.Sprintf("level: %d\n", 3*strings.Count(a.Bundle[improvetest.NotesPath], "[pb-"))
		arms[i] = a
	}
	req.Arms = arms
	return x.Exec.Score(ctx, req)
}

// neverOpened is an evidence builder whose practice runs (of every
// version) never open a skill.
type neverOpened struct{}

func (neverOpened) Build(_ context.Context, req improve.EvidenceRequest) (*improve.EvidenceDoc, error) {
	doc := &improve.EvidenceDoc{EvidenceJSON: []byte(`{"items":[]}`)}
	for v := 1; v <= 20; v++ {
		doc.Items = append(doc.Items, improve.EvidenceItem{Task: "task-pool-01", RunRef: "dev:task-pool-01#1/A", Version: v})
	}
	return doc, nil
}

// Item 4 of the group H merge (design A3.3): a skill no practice run opened
// for 2 rounds is retired by a prune edit added to slot 1's edit set, and
// not before.
func TestPruneRetiresUnopenedSkillOnSlotOne(t *testing.T) {
	x := pruneExec{&improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}}
	dir := filepath.Join(t.TempDir(), improve.DefaultDir)
	r := &improve.Runner{Root: dir, Exec: x, Evidence: neverOpened{}, Objects: improve.Objects{Root: filepath.Join(dir, "objects")}}
	rec := createRun(t, r, w4Spec(func(s *improve.Spec) {
		s.Rounds = 3
		s.Editable = improve.Editable{Files: []string{improvetest.NotesPath}, Dirs: []string{"/home/agent/skills/"}}
	}))
	if len(rec.Rounds) != 3 {
		t.Fatalf("rounds %d: %v (%s)", len(rec.Rounds), decisions(rec), rec.Reason)
	}
	retired := func(rd *improve.Round) bool {
		for _, e := range rd.Candidates[0].Edits {
			if e.Op == "retire_skill" && e.Path == pruneSkill && e.Result == "applied" {
				return true
			}
		}
		return false
	}
	for i, rd := range rec.Rounds[:2] {
		if retired(rd) {
			t.Errorf("round %d retired the skill too early", i+1)
		}
		if rd.Decision != improve.DecisionCommitted {
			t.Fatalf("round %d %s (%s)", i+1, rd.Decision, rd.Why)
		}
	}
	rd := rec.Rounds[2]
	if !retired(rd) {
		t.Fatalf("round 3 slot 1 edits %+v", rd.Candidates[0].Edits)
	}
	if c := rd.Candidates[0].Version; c == nil || c.Bundle[pruneSkill] != "" {
		t.Errorf("round 3 idea still holds the skill")
	}
	if c := rd.Candidates[0].Version; c == nil || strings.Count(c.Bundle[improvetest.NotesPath], "[pb-") != 3 {
		t.Errorf("round 3 idea lost the improver's own edits")
	}
}

// An improver whose edit set applies nothing ("no_edits", or every edit
// refused) drops its idea as "changed nothing"; it is not a tripwire and
// the run does not pause for an approval.
func TestNothingAppliedIsNotATripwire(t *testing.T) {
	x := noEditsExec{&improvetest.Exec{Behavior: improvetest.Honest, Base: 2, NTasks: 40}}
	r := &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x}
	rec := createRun(t, r, w4Spec(func(s *improve.Spec) { s.Rounds = 1 }))
	if rec.Status != improve.StatusDone {
		t.Fatalf("status %s (%s)", rec.Status, rec.Reason)
	}
	c := rec.Rounds[0].Candidates[0]
	if len(c.Tripwires) != 0 || c.Dropped != "the improver changed nothing" {
		t.Errorf("idea %+v", c)
	}
}

type noEditsExec struct{ *improvetest.Exec }

func (x noEditsExec) Improve(ctx context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	res, err := x.Exec.Improve(ctx, req)
	if err != nil || req.Null {
		return res, err
	}
	res.Bundle = req.Incumbent.Bundle.Clone()
	res.Edits = &improve.EditsFile{NoEdits: "nothing to change", Rationale: "nothing to change", Edits: []improve.Edit{}}
	return res, nil
}

// I41 to I40: an edit that cites only a run the evidence never showed is
// refused as citing no practice run.
func TestMadeUpRefsAreNotEvidence(t *testing.T) {
	x := madeUpExec{pruneExec{&improvetest.Exec{Behavior: improvetest.Honest, Base: 2, NTasks: 40}}}
	dir := filepath.Join(t.TempDir(), improve.DefaultDir)
	r := &improve.Runner{Root: dir, Exec: x, Evidence: neverOpened{}, Objects: improve.Objects{Root: filepath.Join(dir, "objects")}}
	rec := createRun(t, r, w4Spec(func(s *improve.Spec) { s.Rounds = 1 }))
	c := rec.Rounds[0].Candidates[0]
	if len(c.Edits) != 1 || c.Edits[0].Result != "refused" || c.Edits[0].Why != "the edit cites no practice run" {
		t.Fatalf("edits %+v (dropped %q)", c.Edits, c.Dropped)
	}
}

type madeUpExec struct{ pruneExec }

func (x madeUpExec) Improve(ctx context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	res, err := x.pruneExec.Improve(ctx, req)
	if err == nil && res.Edits != nil {
		res.Edits.Edits[0].Evidence = []string{"dev:made-up#1/B"}
	}
	return res, err
}
