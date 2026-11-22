package handlers

// Evaluation notifications (lane Y05, audit G4-08). When an evaluation
// ends, the server builds one EvaluationNotice (its scores, the change
// against its baseline or the previous run of its schedule, the gate and
// whether a release is blocked) and hands it to the notifier the server
// set (cmd/bifrost-bench/notify.go), which sends evaluation.finished,
// evaluation.regressed and release.blocked to the org's webhooks, chat
// channels, PagerDuty and the members who chose email. Evaluations end
// whenever their status is refreshed: on a read, after each child
// experiment's job ends, and on the server's periodic sweep
// (RefreshEvaluations). NotifiedAt makes it once.

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// NoticeBaseline is what an evaluation's scores are compared with.
type NoticeBaseline struct {
	// Source is previous_run (the last finished run of the same schedule)
	// or baseline (the gate's baseline: the other arm, or the live version).
	Source       string  `json:"source"`
	Version      int     `json:"version,omitempty"`
	EvaluationID string  `json:"evaluation_id,omitempty"`
	Score        float64 `json:"score"`
	Runs         int     `json:"runs"`
}

// Baseline sources.
const (
	BaselineFromPreviousRun = "previous_run"
	BaselineFromGate        = "baseline"
)

// NoticeCapability is one capability's score and change.
type NoticeCapability struct {
	Capability string   `json:"capability"`
	Score      float64  `json:"score"`
	Baseline   *float64 `json:"baseline,omitempty"`
	Change     *float64 `json:"change,omitempty"` // score minus baseline, as a fraction
}

// NoticeGate is the gate's decision.
type NoticeGate struct {
	State   string `json:"state"`
	Summary string `json:"summary"`
}

// EvaluationNotice is the data of the evaluation.* and release.blocked
// events (docs/notifications.md). Scores are verified success rates from 0
// to 1 over clean runs; Change is Score minus the baseline's.
type EvaluationNotice struct {
	EvaluationID string             `json:"evaluation_id"`
	Name         string             `json:"name,omitempty"`
	Status       string             `json:"status"`
	Mode         string             `json:"mode,omitempty"`
	Agent        string             `json:"agent,omitempty"`
	Version      int                `json:"version,omitempty"`
	Score        *float64           `json:"score,omitempty"`
	CILow        *float64           `json:"ci_low,omitempty"`
	CIHigh       *float64           `json:"ci_high,omitempty"`
	Runs         int                `json:"runs"`
	Baseline     *NoticeBaseline    `json:"baseline,omitempty"`
	Change       *float64           `json:"change,omitempty"`
	Capabilities []NoticeCapability `json:"capabilities,omitempty"`
	Gate         *NoticeGate        `json:"gate,omitempty"`
	Regressed    bool               `json:"regressed"`
	Regressions  []string           `json:"regressions,omitempty"` // why it counts as a regression
	Blocked      []string           `json:"blocked,omitempty"`     // why a release needs an override
	CostUSD      *float64           `json:"cost_usd,omitempty"`    // every arm's clean cost; nil when any run's cost is unknown
	Schedule     string             `json:"schedule,omitempty"`
	Link         string             `json:"link"` // absolute when the server knows its address, else a path
	FinishedAt   time.Time          `json:"finished_at"`
	Note         string             `json:"note,omitempty"`
}

// Events are the event types this notice is sent as, in order.
func (n EvaluationNotice) Events() []string {
	out := []string{webhooks.EventEvaluationFinished}
	if n.Regressed {
		out = append(out, webhooks.EventEvaluationRegressed)
	}
	if len(n.Blocked) > 0 {
		out = append(out, webhooks.EventReleaseBlocked)
	}
	return out
}

// Subject names the agent and version, else the evaluation.
func (n EvaluationNotice) Subject() string {
	switch {
	case n.Agent != "" && n.Version > 0:
		return fmt.Sprintf("%s v%d", n.Agent, n.Version)
	case n.Agent != "":
		return n.Agent
	case n.Name != "":
		return n.Name
	}
	return "Evaluation " + n.EvaluationID
}

// Pct is a 0..1 score as "61%" (one decimal under 10 points of change).
func Pct(v float64) string { return fmt.Sprintf("%.0f%%", math.Round(v*100)) }

// Points is a change of score as "+3.5 points" or "-6 points".
func Points(d float64) string {
	p := math.Round(d*1000) / 10
	s := fmt.Sprintf("%+.1f", p)
	s = strings.TrimSuffix(s, ".0")
	if p == 0 {
		s = "0"
	}
	if math.Abs(p) == 1 {
		return s + " point"
	}
	return s + " points"
}

