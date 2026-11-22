package handlers

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
)

func rcRun(slot, task, cond string, attempt int, o schemas.Outcome, superseded bool) RunView {
	return RunView{Slot: schemas.Slot{SlotID: slot, Task: task, Condition: cond, Arm: "A", Counted: true},
		Run: schemas.Run{RunID: "r_" + slot + string(rune('0'+attempt)), Attempt: attempt, Outcome: o}, Sealed: true, Superseded: superseded}
}

func TestRerunExperimentNarrowsToFailures(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_p", Experiment: schemas.Experiment{ID: "e_p", Name: "nightly", EvaluationID: "ev_1",
		Primary:    schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
		Conditions: []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 2}, {ID: "model-down", Tasks: schemas.TaskSelector{All: true}, Trials: 1}},
		Extends:    &schemas.ExtendsRef{}}}
	runs := []RunView{
		rcRun("s1", "t1", "clean", 1, schemas.OutcomePass, false),
		rcRun("s2", "t2", "clean", 1, schemas.OutcomeFail, false),
		rcRun("s3", "t3", "model-down", 1, schemas.OutcomeTimeout, false),
		// superseded infra_error, then a pass: the slot passed
		rcRun("s4", "t4", "clean", 1, schemas.OutcomeInfraError, true),
		rcRun("s4", "t4", "clean", 2, schemas.OutcomePass, false),
	}
	e, n, err := RerunExperiment(m, runs, nil, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || e.RerunOf != "e_p" || !e.Exploratory() || e.ID != "" || e.Extends != nil || e.EvaluationID != "" {
		t.Fatalf("rerun %+v n %d", e, n)
	}
	if len(e.Conditions) != 2 || !slices.Equal(e.Conditions[0].Tasks.IDs, []string{"t2"}) || !slices.Equal(e.Conditions[1].Tasks.IDs, []string{"t3"}) || e.Conditions[0].Trials != 2 {
		t.Fatalf("conditions %+v", e.Conditions)
	}
	if m.Experiment.Conditions[0].Tasks.IDs != nil {
		t.Fatal("the parent's manifest was changed")
	}
	// Only timeouts: the primary condition is kept with the failing task.
	e, _, err = RerunExperiment(m, runs, []schemas.Outcome{schemas.OutcomeTimeout}, time.Unix(0, 0))
	if err != nil || e.Conditions[0].ID != "clean" || !slices.Equal(e.Conditions[0].Tasks.IDs, []string{"t3"}) {
		t.Fatalf("primary kept: %+v %v", e, err)
	}
	if _, _, err := RerunExperiment(m, runs[:1], nil, time.Now()); !errors.Is(err, runctl.ErrConflict) {
		t.Fatalf("nothing failed: %v", err)
	}
	if _, _, err := RerunExperiment(m, runs, []schemas.Outcome{schemas.OutcomePass}, time.Now()); !errors.Is(err, runctl.ErrInvalid) {
		t.Fatalf("pass accepted: %v", err)
	}
}

func TestCountedRunsSkipSuperseded(t *testing.T) {
	runs := []RunView{rcRun("s1", "t1", "clean", 1, schemas.OutcomeInfraError, true), rcRun("s1", "t1", "clean", 2, schemas.OutcomeFail, false)}
	got := countedRuns(runs)["s1"]
	if got.Run.Attempt != 2 {
		t.Fatalf("counted %+v", got.Run)
	}
}

func TestErrorPolicyTableDefaultsAndOverride(t *testing.T) {
	rows := schemas.ErrorPolicyTable(nil)
	if rows[0].Class != schemas.FailureAbortedInfra || rows[0].MaxAttempts != 3 || rows[3].Retry != schemas.RetryManual {
		t.Fatalf("defaults %+v", rows)
	}
	yes := true
	p := &schemas.ErrorPolicySpec{AbortedInfra: &schemas.ErrorRule{MaxAttempts: 5}, InfraError: &schemas.ErrorRule{Retry: &yes, MaxAttempts: 3}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	rows = schemas.ErrorPolicyTable(p)
	if rows[0].MaxAttempts != 5 || rows[3].Retry != schemas.RetryAuto || rows[3].MaxAttempts != 3 {
		t.Fatalf("override %+v", rows)
	}
	bad := &schemas.ErrorPolicySpec{InfraError: &schemas.ErrorRule{MaxAttempts: 9}}
	if bad.Validate() == nil {
		t.Fatal("out of range accepted")
	}
}

// logLauncher answers Controls with a paused state and a change log.
type logLauncher struct{ budgetLauncher }

func (l *logLauncher) Controls(context.Context, string) (*runctl.State, []runctl.Entry, error) {
	return &runctl.State{Settings: runctl.Settings{Mode: runctl.ModePaused, PausedBy: "usr_1"}},
		[]runctl.Entry{{Seq: 1, Actor: "usr_2", Action: "patch"}, {Seq: 2, Actor: "usr_1", Action: "pause"}, {Seq: 3, Actor: "usr_gone", Action: "resume"}}, nil
}

// TestControlsCarryActorNames: GET /controls names the user who paused and
// each change log actor (name, else email); unknown ids stay as they are,
// and a server with no member list (local mode) adds no names.
func TestControlsCarryActorNames(t *testing.T) {
	var asked string
	SetMemberNames(func(_ context.Context, orgID string) (map[string]string, error) {
		asked = orgID
		return map[string]string{"usr_1": "Ada Lovelace", "usr_2": "bo@example.com"}, nil
	})
	t.Cleanup(func() { SetMemberNames(nil) })
	h := &ExperimentsHandler{}
	m := &schemas.Manifest{ExpID: "e_1"}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{UserID: "usr_1", OrgID: "org_a"})
	out, err := h.controlsView(ctx, &logLauncher{}, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "org_a" || out.PausedByName != "Ada Lovelace" {
		t.Fatalf("paused by name %q (org %q)", out.PausedByName, asked)
	}
	if len(out.ActorNames) != 2 || out.ActorNames["usr_1"] != "Ada Lovelace" || out.ActorNames["usr_2"] != "bo@example.com" {
		t.Fatalf("actor names %v", out.ActorNames)
	}
	if out.Log[2].Actor != "usr_gone" {
		t.Fatalf("log changed: %+v", out.Log)
	}

	SetMemberNames(nil)
	out, err = h.controlsView(ctx, &logLauncher{}, m, nil)
	if err != nil || out.PausedByName != "" || out.ActorNames != nil {
		t.Fatalf("local mode named actors: %+v %v", out, err)
	}
}
