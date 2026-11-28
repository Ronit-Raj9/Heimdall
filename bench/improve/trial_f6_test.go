package improve_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

func trialSpec() *improve.Spec {
	s := &improve.Spec{Name: "trial-loop", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7,
		Preset: improve.PresetTrial, Editable: improve.Editable{Files: []string{improvetest.NotesPath}}}
	s.Defaults()
	return s
}

func TestTrialDefaultsAndLimits(t *testing.T) {
	s := trialSpec()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Rounds != 1 || s.MaxTasks != improve.DefaultTrialTasks || len(s.Controls) != 0 || s.Budget.TotalUSD != 3 || s.Claim.Extension {
		t.Fatalf("trial defaults: %+v", s)
	}
	for _, bad := range []func(*improve.Spec){
		func(s *improve.Spec) { s.MaxTasks = 5 },
		func(s *improve.Spec) { s.MaxTasks = 41 },
		func(s *improve.Spec) { s.Rounds = 4 },
		func(s *improve.Spec) { s.Improver.Candidates = 3 },
	} {
		c := trialSpec()
		bad(c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "A trial") {
			t.Errorf("not refused in plain words: %v", err)
		}
	}
}

// An honest improver that the full gate would keep: a trial still keeps
// nothing, spends no look, and its verdict claims no gain.
func TestTrialNeverRecordsAGain(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40}
	r := &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x, RefuseUndecidable: true}
	s := trialSpec()
	s.Rounds = 3
	s.Improver.BudgetUSD = 2 // the scripted improver spends more than a real trial cap
	rec := createRun(t, r, s)
	if n := len(rec.Splits.Dev) + len(rec.Splits.Audit) + len(rec.Splits.Sealed); n != improve.DefaultTrialTasks {
		t.Fatalf("pool %d, want %d", n, improve.DefaultTrialTasks)
	}
	for _, rd := range rec.Rounds {
		if rd.Decision == improve.DecisionCommitted {
			t.Fatalf("round %d committed in a trial: %v", rd.N, decisions(rec))
		}
		if rd.Candidate != nil && (rd.Commit == nil || !rd.Commit.Skipped || rd.Why != improve.TrialDecision) {
			t.Fatalf("round %d gate: %+v %q", rd.N, rd.Commit, rd.Why)
		}
	}
	if len(rec.Looks) != 0 || rec.Incumbent.Number != rec.Base.Number {
		t.Fatalf("looks %d incumbent v%d", len(rec.Looks), rec.Incumbent.Number)
	}
	v := rec.Verdict
	if v == nil || v.State != improve.VerdictNotClaimable || v.Headline != improve.TrialText || v.Commits != 0 {
		t.Fatalf("verdict %+v", v)
	}
	if improve.CanContinue(rec) == nil {
		t.Error("a trial offers a second look")
	}
}

// The server refuses at Create a full size whose gate can never decide,
// before anything is stored; the same tasks as a trial are accepted.
func TestCreateRefusesUndecidableSize(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40}
	r := &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x, RefuseUndecidable: true}
	s := testSpec()
	s.MaxTasks = 12
	_, err := r.Create(context.Background(), s, "ana")
	if !errors.Is(err, improve.ErrBadSize) || !strings.Contains(err.Error(), "held-out tasks") {
		t.Fatalf("want a size refusal, got %v", err)
	}
	if list, _ := r.List(); len(list) != 0 {
		t.Fatalf("a refused run was stored: %d", len(list))
	}
	if _, err := r.Create(context.Background(), trialSpec(), "ana"); err != nil {
		t.Fatalf("trial refused: %v", err)
	}
}
