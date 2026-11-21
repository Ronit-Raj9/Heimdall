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
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// LiveVersion is GET /agents/{id}/live-version: the version of an agent
// that the newest release put live. Version 0 means nothing was released.
type LiveVersion struct {
	Agent        string    `json:"agent"`
	Version      int       `json:"version"`
	ReleaseID    string    `json:"release_id,omitempty"`
	ReleasedAt   time.Time `json:"released_at,omitzero"`
	Approver     string    `json:"approver,omitempty"`
	EvaluationID string    `json:"evaluation_id,omitempty"`
}

// liveVersion reads the agent's live version from the release log: the
// newest record that names the agent and a version.
func liveVersion(ctx context.Context, rl ReleaseLog, agent string) LiveVersion {
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
			out.Version, out.ReleaseID, out.ReleasedAt, out.Approver, out.EvaluationID = r.Version, r.ReleaseID, r.CreatedAt, r.Approver, r.EvaluationID
			return out
		}
	}
	return out
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
		res.Live = liveVersion(ctx, s.Releases, agent)
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
		res.Live = liveVersion(ctx, s.Releases, res.Agent)
	default:
		cand := arms[0]
		res.Candidate, res.CandidateVersion = cand.Label, g.armVersion(cand)
		res.Agent, _ = reportcard.AgentRef(cand.Agent)
		res.Live = liveVersion(ctx, s.Releases, res.Agent)
		if want == 0 {
			want = res.Live.Version
		}
		if want > 0 {
			if prev, label := previousRun(evals, ev, res.Agent, want); prev != nil {
				o := opt
				o.Arms = []string{label}
				if br, err := reportcard.Build(ctx, src, prev, o); err == nil {
					res.BaselineArm, res.BaselineVersion, res.BaselineReport = label, want, br
					res.Baseline = &schemas.GateBaseline{Agent: res.Agent, Version: want, EvaluationID: prev.ID}
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
		if e == nil || e.ID == ev.ID || e.Status != schemas.EvaluationDone || e.CreatedAt.After(ev.CreatedAt) {
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
		Report: r.Report, Baseline: r.Baseline, BaselineArm: r.BaselineArm, BaselineReport: r.BaselineReport, Now: g.now(),
	})
}

// summary is the release summary of a resolution: the candidate and the
// baseline with their numbers, and the per-capability scores.
func (r *evalResolution) summary() *schemas.ReleaseSummary {
	out := &schemas.ReleaseSummary{Candidate: reportcard.Side(r.Report, r.Candidate), Note: r.Note}
	out.Candidate.Version = r.CandidateVersion
	if r.BaselineReport != nil {
		b := reportcard.Side(r.BaselineReport, r.BaselineArm)
		b.Version = r.BaselineVersion
		out.Baseline = &b
	}
	out.Capabilities = reportcard.CapabilityScores(r.Report, r.Candidate, r.BaselineReport, r.BaselineArm)
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
