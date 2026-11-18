package engine

import (
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// trifectaSuite has a desk task with all three legs, a read-only task with a
// proxied service but no exit, and a plain coding task.
func trifectaSuite() *schemas.Suite {
	desk := schemas.ServiceSpec{ID: "desk", Role: "mcp", Export: []schemas.StateExport{{ID: "db.json", Path: "/state/db.json"}}}
	s := &schemas.Suite{
		ID: "acme-safety",
		Tasks: []*schemas.Task{
			{ID: "card-update", Services: []schemas.ServiceSpec{desk}, External: []schemas.ExternalAPI{{ID: "partner"}}},
			{ID: "lookup", Services: []schemas.ServiceSpec{desk}},
			{ID: "tagged", Capabilities: []string{"private_data", "untrusted_input", "external_comms"}},
			{ID: "coding"},
		},
		TaskIDs: []string{"card-update", "lookup", "tagged", "coding"},
		Probes:  []*schemas.ProbeSpec{{ID: "exfil-ticket", Axis: "exfiltration"}, {ID: "tool-error-first", Axis: "tools"}},
	}
	return s
}

func TestInferTrifecta(t *testing.T) {
	s := trifectaSuite()
	tests := []struct {
		task                  string
		private, untrust, out bool
	}{
		{"card-update", true, true, true},
		{"lookup", true, true, false},
		{"tagged", true, true, true},
		{"coding", false, false, false},
	}
	for _, tt := range tests {
		l := InferTrifecta(s, s.TaskByID(tt.task))
		if l.PrivateData != tt.private || l.UntrustedInput != tt.untrust || l.Exit != tt.out {
			t.Errorf("%s: %+v", tt.task, l)
		}
	}
	answer := &schemas.Task{ID: "a", Services: []schemas.ServiceSpec{{ID: "desk", Role: "mcp", Export: []schemas.StateExport{{ID: "x"}}}}, Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}
	if !InferTrifecta(&schemas.Suite{}, answer).Exit {
		t.Error("a rendered final answer is an exit")
	}
}

func TestApplySafetyTrifecta(t *testing.T) {
	e := &schemas.Experiment{Safety: schemas.SafetyTrifecta, Conditions: []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 2}}}
	notes, err := applySafetyTrifecta(trifectaSuite(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Conditions) != 2 || e.Conditions[1].ID != "exfil-ticket" || e.Conditions[1].Trials != 2 {
		t.Fatalf("conditions %+v", e.Conditions)
	}
	if got := e.Conditions[1].Tasks.IDs; !slices.Equal(got, []string{"card-update", "tagged"}) {
		t.Fatalf("tasks %v", got)
	}
	if !slices.Contains(e.ExtraGates, "exfiltration_attempted") {
		t.Fatalf("gates %v", e.ExtraGates)
	}
	if len(notes) != 3 || !strings.Contains(notes[0], "card-update") {
		t.Fatalf("notes %v", notes)
	}
	t.Run("existing condition gains tasks", func(t *testing.T) {
		e := &schemas.Experiment{Safety: "trifecta", Conditions: []schemas.ConditionSpec{
			{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1},
			{ID: "exfil-ticket", Tasks: schemas.TaskSelector{IDs: []string{"tagged"}}, Trials: 1},
		}}
		if _, err := applySafetyTrifecta(trifectaSuite(), e); err != nil {
			t.Fatal(err)
		}
		if got := e.Conditions[1].Tasks.IDs; !slices.Equal(got, []string{"tagged", "card-update"}) {
			t.Fatalf("tasks %v", got)
		}
	})
	t.Run("off", func(t *testing.T) {
		e := &schemas.Experiment{Conditions: []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{All: true}, Trials: 1}}}
		if notes, err := applySafetyTrifecta(trifectaSuite(), e); err != nil || notes != nil || len(e.Conditions) != 1 {
			t.Fatalf("%v %v %+v", notes, err, e.Conditions)
		}
	})
	t.Run("no exfiltration probe", func(t *testing.T) {
		s := trifectaSuite()
		s.Probes = s.Probes[1:]
		if _, err := applySafetyTrifecta(s, &schemas.Experiment{Safety: "trifecta"}); err == nil {
			t.Fatal("want error")
		}
	})
}
