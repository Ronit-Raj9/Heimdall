package compliance

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// PackVersion is the version of the pack format.
const PackVersion = "1.0.0"

// Chain statuses of an experiment's seals.log.
const (
	ChainVerified   = "verified"
	ChainBroken     = "broken"
	ChainNotChecked = "not_checked"
)

// Input is everything a pack is built from. The caller loads it; Build reads
// nothing else.
type Input struct {
	Experiments  []ExperimentInput
	Release      *schemas.ReleaseRecord // set when the pack is exported with a release
	RunsRoot     string                 // absolute runs root, to make run paths relative
	BenchVersion string
	Now          time.Time
}

// ExperimentInput is one experiment's evidence as loaded from the store.
type ExperimentInput struct {
	Manifest     *schemas.Manifest
	Runs         []schemas.SealedRun           // counted and uncounted sealed runs, as SealedRuns returns them
	Analysis     *schemas.Analysis             // the analysis the pack reports; nil when none could be loaded
	Probes       map[string]*schemas.ProbeSpec // the suite's probes by id; nil when the suite could not be loaded
	ChainChecked bool
	ChainErr     error  // nil when the chain verified
	SealsTail    string // the last line of seals.log
}

// Build returns the evidence pack of the experiments. It refuses no
// experiment for its verdict or its evidence: a broken chain or a run that
// does not verify is reported in the pack and in Warnings, because the pack
// exists to show the evidence as it is.
func Build(in Input) (*schemas.CompliancePack, error) {
	if len(in.Experiments) == 0 {
		return nil, errors.New("compliance pack: no experiment")
	}
	p := &schemas.CompliancePack{
		Version:     PackVersion,
		GeneratedAt: in.Now.UTC(),
		Generator:   "bifrost-bench " + in.BenchVersion,
	}
	tests := map[string]*schemas.ComplianceTest{}
	for i := range in.Experiments {
		ex := &in.Experiments[i]
		if ex.Manifest == nil {
			return nil, fmt.Errorf("compliance pack: experiment %d has no manifest", i)
		}
		p.Experiments = append(p.Experiments, experimentSummary(ex))
		p.Warnings = append(p.Warnings, experimentWarnings(ex)...)
		addTests(tests, ex, in.RunsRoot)
	}
	if in.Release != nil {
		p.Release = releaseSummary(in.Release)
	}
	p.Tests = sortedTests(tests)
	for _, t := range p.Tests {
		if len(t.Mappings) == 0 {
			p.Unmapped = append(p.Unmapped, t.ID)
		}
	}
	p.Frameworks = usedFrameworks(p.Tests)
	p.Functions = nistFunctions(p)
	p.RecordKeeping = recordKeeping()
	p.NotCertified = notCertified(p)
	return p, nil
}

// experimentSummary is one experiment's row of the pack.
func experimentSummary(ex *ExperimentInput) schemas.ComplianceExperiment {
	m := ex.Manifest
	out := schemas.ComplianceExperiment{
		ExpID: m.ExpID, Name: m.Experiment.Name, Suite: m.SuiteID, SuiteVersion: m.SuiteVersion,
		SuiteLockHash: m.SuiteLockHash, ManifestHash: m.Hash, FrozenAt: m.CreatedAt,
		Realism: schemas.EffectiveRealism(&m.Experiment, nil), Path: m.ExpID,
		SealsLogTail: ex.SealsTail, ChainStatus: ChainNotChecked,
	}
	for id := range m.Arms {
		out.Arms = append(out.Arms, id)
	}
	sort.Strings(out.Arms)
	for _, c := range m.Experiment.Conditions {
		out.Conditions = append(out.Conditions, c.ID)
	}
	for _, r := range countedRuns(ex.Runs) {
		out.CountedRuns++
		if len(r.Problems) == 0 && r.SealHash != "" {
			out.RunsVerified++
		}
		for _, pr := range r.Problems {
			out.RunProblems = append(out.RunProblems, fmt.Sprintf("%s: %s", r.Run.RunID, pr))
		}
	}
	if ex.ChainChecked {
		out.ChainStatus = ChainVerified
		if ex.ChainErr != nil {
			out.ChainStatus, out.ChainDetail = ChainBroken, ex.ChainErr.Error()
		}
	}
	if a := ex.Analysis; a != nil {
		out.AnalysisID, out.Decision, out.Action = a.AnalysisID, a.Verdict.Decision, a.Verdict.Action
		out.GradeSource, out.Mode = a.GradeSource, a.Mode
	}
	return out
}

