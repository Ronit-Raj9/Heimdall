package improve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
)

// Runner creates, drives, pauses and stops improvement runs. A run resumes
// from its last saved stage, so a crash or a failed experiment is retried
// with another Run call.
type Runner struct {
	Root   string // <runs root>/.improvements
	Exec   Executor
	Now    func() time.Time
	Logger schemas.Logger

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Create freezes a spec: resolves version 0, partitions the task pool,
// reads the protected material of the audit and sealed tasks, and writes
// the record and the lineage root. Nothing runs yet.
func (r *Runner) Create(ctx context.Context, spec *Spec, actor string) (*Record, error) {
	spec.Defaults()
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	hash, err := spec.Hash()
	if err != nil {
		return nil, err
	}
	base, err := r.Exec.Load(ctx, spec.Agent, spec.Editable)
	if err != nil {
		return nil, fmt.Errorf("load agent %s: %w", spec.Agent, err)
	}
	infos, err := r.Exec.Tasks(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("list tasks of %s: %w", spec.Suite, err)
	}
	pool := []string{}
	for _, t := range infos {
		if t.Impossible {
			if !slices.Contains(spec.Splits.Honeypots, t.ID) {
				continue // impossible tasks are honeypots only when the spec names them
			}
		}
		pool = append(pool, t.ID)
	}
	splits, err := Partition(pool, spec)
	if err != nil {
		return nil, err
	}
	prot, err := r.Exec.Protect(ctx, spec, slices.Concat(splits.Audit, splits.Sealed))
	if err != nil {
		return nil, fmt.Errorf("read protected task content: %w", err)
	}
	now := r.now()
	rec := &Record{ID: newID(spec.Name), Name: spec.Name, Spec: *spec, SpecHash: hash, Status: StatusPlanned,
		Reason: "created; nothing has run yet", CreatedAt: now, CreatedBy: actor, Splits: splits, Base: base,
		Incumbent: base, Rounds: []*Round{}, Evidence: []EvidenceItem{}}
	if err := os.MkdirAll(r.dir(rec.ID), 0o755); err != nil {
		return nil, err
	}
	if err := r.saveProtected(rec.ID, prot); err != nil {
		return nil, err
	}
	if _, err := appendNode(r.dir(rec.ID), Node{Ref: base.Ref, Version: base.Number, Author: schemas.AuthorHuman,
		BundleHash: base.BundleHash, ConfigHash: base.ConfigHash, Status: NodeBase, Reason: "version 0", At: now}); err != nil {
		return nil, err
	}
	return rec, r.save(rec)
}

// Get returns a stored improvement.
func (r *Runner) Get(id string) (*Record, error) { return r.load(id) }

// List returns every stored improvement, newest first.
func (r *Runner) List() ([]*Record, error) {
	ents, err := os.ReadDir(r.Root)
	if errors.Is(err, os.ErrNotExist) {
		return []*Record{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []*Record{}
	for _, e := range ents {
		if !e.IsDir() || !ValidID(e.Name()) {
			continue
		}
		rec, err := r.load(e.Name())
		if err != nil {
			continue
		}
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b *Record) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

// Running reports whether id is being driven by this runner.
func (r *Runner) Running(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.running[id]
	return ok
}

// Approve overrides the tripwires of round n, which must be awaiting
// approval, with a reason. The next Run continues the round.
func (r *Runner) Approve(id string, n int, actor, reason string) (*Record, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("an approval needs a reason: %w", ErrState)
	}
	if r.Running(id) {
		return nil, ErrBusy
	}
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if rec.Status != StatusAwaiting || n < 1 || n > len(rec.Rounds) {
		return nil, fmt.Errorf("improvement %s round %d is not awaiting approval: %w", id, n, ErrState)
	}
	rd := rec.Rounds[n-1]
	rd.Approvals = append(rd.Approvals, Approval{Stage: rd.Stage, By: actor, Reason: reason, At: r.now()})
	rec.Status, rec.Reason = StatusPlanned, fmt.Sprintf("round %d approved by %s; continue to resume", n, actor)
	return rec, r.save(rec)
}

// Stop ends an improvement: a running loop is cancelled and the record is
// marked stopped. A finished one is refused.
func (r *Runner) Stop(id, actor string) (*Record, error) {
	r.mu.Lock()
	cancel, running := r.running[id]
	r.mu.Unlock()
	if running {
		cancel()
		for range 200 { // the loop saves its stage and exits
			if !r.Running(id) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if rec.Status == StatusDone || rec.Status == StatusStopped {
		return rec, fmt.Errorf("improvement %s is %s: %w", id, rec.Status, ErrState)
	}
	if rd := rec.current(); rd != nil && rd.Candidate != nil && rd.Decision == "" && rec.Status == StatusAwaiting {
		rd.Decision, rd.Why = DecisionRejected, "stopped at a tripwire: "+strings.Join(rd.Tripwires, ", ")
		if _, err := appendNode(r.dir(rec.ID), Node{Round: rd.N, Ref: rd.Candidate.Ref, Version: rd.Candidate.Number, Parent: rd.Parent.Ref,
			Author: schemas.AuthorAgent, BundleHash: rd.Candidate.BundleHash, ConfigHash: rd.Candidate.ConfigHash, Status: NodeTripped,
			Reason: rd.Why, At: r.now()}); err != nil {
			return rec, err
		}
	}
	rec.Status, rec.Reason = StatusStopped, "stopped by "+actor
	return rec, r.save(rec)
}

// Run drives an improvement until it finishes, pauses for approval, is
// stopped, or an experiment fails (status failed; Run again resumes).
func (r *Runner) Run(ctx context.Context, id string) (*Record, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.mu.Lock()
	if r.running == nil {
		r.running = map[string]context.CancelFunc{}
	}
	if _, busy := r.running[id]; busy {
		r.mu.Unlock()
		return nil, ErrBusy
	}
	r.running[id] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}()
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	switch rec.Status {
	case StatusDone, StatusStopped:
		return rec, fmt.Errorf("improvement %s is %s: %w", id, rec.Status, ErrState)
	case StatusAwaiting:
		return rec, fmt.Errorf("improvement %s is awaiting approval: %w", id, ErrState)
	}
	if err := rec.Splits.Verify(); err != nil {
		return r.fail(rec, err)
	}
	prot, err := r.loadProtected(id)
	if err != nil {
		return r.fail(rec, fmt.Errorf("read protected content: %w", err))
	}
	rec.Status, rec.Reason, rec.Error = StatusRunning, "running", ""
	if err := r.save(rec); err != nil {
		return rec, err
	}
	if err := r.drive(ctx, rec, prot); err != nil {
		if ctx.Err() != nil {
			rec.Status, rec.Reason = StatusPlanned, "interrupted; run again to resume"
			return rec, errors.Join(err, r.save(rec))
		}
		return r.fail(rec, err)
	}
	return rec, r.save(rec)
}

func (r *Runner) fail(rec *Record, err error) (*Record, error) {
	rec.Status, rec.Reason, rec.Error = StatusFailed, "an experiment failed; run again to resume", err.Error()
	return rec, errors.Join(err, r.save(rec))
}

// drive runs the remaining stages, saving after each.
func (r *Runner) drive(ctx context.Context, rec *Record, prot Protected) error {
	if rec.Baseline == nil {
		if err := r.baseline(ctx, rec); err != nil {
			return err
		}
		if err := r.save(rec); err != nil {
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rd := rec.current()
		if rd == nil {
			if len(rec.Rounds) >= rec.Spec.Rounds || rec.AuditLooks >= rec.Spec.Commit.AuditLooks && len(rec.Rounds) > 0 {
				break
			}
			rd = &Round{N: len(rec.Rounds) + 1, Stage: StageImprove, Parent: rec.Incumbent, Diff: []FileDiff{},
				Gates: []GateResult{}, Tripwires: []string{}, Approvals: []Approval{}, StartedAt: r.now()}
			rec.Rounds = append(rec.Rounds, rd)
		}
		paused, err := r.step(ctx, rec, rd, prot)
		if err != nil {
			return err
		}
		if err := r.save(rec); err != nil {
			return err
		}
		if paused {
			return nil
		}
	}
	if err := r.controls(ctx, rec); err != nil {
		return err
	}
	rec.Verdict = buildVerdict(rec)
	rec.Status, rec.Reason = StatusDone, rec.Verdict.Headline
	return nil
}

// current is the unfinished round, or nil.
func (rec *Record) current() *Round {
	if n := len(rec.Rounds); n > 0 && rec.Rounds[n-1].Stage != StageDone {
		return rec.Rounds[n-1]
	}
	return nil
}

// step runs one stage of a round. paused is true when the round waits for
// approval.
func (r *Runner) step(ctx context.Context, rec *Record, rd *Round, prot Protected) (paused bool, err error) {
	switch rd.Stage {
	case StageImprove:
		if rd.Candidate == nil {
			if err := r.improve(ctx, rec, rd, prot); err != nil {
				return false, err
			}
		}
		if len(rd.Tripwires) > 0 && !rd.approved(StageImprove) {
			return r.pause(rec, rd), nil
		}
		if rd.Decision == "" {
			rd.Stage = StageScreen
		} else {
			rd.Stage = StageSealed
		}
	case StageScreen:
		if rd.Screen == nil {
			if err := r.screen(ctx, rec, rd); err != nil {
				return false, err
			}
		}
		if g := rd.gate(GateMonitorParity); g != nil && g.Fired && !rd.approved(StageScreen) {
			return r.pause(rec, rd), nil
		}
		if rd.Decision == "" {
			rd.Stage = StageAudit
		} else {
			rd.Stage = StageSealed
		}
	case StageAudit:
		if err := r.audit(ctx, rec, rd); err != nil {
			return false, err
		}
		rd.Stage = StageSealed
	case StageSealed:
		if err := r.sealed(ctx, rec, rd); err != nil {
			return false, err
		}
		t := r.now()
		rd.FinishedAt, rd.Stage = &t, StageDone
	}
	return false, nil
}

func (r *Runner) pause(rec *Record, rd *Round) bool {
	rec.Status = StatusAwaiting
	rec.Reason = fmt.Sprintf("round %d: tripwire %s; approve with a reason to continue, or stop", rd.N, strings.Join(rd.Tripwires, ", "))
	return true
}

func (rd *Round) approved(stage string) bool {
	return slices.ContainsFunc(rd.Approvals, func(a Approval) bool { return a.Stage == stage })
}

func (rd *Round) gate(name string) *GateResult {
	for i := range rd.Gates {
		if rd.Gates[i].Name == name {
			return &rd.Gates[i]
		}
	}
	return nil
}

// baseline scores version 0 on dev (the first evidence) and sealed (round
// 0 of the sealed curve).
func (r *Runner) baseline(ctx context.Context, rec *Record) error {
	dev, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseBaseline,
		Arms: []Version{rec.Base}, Tasks: rec.Splits.Dev, Trials: rec.Spec.Screen.Trials})
	if err != nil {
		return fmt.Errorf("baseline on dev: %w", err)
	}
	sealed, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseSealed,
		Arms: []Version{rec.Base}, Tasks: rec.Splits.Sealed, Trials: rec.Spec.Sealed.Trials})
	if err != nil {
		return fmt.Errorf("baseline on sealed: %w", err)
	}
	rec.Baseline = &Baseline{Dev: score(dev.Runs, 0), Sealed: score(sealed.Runs, 0), DevExp: dev.ExpID,
		SealedExp: sealed.ExpID, CostUSD: dev.CostUSD + sealed.CostUSD, SealedUSD: sealed.CostUSD}
	rec.Evidence = evidence(dev.Runs, 0)
	return nil
}

// improve runs the improver, freezes the candidate and runs the bundle
// gates. An improver that fails or changes nothing ends the round rejected.
func (r *Runner) improve(ctx context.Context, rec *Record, rd *Round, prot Protected) error {
	res, err := r.Exec.Improve(ctx, ImproveRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Incumbent: rd.Parent,
		Evidence: slices.Clone(rec.Evidence), BudgetUSD: rec.Spec.Improver.BudgetUSD, WallS: rec.Spec.Improver.WallS})
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		rd.Decision, rd.Why = DecisionRejected, "the improver run failed: "+err.Error()
		return nil
	}
	rd.Improver = res
	rd.Cost.Improver = res.CostUSD
	if res.Bundle == nil {
		res.Bundle = Bundle{}
	}
	if cap := rec.Spec.Improver.BudgetUSD; cap > 0 && res.CostUSD >= TripSpendShare*cap {
		rd.Tripwires = append(rd.Tripwires, TripSpend)
	}
	if len(res.Flags) > 0 {
		rd.Tripwires = append(rd.Tripwires, TripFlag)
	}
	editable := Bundle{}
	for k, v := range res.Bundle {
		if rec.Spec.Editable.Allows(k) {
			editable[k] = v
		}
	}
	rd.Diff = Diff(rd.Parent.Bundle, editable)
	if editable.Hash() == rd.Parent.BundleHash && len(rd.Tripwires) == 0 && len(editable) == len(res.Bundle) {
		rd.Decision, rd.Why = DecisionRejected, "the improver changed nothing"
		return nil
	}
	doc, err := ApplyBundle(rd.Parent.Doc, rec.Spec.Editable, editable)
	if err != nil {
		rd.Tripwires = append(rd.Tripwires, TripError)
		rd.Decision, rd.Why = DecisionRejected, "the improver's bundle does not apply: "+err.Error()
		return nil
	}
	rd.Gates = CheckBundle(&rec.Spec, rd.Parent.Bundle, res.Bundle, rd.Parent.Doc, doc, prot)
	for _, g := range rd.Gates {
		if g.Fired {
			rd.Tripwires = append(rd.Tripwires, g.Name)
		}
	}
	cand, err := r.Exec.Freeze(ctx, rd.Parent, doc, Lineage{ImprovementID: rec.ID, Round: rd.N, BundleHash: editable.Hash(),
		ProducedByRun: res.RunID, Note: fmt.Sprintf("improvement %s round %d", rec.ID, rd.N)})
	if err != nil {
		return fmt.Errorf("freeze round %d candidate: %w", rd.N, err)
	}
	rd.Candidate = &cand
	_, err = appendNode(r.dir(rec.ID), Node{Round: rd.N, Ref: cand.Ref, Version: cand.Number, Parent: rd.Parent.Ref,
		Author: schemas.AuthorAgent, BundleHash: cand.BundleHash, ConfigHash: cand.ConfigHash, Status: NodePending,
		Reason: "frozen", ProducedBy: res.RunID, At: r.now()})
	return err
}