// Fields are the short facts every format shows.
func (n EvaluationNotice) Fields() []webhooks.Field {
	var fs []webhooks.Field
	if n.Score != nil {
		fs = append(fs, webhooks.Field{Label: "Score", Value: Pct(*n.Score)})
	}
	if n.Change != nil && n.Baseline != nil {
		against := "baseline"
		switch {
		case n.Baseline.Source == BaselineFromPreviousRun:
			against = "previous run"
		case n.Baseline.Version > 0:
			against = fmt.Sprintf("v%d", n.Baseline.Version)
		}
		fs = append(fs, webhooks.Field{Label: "Change", Value: fmt.Sprintf("%s vs %s (%s)", Points(*n.Change), against, Pct(n.Baseline.Score))})
	}
	if n.Gate != nil {
		fs = append(fs, webhooks.Field{Label: "Gate", Value: gateWord(n.Gate.State)})
	}
	if n.CostUSD != nil {
		fs = append(fs, webhooks.Field{Label: "Spend", Value: fmt.Sprintf("$%.2f", *n.CostUSD)})
	}
	fs = append(fs, webhooks.Field{Label: "Status", Value: n.Status})
	if n.Schedule != "" {
		fs = append(fs, webhooks.Field{Label: "Schedule", Value: n.Schedule})
	}
	return fs
}

func gateWord(s string) string {
	switch s {
	case schemas.GatePass:
		return "passed"
	case schemas.GateFail:
		return "failed"
	case schemas.GateNoBaseline:
		return "no baseline to compare"
	}
	return s
}

