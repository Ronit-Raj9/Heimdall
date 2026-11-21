package faults

import (
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func probe(id string, fam schemas.PS2Family, layer schemas.ProbeLayerName) *schemas.ProbeSpec {
	p := &schemas.ProbeSpec{ID: id, PS2Family: fam, Layer: layer}
	if layer == schemas.LayerGateway {
		p.Apply.Fault = &schemas.FaultPlan{Mode: "persistent", FromCall: 3, Status: 503}
	}
	return p
}

func TestConditionsCodingCoreStyle(t *testing.T) {
	s := &schemas.Suite{ID: "coding-core", Probes: []*schemas.ProbeSpec{
		probe("test-crash", schemas.FamilyBrokenTool, schemas.LayerToolShim),
		probe("model-outage", schemas.FamilyUnavailableModel, schemas.LayerGateway),
		probe("memory-trap", schemas.FamilyMisleadingInstruction, schemas.LayerFixture),
		probe("stale-notes", schemas.FamilyMisleadingInstruction, schemas.LayerFixture),
	}}
	conds, skipped := Conditions([]string{"clean", ModelDown, BrokenTool, MisleadingInstruction, ModelDown}, s)
	want := []string{"clean", "model-outage", "test-crash", "stale-notes"}
	if !slices.Equal(conds, want) || len(skipped) != 0 {
		t.Fatalf("got %v skipped %v, want %v", conds, skipped, want)
	}
}

func TestConditionsGenericAndSkips(t *testing.T) {
	s := &schemas.Suite{ID: "plain"}
	conds, skipped := Conditions([]string{ModelDown, BrokenTool, MisleadingInstruction, "nope"}, s)
	if !slices.Equal(conds, []string{"clean", schemas.GenericProbeModelDown}) {
		t.Fatalf("conds %v", conds)
	}
	if len(skipped) != 3 || skipped[0].Fault != BrokenTool || skipped[1].Fault != MisleadingInstruction || skipped[2].Fault != "nope" {
		t.Fatalf("skipped %v", skipped)
	}
	for _, sk := range skipped {
		if sk.Reason == "" || strings.Contains(sk.Reason, "—") {
			t.Fatalf("bad reason %q", sk.Reason)
		}
	}
	// The planner resolves the generic condition on any suite.
	p := s.ProbeByID(schemas.GenericProbeModelDown)
	if p == nil || p.Layer != schemas.LayerGateway || p.Apply.Fault == nil || p.Apply.Fault.Status != 503 {
		t.Fatalf("generic model-down probe %+v", p)
	}
	if s.ProbeByID("clean") != nil || s.ProbeByID("unknown") != nil {
		t.Fatal("clean and unknown ids must not resolve")
	}
}

func TestBrokenToolGenericNeedsToolProxy(t *testing.T) {
	s := &schemas.Suite{ID: "helpdesk", Services: []schemas.ServiceSpec{{ID: "crm", Role: schemas.ServiceRoleTool}}}
	conds, skipped := Conditions([]string{BrokenTool}, s)
	if !slices.Equal(conds, []string{"clean", schemas.GenericProbeBrokenTool}) || len(skipped) != 0 {
		t.Fatalf("got %v %v", conds, skipped)
	}
	p := s.ProbeByID(schemas.GenericProbeBrokenTool)
	if p == nil || p.Layer != schemas.LayerTool || p.Apply.Tool == nil || p.Apply.Tool.Mode != "error" {
		t.Fatalf("generic broken-tool probe %+v", p)
	}
	hidden := &schemas.Suite{ID: "h", Services: []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleTool, Hidden: true}}}
	if HasToolProxy(hidden) {
		t.Fatal("hidden service is not behind the tool proxy")
	}
	task := &schemas.Suite{ID: "t", Tasks: []*schemas.Task{{ID: "a", External: []schemas.ExternalAPI{{}}}}}
	if !HasToolProxy(task) {
		t.Fatal("task external API starts the tool proxy")
	}
}

func TestSuiteProbeWinsOverGeneric(t *testing.T) {
	s := &schemas.Suite{ID: "x", Probes: []*schemas.ProbeSpec{
		probe("outage-b", schemas.FamilyUnavailableModel, schemas.LayerGateway),
		probe("tool-error", schemas.FamilyBrokenTool, schemas.LayerTool),
		probe("crash", schemas.FamilyBrokenTool, schemas.LayerToolShim),
	}}
	conds, _ := Conditions([]string{BrokenTool, ModelDown}, s)
	if !slices.Equal(conds, []string{"clean", "tool-error", "outage-b"}) {
		t.Fatalf("got %v", conds)
	}
	own := &schemas.Suite{Probes: []*schemas.ProbeSpec{{ID: schemas.GenericProbeModelDown, Version: "9"}}}
	if own.ProbeByID(schemas.GenericProbeModelDown).Version != "9" {
		t.Fatal("suite probe with the generic id must win")
	}
}

// TestMapAndAttacks checks Map gives each condition its real fault (probe
// ids that name no fault included), marks attack probes, and that an attack
// probe is never taken for misleading-instruction.
func TestMapAndAttacks(t *testing.T) {
	inj := probe("prompt-injection", schemas.FamilyMisleadingInstruction, schemas.LayerFixture)
	inj.Axis = "safety"
	hostile := probe("important_instructions-it1", schemas.FamilyHostileTool, schemas.LayerService)
	s := &schemas.Suite{ID: "coding-core", Probes: []*schemas.ProbeSpec{
		inj, hostile,
		probe("test-crash", schemas.FamilyBrokenTool, schemas.LayerToolShim),
		probe("memory-trap", schemas.FamilyMisleadingInstruction, schemas.LayerFixture),
	}}
	conds, _ := Conditions([]string{MisleadingInstruction, BrokenTool, ModelDown}, s)
	if !slices.Equal(conds, []string{"clean", "memory-trap", "test-crash", schemas.GenericProbeModelDown}) {
		t.Fatalf("conds %v", conds)
	}
	m := Map([]string{MisleadingInstruction, BrokenTool, ModelDown}, s)
	want := map[string]string{"clean": Clean, "memory-trap": MisleadingInstruction, "test-crash": BrokenTool,
		schemas.GenericProbeModelDown: ModelDown, "prompt-injection": Attack, "important_instructions-it1": Attack}
	if len(m) != len(want) {
		t.Fatalf("map %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("map[%s] = %q, want %q (%v)", k, m[k], v, m)
		}
	}
	only := &schemas.Suite{ID: "x", Probes: []*schemas.ProbeSpec{inj}}
	if conds, skipped := Conditions([]string{MisleadingInstruction}, only); len(conds) != 1 || len(skipped) != 1 {
		t.Fatalf("an attack probe was taken for misleading-instruction: %v %v", conds, skipped)
	}
}