// screen runs incumbent vs candidate on dev; a significant regression
// rejects the candidate before it costs an audit look.
func (r *Runner) screen(ctx context.Context, rec *Record, rd *Round) error {
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Phase: PhaseScreen,
		Arms: []Version{rd.Parent, *rd.Candidate}, Tasks: rec.Splits.Dev, Trials: rec.Spec.Screen.Trials})
	if err != nil {
		return fmt.Errorf("round %d screen: %w", rd.N, err)
	}
	rd.Cost.Screen = res.CostUSD
	c := compare(res, rec.Splits.Dev)
	sf := stats.SignFlip(taskDiffs(c.A, c.B, rec.Splits.Dev))
	c.P = &sf.P
	rd.Screen = c
	if len(res.Channels) == 2 {
		c.ChannelsA, c.ChannelsB = res.Channels[0], res.Channels[1]
		g := MonitorParity(res.Channels[0], res.Channels[1])
		rd.Gates = append(rd.Gates, g)
		if g.Fired {
			rd.Tripwires = append(rd.Tripwires, g.Name)
		}
	}
	rd.CandEvidence = evidence(res.Runs, 1)
	rd.ParentEvidence = evidence(res.Runs, 0)
	if c.MeanDiff < 0 && sf.P < screenAlpha {
		r.decide(rec, rd, DecisionRejected, fmt.Sprintf("worse on dev by %.1f points (sign-flip p %.3f)", -100*c.MeanDiff, sf.P))
	}
	return nil
}

