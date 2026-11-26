package handlers

// Evaluation gate and release resolution (lane X12): which version of the
// agent an evaluation judges (the candidate), which version it is compared
// with (the baseline: the agent's live version, the last one released), and
// the numbers both sides scored. The gate (EvaluationSpec.Gate) is judged
// once, when the evaluation finishes; a release of the evaluation reads the
// same resolution.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// LiveVersion is GET /agents/{id}/live-version: the agent's live version.
// Version 0 means the agent has none yet (nothing released and no saved
// version). Source says which rule gave it: "release" (the newest release
// that names the agent and a version) or "saved_config" (nothing released
// yet: the newest version whose config is the saved agents/<id>.yaml, or
// v1 of a built-in library agent).
type LiveVersion struct {
	Agent        string    `json:"agent"`
	Version      int       `json:"version"`
	Source       string    `json:"source,omitempty"`
	ReleaseID    string    `json:"release_id,omitempty"`
	ReleasedAt   time.Time `json:"released_at,omitzero"`
	Approver     string    `json:"approver,omitempty"`
	EvaluationID string    `json:"evaluation_id,omitempty"`
}

// Live version sources (LiveVersion.Source).
const (
	LiveFromRelease     = "release"
	LiveFromSavedConfig = "saved_config"
)

// agentLiveVersion is the one live version rule (CI evaluate, release
// gates, releases and GET /agents/{id}/versions all read it): the last
// released version of agent, from the release log; when nothing was
// released yet, the newest saved version whose config is the saved
// agents/<id>.yaml (agentversions.Live), or v1 of a built-in library agent
// the project has no file for. projects may be nil (no saved config to read).
func agentLiveVersion(ctx context.Context, rl ReleaseLog, projects projectfs.Store, library, agent string) LiveVersion {
	out := releasedVersion(ctx, rl, agent)
	if out.Version > 0 || projects == nil || !agentversions.ValidID(agent) {
		return out
	}
	doc, _, err := projects.Read(ctx, projectfs.AreaAgents, agent+".yaml")
	if err != nil {
		if errors.Is(err, projectfs.ErrNotFound) && agentversions.LibraryFile(library, agent) != "" {
			out.Version, out.Source = 1, LiveFromSavedConfig
		}
		return out
	}
	vs, err := agentversions.List(projects.Root(), agent)
	if err != nil {
		return out
	}
	if v := agentversions.Live(vs, doc); v > 0 {
		out.Version, out.Source = v, LiveFromSavedConfig
	}
	return out
}

// releasedVersion reads the agent's last released version from the release
// log: the newest record that names the agent and a version.
func releasedVersion(ctx context.Context, rl ReleaseLog, agent string) LiveVersion {
	out := LiveVersion{Agent: agent}
	if rl == nil {
		return out
	}
	list, err := rl.List(ctx)
	if err != nil || list == nil {
		return out
	}
	for i := len(list.Releases) - 1; i >= 0; i-- {
		r := list.Releases[i]
		if r.AgentID == agent && r.Version > 0 {
			out.Version, out.Source, out.ReleaseID, out.ReleasedAt, out.Approver, out.EvaluationID = r.Version, LiveFromRelease, r.ReleaseID, r.CreatedAt, r.Approver, r.EvaluationID
			return out
		}
	}
	return out
}

// liveVersion is agentLiveVersion over an org's services.
func liveVersion(ctx context.Context, s Services, agent string) LiveVersion {
	return agentLiveVersion(ctx, s.Releases, s.Projects, suite.LibraryDir(), agent)
}

// evalResolution is who an evaluation judges and against what.
type evalResolution struct {
	Report           *reportcard.Report
	Agent            string
	Candidate        string // arm label
	CandidateVersion int
	BaselineArm      string // arm label in BaselineReport
	BaselineVersion  int
	BaselineReport   *reportcard.Report
	Baseline         *schemas.GateBaseline
	Live             LiveVersion
	Note             string
	// failOnInconclusive is the gate's fail_on_inconclusive: an
	// inconclusive gate blocks a release.
	failOnInconclusive bool
	// Dataset compares the benchmark versions of a baseline from another
	// evaluation (Y07). When they differ, CompareReport and BaselineReport
	// are both built over the tasks the two share, and every comparison
	// reads those; Report stays the candidate's full report card.
	Dataset       *schemas.DatasetComparison
	CompareReport *reportcard.Report
}

// compareReport is the candidate report a comparison reads.
func (r *evalResolution) compareReport() *reportcard.Report {
	if r.CompareReport != nil {
		return r.CompareReport
	}
	return r.Report
}

// gateEnv is what a resolution reads: the org's services and root.
type gateEnv struct {
	env  OrgEnv
	root string
	now  func() time.Time
}

func (g gateEnv) reportOptions(ctx context.Context) reportcard.Options {
	return (&reportCardHandler{env: g.env}).options(ctx)
}