// Summary is the human message of one of the notice's events.
func (n EvaluationNotice) Summary(event string) *webhooks.Summary {
	s := &webhooks.Summary{Link: n.Link, Fields: n.Fields(), Severity: webhooks.SeverityInfo}
	who := n.Subject()
	switch event {
	case webhooks.EventEvaluationRegressed:
		s.Severity = webhooks.SeverityError
		s.Title = who + " regressed"
		if n.Score != nil && n.Change != nil {
			s.Title = fmt.Sprintf("%s regressed: %s (%s)", who, Pct(*n.Score), Points(*n.Change))
		}
		s.Text = strings.Join(n.Regressions, "; ")
		s.DedupKey = "heimdall-regressed-" + firstNonEmpty(n.Schedule, n.Agent, n.EvaluationID)
	case webhooks.EventReleaseBlocked:
		s.Severity = webhooks.SeverityWarning
		s.Title = who + " cannot be released without an override"
		s.Text = strings.Join(n.Blocked, "; ")
		s.DedupKey = "heimdall-blocked-" + n.EvaluationID
	default:
		switch n.Status {
		case schemas.EvaluationDone:
			s.Title = who + " evaluation finished"
			if n.Score != nil {
				s.Title = fmt.Sprintf("%s scored %s", who, Pct(*n.Score))
			}
		case schemas.EvaluationCancelled:
			s.Title = who + " evaluation was cancelled"
		default:
			s.Severity = webhooks.SeverityWarning
			s.Title = who + " evaluation failed: no benchmark finished"
		}
		var parts []string
		if n.Name != "" {
			parts = append(parts, "Evaluation "+n.Name+".")
		}
		if n.Gate != nil && n.Gate.Summary != "" {
			parts = append(parts, n.Gate.Summary)
		}
		if n.Note != "" {
			parts = append(parts, n.Note)
		}
		s.Text = strings.Join(parts, " ")
	}
	return s
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// EvaluationNotifier receives each evaluation that ended, once.
type EvaluationNotifier func(env OrgEnv, ev *schemas.Evaluation, n EvaluationNotice)

var (
	evalNotifierMu sync.RWMutex
	evalNotifier   EvaluationNotifier
	// evalNotified guards against two refreshes of one evaluation racing
	// to send its finish (project root + id).
	evalNotified sync.Map
)

// SetEvaluationNotifier sets the server's notifier (nil turns it off).
func SetEvaluationNotifier(f EvaluationNotifier) {
	evalNotifierMu.Lock()
	evalNotifier = f
	evalNotifierMu.Unlock()
}

func currentEvaluationNotifier() EvaluationNotifier {
	evalNotifierMu.RLock()
	defer evalNotifierMu.RUnlock()
	return evalNotifier
}

// evalNotifyWait lets tests wait for the notice goroutine.
var evalNotifyWait sync.WaitGroup

// notifyFinished sends an evaluation that just ended (prev was not final)
// to the notifier and marks it; it reports whether ev changed.
func (h *evaluationsHandler) notifyFinished(ev *schemas.Evaluation, prev string) bool {
	f := currentEvaluationNotifier()
	if f == nil || terminalEvaluation(prev) || !terminalEvaluation(ev.Status) || ev.NotifiedAt != nil {
		return false
	}
	if _, dup := evalNotified.LoadOrStore(h.root()+"|"+ev.ID, true); dup {
		return false
	}
	now := time.Now().UTC()
	ev.NotifiedAt = &now
	snap := *ev
	snap.Children = slices.Clone(ev.Children)
	env, root := h.env, h.root()
	evals, _ := h.store().List()
	evalNotifyWait.Add(1)
	go func() {
		defer evalNotifyWait.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		f(env, &snap, BuildEvaluationNotice(ctx, env, root, &snap, evals, now))
	}()
	return true
}

// RefreshEvaluations refreshes every unfinished evaluation of the org in
// env, so the ones whose benchmarks all ended are marked done, judged and
// notified without anyone opening them. It returns how many ended.
func RefreshEvaluations(env OrgEnv) int {
	h := &evaluationsHandler{env: env, now: time.Now}
	if h.root() == "" {
		return 0
	}
	evs, err := h.store().List()
	if err != nil {
		return 0
	}
	n := 0
	for _, ev := range evs {
		if terminalEvaluation(ev.Status) || (env.Services.OrgID != "" && ev.Org != "" && ev.Org != env.Services.OrgID) {
			continue
		}
		h.refresh(ev)
		if terminalEvaluation(ev.Status) {
			n++
		}
	}
	return n
}

// BuildEvaluationNotice reads a finished evaluation's numbers. Without a
// run index, or for an evaluation that did not finish any benchmark, the
// notice carries the status alone.
func BuildEvaluationNotice(ctx context.Context, env OrgEnv, root string, ev *schemas.Evaluation, evals []*schemas.Evaluation, at time.Time) EvaluationNotice {
	n := EvaluationNotice{EvaluationID: ev.ID, Name: ev.Spec.Name, Status: ev.Status, Mode: ev.Spec.Mode, Schedule: ev.Schedule,
		Link: "/evaluations/" + ev.ID, FinishedAt: at.UTC()}
	if len(ev.Spec.Arms) > 0 {
		n.Agent, n.Version = reportcard.AgentRef(ev.Spec.Arms[len(ev.Spec.Arms)-1].Agent)
		if v := ev.Spec.Arms[len(ev.Spec.Arms)-1].Version; v > 0 {
			n.Version = v
		}
	}
	if ev.GateResult != nil {
		n.Gate = &NoticeGate{State: ev.GateResult.State, Summary: ev.GateResult.Summary}
	}
	if ev.Status != schemas.EvaluationDone {
		return n
	}
	g := gateEnv{env: env, root: root, now: func() time.Time { return at }}
	r, err := g.resolve(ctx, ev, evals)
	if err != nil {
		n.Note = "Scores are not available here: " + err.Error()
		return n
	}
	n.Agent, n.Version = r.Agent, r.CandidateVersion
	cand := reportcard.Side(r.Report, r.Candidate)
	if cand.Runs > 0 {
		n.Score, n.CILow, n.CIHigh, n.Runs = &cand.Score, &cand.CILow, &cand.CIHigh, cand.Runs
	}
	n.CostUSD = totalCost(r.Report)

	var base *reportcard.Report
	baseLabel := ""
	if ev.Schedule != "" {
		if prev, label := previousScheduledRun(evals, ev, r.Candidate); prev != nil {
			o := g.reportOptions(ctx)
			o.Arms = []string{label}
			if br, err := reportcard.Build(ctx, LeaderboardSource(env.Services.Index), prev, o); err == nil {
				if s := reportcard.Side(br, label); s.Runs > 0 {
					n.Baseline = &NoticeBaseline{Source: BaselineFromPreviousRun, Version: s.Version, EvaluationID: prev.ID, Score: s.Score, Runs: s.Runs}
					base, baseLabel = br, label
				}
			}
		}
	}
	if n.Baseline == nil && r.BaselineReport != nil {
		if s := reportcard.Side(r.BaselineReport, r.BaselineArm); s.Runs > 0 {
			n.Baseline = &NoticeBaseline{Source: BaselineFromGate, Version: r.BaselineVersion, Score: s.Score, Runs: s.Runs}
			if r.Baseline != nil {
				n.Baseline.EvaluationID = r.Baseline.EvaluationID
			}
			base, baseLabel = r.BaselineReport, r.BaselineArm
		}
	}
	if n.Baseline != nil && n.Score != nil {
		d := *n.Score - n.Baseline.Score
		n.Change = &d
	}
	for _, c := range reportcard.CapabilityScores(r.Report, r.Candidate, base, baseLabel) {
		row := NoticeCapability{Capability: c.Capability, Score: c.Candidate.Score}
		if c.Baseline != nil {
			b, d := c.Baseline.Score, c.Candidate.Score-c.Baseline.Score
			row.Baseline, row.Change = &b, &d
		}
		n.Capabilities = append(n.Capabilities, row)
	}

	// Regressions: a failed max_regression rule, a significant verdict
	// against the other arm, or (scheduled) a drop from the previous run.
	if ev.GateResult != nil && ev.GateResult.State == schemas.GateFail {
		for _, c := range ev.GateResult.Checks {
			if !c.Pass && c.Rule == "max_regression" {
				n.Regressions = append(n.Regressions, strings.TrimSuffix(c.Summary, "."))
			}
		}
	}
	if r.decision() == schemas.DecisionRegressed {
		n.Regressions = append(n.Regressions, fmt.Sprintf("the evaluation says %s is worse than %s", versionLabel(r.Agent, r.CandidateVersion), versionLabel(r.Agent, r.BaselineVersion)))
	}
	if b := n.Baseline; b != nil && b.Source == BaselineFromPreviousRun && n.Score != nil && n.CIHigh != nil {
		limit := 0.0
		if ev.Spec.Gate != nil {
			limit = ev.Spec.Gate.MaxRegression
		}
		if why := scheduledRegression(*n.Score, *n.CIHigh, b.Score, limit); why != "" {
			n.Regressions = append(n.Regressions, fmt.Sprintf("%s than the previous run of schedule %s (%s, %s)", why, ev.Schedule, b.EvaluationID, Pct(b.Score)))
		}
	}
	n.Regressed = len(n.Regressions) > 0
	n.Blocked = r.blocked(ev.GateResult)
	return n
}

// scheduledRegression says why score is a regression from prev, or "".
// With a max_regression limit the drop must pass it; without one the
// whole interval of the new score must sit below the previous score, so
// noise between two runs of the same check does not alert.
func scheduledRegression(score, ciHigh, prev, limit float64) string {
	drop := prev - score
	if drop <= 0 {
		return ""
	}
	if limit > 0 {
		if drop > limit+1e-9 {
			return fmt.Sprintf("%s lower, more than the %s the gate allows,", strings.TrimPrefix(Points(-drop), "-"), strings.TrimPrefix(Points(limit), "+"))
		}
		return ""
	}
	if ciHigh < prev {
		return fmt.Sprintf("%s lower, beyond its interval,", strings.TrimPrefix(Points(-drop), "-"))
	}
	return ""
}

// previousScheduledRun is the newest done evaluation of the same schedule
// created before ev, and the arm to compare: the one with the candidate's
// label, else its first.
func previousScheduledRun(evals []*schemas.Evaluation, ev *schemas.Evaluation, label string) (*schemas.Evaluation, string) {
	var best *schemas.Evaluation
	for _, e := range evals {
		if e == nil || e.ID == ev.ID || e.Schedule != ev.Schedule || e.Status != schemas.EvaluationDone || e.RerunOf != "" || !e.CreatedAt.Before(ev.CreatedAt) {
			continue
		}
		if ev.Org != "" && e.Org != "" && e.Org != ev.Org {
			continue
		}
		if best == nil || e.CreatedAt.After(best.CreatedAt) {
			best = e
		}
	}
	if best == nil || len(best.Spec.Arms) == 0 {
		return nil, ""
	}
	for _, a := range best.Spec.Arms {
		if a.Label == label {
			return best, label
		}
	}
	return best, best.Spec.Arms[0].Label
}

// totalCost is the clean cost of every arm, nil when any is unknown.
func totalCost(r *reportcard.Report) *float64 {
	if r == nil || len(r.Totals) == 0 {
		return nil
	}
	sum := 0.0
	for _, t := range r.Totals {
		if t.CostUSD == nil {
			return nil
		}
		sum += *t.CostUSD
	}
	return &sum
}