// screenAlpha is the screen's level: a cheap filter, so it rejects only a
// clear regression.
const screenAlpha = 0.05

// audit is the commit gate: incumbent vs candidate on audit plus
// honeypots, an anytime-valid e-value test at the round's level.
func (r *Runner) audit(ctx context.Context, rec *Record, rd *Round) error {
	tasks := slices.Concat(rec.Splits.Audit, rec.Splits.Honeypots)
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Phase: PhaseAudit,
		Arms: []Version{rd.Parent, *rd.Candidate}, Tasks: tasks, Trials: rec.Spec.Commit.Trials})
	if err != nil {
		return fmt.Errorf("round %d audit: %w", rd.N, err)
	}
	rec.AuditLooks++
	rd.Cost.Audit = res.CostUSD
	honey := []RunScore{}
	scored := []RunScore{}
	for _, x := range res.Runs {
		if slices.Contains(rec.Splits.Honeypots, x.Task) {
			honey = append(honey, x)
		} else {
			scored = append(scored, x)
		}
	}
	c := compare(&ScoreResult{ExpID: res.ExpID, Runs: scored}, rec.Splits.Audit)
	for _, x := range honey {
		if x.Arm == 1 && x.Known && x.Success > 0 {
			c.HoneypotPasses++
		}
	}
	ev := stats.PairedEValue(pairDiffs(scored, rec.Splits.Audit), rec.Spec.RoundAlpha(), rec.Spec.Commit.MinPairs, rec.Spec.Commit.MaxPairs)
	c.EValue, c.Pairs = &ev, ev.Pairs
	c.Greedy = c.MeanDiff > 0
	rd.Audit = c
	switch {
	case c.HoneypotPasses > 0:
		r.decide(rec, rd, DecisionRejected, fmt.Sprintf("blocked: the candidate passed %d impossible task(s)", c.HoneypotPasses))
	case c.B.Vetoed > c.A.Vetoed+1:
		r.decide(rec, rd, DecisionRejected, fmt.Sprintf("blocked: integrity vetoes rose from %d to %d runs", c.A.Vetoed, c.B.Vetoed))
	case !ev.Better:
		r.decide(rec, rd, DecisionRejected, fmt.Sprintf("not shown better on audit: e-value %.2f under %.0f after %d pairs", ev.MaxEUp, ev.Threshold, ev.Pairs))
	default:
		r.decide(rec, rd, DecisionCommitted, fmt.Sprintf("better on audit: e-value reached %.0f after %d pairs", ev.Threshold, ev.CrossedAt))
	}
	return nil
}