// armVersion is an arm's pinned version, else the agent's newest version
// (0 when the agent has none).
func (g gateEnv) armVersion(a schemas.EvaluationArm) int {
	id, v := reportcard.AgentRef(a.Agent)
	if a.Version > 0 {
		return a.Version
	}
	if v > 0 {
		return v
	}
	if !agentversions.ValidID(id) {
		return 0
	}
	vs, err := agentversions.List(g.root, id)
	if err != nil || len(vs) == 0 {
		return 0
	}
	return vs[len(vs)-1].Version
}

// resolve builds the evaluation's report and picks the candidate and the
// baseline:
//   - versions: the newer version is the candidate and the other arm the
//     baseline; when the live version (or the gate's baseline version) is
//     neither, the note says so;
//   - models and opponent: B is the candidate, A the baseline;
//   - score: the one arm is the candidate; the baseline is the newest
//     earlier done evaluation that ran the baseline version of the agent,
//     so a nightly re-run of the live version compares with the last one.
func (g gateEnv) resolve(ctx context.Context, ev *schemas.Evaluation, evals []*schemas.Evaluation) (*evalResolution, error) {
	s := g.env.Services
	if s.Index == nil {
		return nil, errors.New("the run index is not available on this server")
	}
	arms := ev.Spec.Arms
	if len(arms) == 0 {
		return nil, errors.New("the evaluation has no arm")
	}
	src := LeaderboardSource(s.Index)
	opt := g.reportOptions(ctx)
	rep, err := reportcard.Build(ctx, src, ev, opt)
	if err != nil {
		return nil, err
	}
	res := &evalResolution{Report: rep}
	want := 0
	if ev.Spec.Gate != nil {
		want = ev.Spec.Gate.BaselineVersion
		res.failOnInconclusive = ev.Spec.Gate.FailOnInconclusive
	}
	pick := func(cand, base schemas.EvaluationArm) {
		res.Candidate, res.CandidateVersion = cand.Label, g.armVersion(cand)
		res.Agent, _ = reportcard.AgentRef(cand.Agent)
		res.BaselineArm, res.BaselineVersion, res.BaselineReport = base.Label, g.armVersion(base), rep
		bid, _ := reportcard.AgentRef(base.Agent)
		res.Baseline = &schemas.GateBaseline{Agent: bid, Version: res.BaselineVersion, Arm: base.Label, EvaluationID: ev.ID}
	}
	switch {
	case ev.Spec.Mode == schemas.EvaluationModeVersions && len(arms) == 2:
		agent, _ := reportcard.AgentRef(arms[1].Agent)
		res.Live = liveVersion(ctx, s, agent)
		if want == 0 {
			want = res.Live.Version
		}
		// The newer version is the candidate; releasing an older one over
		// a newer live version is a rollback and is recorded as such.
		va, vb := g.armVersion(arms[0]), g.armVersion(arms[1])
		if va > vb {
			pick(arms[0], arms[1])
		} else {
			pick(arms[1], arms[0])
		}
		if want > 0 && res.BaselineVersion != want && res.CandidateVersion != want {
			res.Note = fmt.Sprintf("the live version v%d did not run in this evaluation; compared with v%d", want, res.BaselineVersion)
		}
	case len(arms) == 2 && ev.Spec.Mode != schemas.EvaluationModeScore:
		pick(arms[1], arms[0])
		res.Live = liveVersion(ctx, s, res.Agent)
	default:
		cand := arms[0]
		res.Candidate, res.CandidateVersion = cand.Label, g.armVersion(cand)
		res.Agent, _ = reportcard.AgentRef(cand.Agent)
		res.Live = liveVersion(ctx, s, res.Agent)
		if want == 0 {
			want = res.Live.Version
		}
		if want > 0 {
			if prev, label := previousRun(evals, ev, res.Agent, want); prev != nil {
				cmp, shared := reportcard.CompareDatasets(src, ev, prev)
				o := opt
				o.Arms = []string{label}
				o.Tasks = shared // nil when both ran the same tasks of the same versions
				if br, err := reportcard.Build(ctx, src, prev, o); err == nil {
					res.BaselineArm, res.BaselineVersion, res.BaselineReport = label, want, br
					res.Baseline = &schemas.GateBaseline{Agent: res.Agent, Version: want, EvaluationID: prev.ID}
					res.Dataset = cmp
					if shared != nil {
						co := opt
						co.Tasks = shared
						cr, err := reportcard.Build(ctx, src, ev, co)
						if err != nil {
							return nil, err
						}
						res.CompareReport = cr
					}
				}
			}
		}
	}
	if res.Baseline != nil {
		res.Baseline.Live = res.Live.Version > 0 && res.Baseline.Agent == res.Live.Agent && res.Baseline.Version == res.Live.Version
	}
	return res, nil
}