// experimentWarnings names what a reader must know before relying on an
// experiment's rows.
func experimentWarnings(ex *ExperimentInput) []string {
	id := ex.Manifest.ExpID
	var w []string
	if !ex.ChainChecked {
		w = append(w, fmt.Sprintf("%s: seals.log chain was not checked", id))
	} else if ex.ChainErr != nil {
		w = append(w, fmt.Sprintf("%s: seals.log chain does not verify: %v", id, ex.ChainErr))
	}
	for _, r := range countedRuns(ex.Runs) {
		if len(r.Problems) > 0 {
			w = append(w, fmt.Sprintf("%s: run %s has evidence problems: %s", id, r.Run.RunID, strings.Join(r.Problems, "; ")))
		}
	}
	if ex.Analysis == nil {
		w = append(w, fmt.Sprintf("%s: no analysis; the verdict is not part of this pack", id))
	} else if ex.Analysis.Mode == schemas.ModeSynthetic {
		w = append(w, fmt.Sprintf("%s: synthetic mode; its runs are not findings", id))
	}
	if ex.Manifest.Experiment.Smoke {
		w = append(w, fmt.Sprintf("%s: smoke experiment; its runs are never findings", id))
	}
	if ex.Probes == nil {
		w = append(w, fmt.Sprintf("%s: suite %s could not be loaded; probes are mapped by id only", id, ex.Manifest.SuiteID))
	}
	return w
}

// addTests adds the experiment's conditions and gates to the test set.
func addTests(tests map[string]*schemas.ComplianceTest, ex *ExperimentInput, runsRoot string) {
	expID := ex.Manifest.ExpID
	for _, r := range countedRuns(ex.Runs) {
		cond := r.Slot.Condition
		t := testFor(tests, "condition:"+cond, func() *schemas.ComplianceTest { return conditionTest(cond, ex.Probes[cond]) })
		vetoed := anyVeto(r.Gates)
		t.Runs++
		if vetoed {
			t.Fired++
		}
		t.ExpIDs = addID(t.ExpIDs, expID)
		t.Evidence = append(t.Evidence, evidenceFor(r, expID, runsRoot, vetoed))
		for _, g := range r.Gates {
			gt := testFor(tests, "gate:"+g.Gate, func() *schemas.ComplianceTest { return gateTest(g) })
			gt.Runs++
			if g.Fired {
				gt.Fired++
				gt.Evidence = append(gt.Evidence, evidenceFor(r, expID, runsRoot, true))
			}
			gt.ExpIDs = addID(gt.ExpIDs, expID)
		}
	}
}

// conditionTest is a new test row for a condition, mapped by probe id, then
// axis, then family.
func conditionTest(cond string, probe *schemas.ProbeSpec) *schemas.ComplianceTest {
	t := &schemas.ComplianceTest{ID: "condition:" + cond, Kind: schemas.ComplianceTestCondition, Name: cond}
	if probe != nil {
		t.Axis, t.Family = probe.Axis, string(probe.PS2Family)
	}
	switch r, ok := probeRules[cond]; {
	case ok:
		t.Title, t.MappedBy, t.Mappings = r.title, "id", mappingsFor(r)
	case t.Axis != "" && axisRules[t.Axis].title != "":
		r := axisRules[t.Axis]
		t.Title, t.MappedBy, t.Mappings = r.title+" ("+cond+")", "axis", mappingsFor(r)
	case t.Family != "" && familyRules[t.Family].title != "":
		r := familyRules[t.Family]
		t.Title, t.MappedBy, t.Mappings = r.title+" ("+cond+")", "family", mappingsFor(r)
	default:
		t.Title, t.MappedBy, t.Mappings = "Probe "+cond, "none", []schemas.ComplianceMapping{}
	}
	return t
}

// gateTest is a new test row for a gate, mapped by gate name.
func gateTest(g schemas.GateResult) *schemas.ComplianceTest {
	t := &schemas.ComplianceTest{ID: "gate:" + g.Gate, Kind: schemas.ComplianceTestGate, Name: g.Gate, Effect: g.Effect}
	if r, ok := gateRules[g.Gate]; ok {
		t.Title, t.MappedBy, t.Mappings = r.title, "id", mappingsFor(r)
		return t
	}
	t.Title, t.MappedBy, t.Mappings = "Gate "+g.Gate, "none", []schemas.ComplianceMapping{}
	return t
}