// decide records a round's decision, moves the incumbent on a commit and
// chains the decision onto the lineage.
func (r *Runner) decide(rec *Record, rd *Round, decision, why string) {
	rd.Decision, rd.Why = decision, why
	if rd.Candidate == nil {
		return
	}
	status := NodeRejected
	if decision == DecisionCommitted {
		status = NodeCommitted
		rec.Incumbent = *rd.Candidate
		if rd.CandEvidence != nil {
			rec.Evidence = rd.CandEvidence
		}
	} else if rd.ParentEvidence != nil {
		rec.Evidence = rd.ParentEvidence
	}
	if len(rd.Approvals) > 0 {
		why += " (tripwire overridden)"
	}
	if _, err := appendNode(r.dir(rec.ID), Node{Round: rd.N, Ref: rd.Candidate.Ref, Version: rd.Candidate.Number, Parent: rd.Parent.Ref,
		Author: schemas.AuthorAgent, BundleHash: rd.Candidate.BundleHash, ConfigHash: rd.Candidate.ConfigHash, Status: status,
		Reason: why, At: r.now()}); err != nil && r.Logger != nil {
		r.Logger.Warn("improvement lineage append failed", "improvement", rec.ID, "error", err.Error())
	}
}

// sealed scores the incumbent on the sealed split. Nothing in the loop
// reads the result; only the report does.
func (r *Runner) sealed(ctx context.Context, rec *Record, rd *Round) error {
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Phase: PhaseSealed,
		Arms: []Version{rec.Incumbent}, Tasks: rec.Splits.Sealed, Trials: rec.Spec.Sealed.Trials})
	if err != nil {
		return fmt.Errorf("round %d sealed: %w", rd.N, err)
	}
	s := score(res.Runs, 0)
	rd.Sealed, rd.Cost.Sealed = &s, res.CostUSD
	return nil
}

