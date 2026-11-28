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

func TestCatalogNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range Catalog {
		if seen[f.ID] || f.Label == "" || f.Help == "" || f.Group == "" || strings.Contains(f.Help, "—") {
			t.Fatalf("bad catalog entry %+v", f)
		}
		seen[f.ID] = true
	}
	if Names[0] != Clean || len(Names) != len(Catalog)+1 {
		t.Fatalf("names %v", Names)
	}
}

func TestConditionsResilience(t *testing.T) {
	withProxy := &schemas.Suite{ID: "api", External: []schemas.ExternalAPI{{ID: "acme"}}}
	drift := &schemas.ProbeSpec{ID: "search-v2", Layer: schemas.LayerTool, Apply: schemas.ProbeApply{Tool: &schemas.ToolFault{Mode: "input_drift"}}}
	withProxy.Probes = []*schemas.ProbeSpec{drift}
	all := []string{ModelBrownout, ModelSlow, ModelFlaky, ToolRateLimit, ToolSlow, ToolFlaky, ToolPartial, ToolAuthExpired, ToolRemoved, ToolInputDrift, ToolDuplicateEffect, CompoundBrownout}
	conds, skipped := Conditions(all, withProxy)
	want := []string{"clean", schemas.GenericProbeModelBrownout, schemas.GenericProbeModelSlow, schemas.GenericProbeModelFlaky,
		schemas.GenericProbeToolRateLimit, schemas.GenericProbeToolSlow, schemas.GenericProbeToolFlaky, schemas.GenericProbeToolPartial,
		schemas.GenericProbeToolRemoved, "search-v2", schemas.GenericProbeToolDuplicate, schemas.GenericProbeCompound}
	if !slices.Equal(conds, want) {
		t.Fatalf("conds %v\nwant  %v", conds, want)
	}
	if len(skipped) != 1 || skipped[0].Fault != ToolAuthExpired {
		t.Fatalf("skipped %v", skipped)
	}
	for _, id := range want[1:] {
		if p := withProxy.ProbeByID(id); p == nil {
			t.Fatalf("probe %s does not resolve", id)
		}
	}
	plain := &schemas.Suite{ID: "plain"}
	conds, skipped = Conditions([]string{ToolRateLimit, CompoundBrownout, ModelBrownout}, plain)
	if !slices.Equal(conds, []string{"clean", schemas.GenericProbeModelBrownout}) || len(skipped) != 2 {
		t.Fatalf("plain: %v %v", conds, skipped)
	}
}

// sweVerifiedShaped is a suite as the swebench importer writes
// hd-swe-verified: docker workspace, hidden tests, bugfix tasks in their own
// image, no probes and no tool service.
func sweVerifiedShaped() *schemas.Suite {
	return &schemas.Suite{ID: "hd-swe-verified", Environment: "docker_workspace", Grader: "verifier_command",
		Workdir: "/testbed", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierCleanRoom},
		Tasks: []*schemas.Task{{ID: "astropy__astropy-12907", Kind: schemas.TaskBugfix, RepoDir: "repo",
			Environment: &schemas.TaskEnvironment{Image: "swebench/sweb.eval.x86_64.astropy_1776_astropy-12907:latest", WorkspacePath: "/testbed"}}}}
}

func TestRepoCodingTakesBothGenericFaults(t *testing.T) {
	s := sweVerifiedShaped()
	conds, skipped := Conditions([]string{ModelDown, BrokenTool, MisleadingInstruction}, s)
	want := []string{"clean", schemas.GenericProbeModelDown, schemas.GenericProbeTestCrash, schemas.GenericProbeMisleadingNotes}
	if !slices.Equal(conds, want) || len(skipped) != 0 {
		t.Fatalf("got %v skipped %v, want %v", conds, skipped, want)
	}
	m := Map([]string{BrokenTool, MisleadingInstruction}, s)
	if m[schemas.GenericProbeTestCrash] != BrokenTool || m[schemas.GenericProbeMisleadingNotes] != MisleadingInstruction {
		t.Fatalf("map %v", m)
	}
	crash := s.ProbeByID(schemas.GenericProbeTestCrash)
	if crash == nil || crash.Layer != schemas.LayerToolShim || crash.Apply.Shim == nil || crash.Apply.Shim.Tool != "pytest" || crash.Apply.Shim.Calls != 2 {
		t.Fatalf("test crash probe %+v", crash)
	}
	notes := s.ProbeByID(schemas.GenericProbeMisleadingNotes)
	if notes == nil || notes.Layer != schemas.LayerFixture || notes.Apply.Overlay == nil || notes.Apply.Overlay.From != schemas.GenericNotesFrom || IsAttack(notes) {
		t.Fatalf("misleading notes probe %+v", notes)
	}
	// A suite's own probe still wins, and a non-coding or non-workspace suite
	// still skips.
	own := sweVerifiedShaped()
	own.Probes = []*schemas.ProbeSpec{probe("stale-notes", schemas.FamilyMisleadingInstruction, schemas.LayerFixture)}
	if c, _ := Conditions([]string{MisleadingInstruction}, own); !slices.Equal(c, []string{"clean", "stale-notes"}) {
		t.Fatalf("own probe lost: %v", c)
	}
	qa := sweVerifiedShaped()
	qa.Tasks[0].Kind = "qa"
	if _, sk := Conditions([]string{BrokenTool, MisleadingInstruction}, qa); len(sk) != 2 {
		t.Fatalf("non-coding suite must skip both, got %v", sk)
	}
	other := sweVerifiedShaped()
	other.Environment = "process"
	if RepoCoding(other) {
		t.Fatal("only the docker workspace plants notes and puts the shim on PATH")
	}
}