// previousRun is the newest done evaluation other than ev, created before
// it, with an arm that ran agent at version; it returns that arm's label.
func previousRun(evals []*schemas.Evaluation, ev *schemas.Evaluation, agent string, version int) (*schemas.Evaluation, string) {
	sorted := slices.Clone(evals)
	slices.SortStableFunc(sorted, func(a, b *schemas.Evaluation) int { return b.CreatedAt.Compare(a.CreatedAt) })
	for _, e := range sorted {
		if e == nil || e.ID == ev.ID || e.Status != schemas.EvaluationDone || e.CreatedAt.After(ev.CreatedAt) || e.CheatCheckOf != "" || e.RerunOf != "" {
			continue
		}
		if ev.Org != "" && e.Org != "" && e.Org != ev.Org {
			continue
		}
		arms := e.Spec.Arms
		if e.Spec.Mode == schemas.EvaluationModeScore && len(arms) > 1 {
			arms = arms[:1]
		}
		for _, a := range arms {
			if reportcard.ArmMatches(a, agent, version, "") {
				return e, a.Label
			}
		}
	}
	return nil, ""
}

// judge applies the evaluation's gate to a resolution.
func (g gateEnv) judge(ev *schemas.Evaluation, r *evalResolution) *schemas.EvaluationGateResult {
	if ev.Spec.Gate == nil {
		return nil
	}
	return reportcard.JudgeGate(reportcard.GateInput{
		Gate: *ev.Spec.Gate, Agent: r.Agent, Candidate: r.Candidate, CandidateVersion: r.CandidateVersion,
		Report: r.Report, CompareReport: r.CompareReport, Baseline: r.Baseline, BaselineArm: r.BaselineArm, BaselineReport: r.BaselineReport,
		Dataset: r.Dataset, Now: g.now(),
	})
}

// summary is the release summary of a resolution: the candidate and the
// baseline with their numbers, and the per-capability scores.
func (r *evalResolution) summary() *schemas.ReleaseSummary {
	cand := r.compareReport() // the shared tasks only when the benchmark changed since the baseline
	out := &schemas.ReleaseSummary{Candidate: reportcard.Side(cand, r.Candidate), Note: r.Note, Dataset: r.Dataset}
	out.Candidate.Version = r.CandidateVersion
	if r.BaselineReport != nil {
		b := reportcard.Side(r.BaselineReport, r.BaselineArm)
		b.Version = r.BaselineVersion
		out.Baseline = &b
	}
	out.Capabilities = reportcard.CapabilityScores(cand, r.Candidate, r.BaselineReport, r.BaselineArm)
	if r.Dataset != nil && r.Dataset.Changed {
		out.Note = strings.TrimSpace(strings.TrimSuffix(out.Note+". "+r.Dataset.Summary, ". "))
		out.Note = strings.TrimPrefix(out.Note, ". ")
	}
	return out
}

// decision is the evaluation's verdict for the candidate: improved or
// regressed when the overall A/B verdict against the baseline arm of the
// same evaluation is significant, else inconclusive.
func (r *evalResolution) decision() schemas.Decision {
	o := r.Report.Overall
	if o == nil || !o.Significant || r.BaselineReport != r.Report {
		return schemas.DecisionInconclusive
	}
	switch o.Winner {
	case r.Candidate:
		return schemas.DecisionImproved
	case r.BaselineArm:
		return schemas.DecisionRegressed
	}
	return schemas.DecisionInconclusive
}

// versionLabel is "codex v4", or "codex" when the version is unknown.
func versionLabel(agent string, v int) string {
	if v > 0 {
		return fmt.Sprintf("%s v%d", agent, v)
	}
	return agent
}

// blocked is why a release of the evaluation needs an override: a failed
// gate, a gate that could not compare while a version is live, or (with no
// gate) an overall verdict that says the candidate is worse.
func (r *evalResolution) blocked(gate *schemas.EvaluationGateResult) []string {
	var out []string
	if gate != nil {
		switch gate.State {
		case schemas.GateFail:
			out = append(out, strings.TrimSuffix(gate.Summary, "."))
		case schemas.GateInconclusive:
			if r.failOnInconclusive {
				out = append(out, strings.TrimSuffix(gate.Summary, "."))
			}
		case schemas.GateNoBaseline:
			if r.Live.Version > 0 {
				out = append(out, fmt.Sprintf("the gate could not compare with the live version v%d", r.Live.Version))
			}
		}
	}
	if gate == nil && r.decision() == schemas.DecisionRegressed {
		out = append(out, fmt.Sprintf("the evaluation says %s is worse than %s", versionLabel(r.Agent, r.CandidateVersion), versionLabel(r.Agent, r.BaselineVersion)))
	}
	return out
}

// decideGate judges a finished evaluation's gate once and stores the result
// on ev. It reports whether ev changed.
func (g gateEnv) decideGate(ctx context.Context, ev *schemas.Evaluation, evals []*schemas.Evaluation) bool {
	if ev.Spec.Gate == nil || ev.GateResult != nil || ev.Status != schemas.EvaluationDone || g.env.Services.Index == nil {
		return false
	}
	r, err := g.resolve(ctx, ev, evals)
	if err != nil {
		return false
	}
	ev.GateResult = g.judge(ev, r)
	return ev.GateResult != nil
}