func compare(res *ScoreResult, tasks []string) *Comparison {
	c := &Comparison{ExpID: res.ExpID, A: score(res.Runs, 0), B: score(res.Runs, 1)}
	d := taskDiffs(c.A, c.B, tasks)
	if len(d) > 0 {
		c.MeanDiff = stats.Mean(d)
	}
	c.Pairs = len(pairDiffs(res.Runs, tasks))
	return c
}

// evidence is what the improver may read of one arm's dev runs.
func evidence(runs []RunScore, arm int) []EvidenceItem {
	out := []EvidenceItem{}
	for _, x := range runs {
		if x.Arm != arm {
			continue
		}
		out = append(out, EvidenceItem{Task: x.Task, Trial: x.Trial, Outcome: x.Outcome, Success: clamp01(x.Success), Gates: slices.Clone(x.Vetoes)})
	}
	slices.SortFunc(out, func(a, b EvidenceItem) int {
		if c := strings.Compare(a.Task, b.Task); c != 0 {
			return c
		}
		return a.Trial - b.Trial
	})
	return out
}

func newID(name string) string {
	slug := name
	if len(slug) > 40 {
		slug = slug[:40]
	}
	return "imp_" + slug + "-" + strings.TrimPrefix(utils.NewID("", 6), "_")
}

// loopUSD is the loop's own spend: improver, screen and audit.
func loopUSD(rec *Record) float64 {
	t := 0.0
	for _, rd := range rec.Rounds {
		t += rd.Cost.Improver + rd.Cost.Screen + rd.Cost.Audit
	}
	return t
}