// evidenceFor is the pointer to one sealed run.
func evidenceFor(r schemas.SealedRun, expID, runsRoot string, fired bool) schemas.ComplianceEvidence {
	return schemas.ComplianceEvidence{
		RunID: r.Run.RunID, ExpID: expID, Task: r.Slot.Task, Arm: r.Slot.Arm, Condition: r.Slot.Condition,
		Trial: r.Slot.Trial, Outcome: r.Run.Outcome, Fired: fired, SealHash: r.SealHash, Path: relPath(runsRoot, r.Dir),
	}
}

// releaseSummary is the release a pack was exported with.
func releaseSummary(r *schemas.ReleaseRecord) *schemas.ComplianceRelease {
	return &schemas.ComplianceRelease{
		ReleaseID: r.ReleaseID, ExpID: r.ExpID, AnalysisID: r.AnalysisID, Decision: r.Decision, Action: r.Action,
		Approver: r.Approver, Override: r.Override, SealsDigest: r.SealsDigest, EvidenceSHA256: r.EvidenceSHA256,
		Hash: r.Hash, CreatedAt: r.CreatedAt,
	}
}

// countedRuns returns the counted runs: the ones a verdict reads.
func countedRuns(runs []schemas.SealedRun) []schemas.SealedRun {
	var out []schemas.SealedRun
	for _, r := range runs {
		if r.Slot.Counted {
			out = append(out, r)
		}
	}
	return out
}

// anyVeto reports whether a veto gate fired in the run.
func anyVeto(gates []schemas.GateResult) bool {
	for _, g := range gates {
		if g.Fired && g.Effect == schemas.GateVeto {
			return true
		}
	}
	return false
}

// testFor returns the test with id, creating it with mk when it is new.
func testFor(tests map[string]*schemas.ComplianceTest, id string, mk func() *schemas.ComplianceTest) *schemas.ComplianceTest {
	if t, ok := tests[id]; ok {
		return t
	}
	t := mk()
	t.ExpIDs = []string{}
	t.Evidence = []schemas.ComplianceEvidence{}
	tests[id] = t
	return t
}

// sortedTests returns conditions first (clean, then probes by id), then gates
// by name, each with its evidence fired runs first, then in experiment, run
// id order, so a capped listing never hides a firing.
func sortedTests(tests map[string]*schemas.ComplianceTest) []schemas.ComplianceTest {
	out := make([]schemas.ComplianceTest, 0, len(tests))
	for _, t := range tests {
		sort.Slice(t.Evidence, func(i, j int) bool {
			a, b := t.Evidence[i], t.Evidence[j]
			if a.Fired != b.Fired {
				return a.Fired
			}
			if a.ExpID != b.ExpID {
				return a.ExpID < b.ExpID
			}
			return a.RunID < b.RunID
		})
		out = append(out, *t)
	}
	rank := func(t schemas.ComplianceTest) int {
		switch {
		case t.Kind == schemas.ComplianceTestCondition && t.Name == schemas.ConditionClean:
			return 0
		case t.Kind == schemas.ComplianceTestCondition:
			return 1
		default:
			return 2
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// usedFrameworks lists every catalog id the tests cite, plus the pack-level
// ids, by framework then id.
func usedFrameworks(tests []schemas.ComplianceTest) []schemas.ComplianceFramework {
	seen := map[string]bool{}
	for _, t := range tests {
		for _, m := range t.Mappings {
			seen[m.ID] = true
		}
	}
	for _, id := range packLevelIDs {
		seen[id] = true
	}
	out := make([]schemas.ComplianceFramework, 0, len(seen))
	for id := range seen {
		e := catalog[id]
		out = append(out, schemas.ComplianceFramework{Framework: e.framework, ID: e.id, Name: e.name, Verified: e.verified, Source: e.source})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Framework != out[j].Framework {
			return out[i].Framework < out[j].Framework
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// addID appends id when the sorted list does not hold it yet.
func addID(ids []string, id string) []string {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	ids = append(ids, id)
	sort.Strings(ids)
	return ids
}

// relPath is dir relative to root with forward slashes, or dir itself when
// it is not under root.
func relPath(root, dir string) string {
	if root == "" || dir == "" {
		return filepath.ToSlash(dir)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}
