package improve

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
)

// Create freezes a spec: resolves version 0, partitions the task pool,
// reads the protected material of the audit and sealed tasks, and writes
// the record and the lineage root. Nothing runs yet.
func (r *Runner) Create(ctx context.Context, spec *Spec, actor string) (*Record, error) {
	spec.Defaults()
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	// A1.5, A1.6: an agent with its own gateway spending limit, a missing
	// improver task or an improver benchmark that was never admitted is
	// refused here, before any version is stored or any run is paid for.
	if err := r.Exec.CheckImprover(ctx, spec); err != nil {
		return nil, badImprover{err}
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
// marked stopped. A finished one is refused. A loop that is still inside an
// experiment when Stop saves keeps the stopped status (A1.7).
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
		rd.Decision, rd.Why = DecisionRejected, "stopped while paused: "+strings.Join(rd.Tripwires, ", ")
		if _, err := appendNode(r.dir(rec.ID), Node{Round: rd.N, Ref: rd.Candidate.Ref, Version: rd.Candidate.Number, Parent: rd.Parent.Ref,
			Author: schemas.AuthorAgent, BundleHash: rd.Candidate.BundleHash, ConfigHash: rd.Candidate.ConfigHash, Status: NodeTripped,
			Reason: rd.Why, At: r.now()}); err != nil {
			return rec, err
		}
	}
	rec.Status, rec.Reason = StatusStopped, "stopped by "+actor
	if err := r.save(rec); err != nil {
		return rec, err
	}
	if !r.Running(id) {
		r.event(rec)
	}
	return rec, nil
}

// acquire marks id as driven by this runner; release undoes it.
func (r *Runner) acquire(ctx context.Context, id string) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil {
		r.running = map[string]context.CancelFunc{}
	}
	if _, busy := r.running[id]; busy {
		cancel()
		return nil, nil, ErrBusy
	}
	r.running[id] = cancel
	return ctx, func() {
		cancel()
		r.mu.Lock()
		delete(r.running, id)
		r.mu.Unlock()
	}, nil
}

// Run drives an improvement until it finishes, pauses for approval, is
// stopped, or an experiment fails (status failed; Run again resumes). It
// accepts planned, failed and interrupted records.
func (r *Runner) Run(ctx context.Context, id string) (*Record, error) {
	ctx, release, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
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
	defer func() { r.event(rec) }()
	if err := rec.Splits.Verify(); err != nil {
		return r.fail(rec, err)
	}
	prot, err := r.loadProtected(id)
	if err != nil {
		return r.fail(rec, fmt.Errorf("read protected content: %w", err))
	}
	rec.Status, rec.Reason, rec.Error = StatusRunning, "running", ""
	if err := r.saveLive(rec); err != nil {
		return rec, err
	}
	if err := r.drive(ctx, rec, prot); err != nil {
		var hs *hardStopError
		switch {
		case errors.As(err, &hs):
			rec.Status, rec.Reason = StatusStopped, hs.msg
			return rec, r.saveLive(rec)
		case ctx.Err() != nil:
			rec.Status, rec.Reason = StatusPlanned, "interrupted; run again to resume"
			return rec, errors.Join(err, r.saveLive(rec))
		}
		return r.fail(rec, err)
	}
	return rec, r.saveLive(rec)
}

// event tells OnEvent that a record finished or paused.
func (r *Runner) event(rec *Record) {
	if r.OnEvent == nil || rec == nil {
		return
	}
	switch rec.Status {
	case StatusDone, StatusStopped, StatusFailed:
		r.OnEvent("improvement.finished", rec)
	case StatusAwaiting:
		r.OnEvent("improvement.paused", rec)
	}
}

func (r *Runner) fail(rec *Record, err error) (*Record, error) {
	rec.Status, rec.Reason, rec.Error = StatusFailed, "an experiment failed; run again to resume", err.Error()
	return rec, errors.Join(err, r.saveLive(rec))
}

// saveLive saves a record the loop is driving. A stopped status already on
// disk (Stop while an experiment ran) is kept, and the spend is updated.
func (r *Runner) saveLive(rec *Record) error {
	if disk, err := r.load(rec.ID); err == nil && disk.Status == StatusStopped && rec.Status != StatusStopped {
		rec.Status, rec.Reason = disk.Status, disk.Reason
	}
	rec.SpentUSD = totalUSD(rec)
	return r.save(rec)
}

// hardStopError ends a run before a step that would pass the spending
// limit (A2.10).
type hardStopError struct{ msg string }

func (e *hardStopError) Error() string { return e.msg }

// hardStop refuses the next step when the spend so far plus next would pass
// the run's spending limit.
func (r *Runner) hardStop(rec *Record, phase string, next float64) error {
	total := rec.Spec.Budget.TotalUSD
	spent := totalUSD(rec)
	rec.SpentUSD = spent
	if total <= 0 || spent+next <= total {
		return nil
	}
	return &hardStopError{fmt.Sprintf("Stopped before %s: the next step would pass the spending limit of $%.2f (spent $%.2f).", phase, total, spent)}
}

// perRunUSD is the expected cost of one scoring run: the mean so far when a
// run was scored, else the estimate's per-run cost, else 0.
func perRunUSD(rec *Record) float64 {
	if b := rec.Baseline; b != nil {
		if n := b.Dev.Runs + b.Sealed.Runs; n > 0 && b.CostUSD > 0 {
			return b.CostUSD / float64(n)
		}
	}
	if rec.Estimate != nil {
		return rec.Estimate.USD.PerRun
	}
	return 0
}

// scoreCost is the expected cost of a scoring call.
func scoreCost(rec *Record, arms, tasks, trials int) float64 {
	return float64(arms*tasks*trials) * perRunUSD(rec)
}

// drive runs the remaining stages, saving after each.
func (r *Runner) drive(ctx context.Context, rec *Record, prot Protected) error {
	if rec.Baseline == nil {
		if err := r.baseline(ctx, rec); err != nil {
			return err
		}
		if err := r.saveLive(rec); err != nil {
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rd := rec.current()
		if rd == nil {
			if len(rec.Rounds) >= rec.Spec.Rounds || rec.AuditLooks >= rec.Spec.Commit.AuditLooks && len(rec.Rounds) > 0 || earlyStop(rec) != "" {
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
		if err := r.saveLive(rec); err != nil {
			return err
		}
		if paused {
			return nil
		}
	}
	if err := r.finish(ctx, rec); err != nil {
		return err
	}
	rec.Status, rec.Reason = StatusDone, rec.Verdict.Headline
	return nil
}

// Early stop reasons (A3.5).
const (
	StopNothingToChange = "the improver found nothing more to change"
	StopNoIdeaBeat      = "no idea beat the current version for 2 rounds"
)

// earlyStop is why the loop ends before its last round, or "".
func earlyStop(rec *Record) string {
	n := len(rec.Rounds)
	if n < 2 {
		return ""
	}
	last := rec.Rounds[n-2:]
	nothing, noBeat := true, true
	for _, rd := range last {
		nothing = nothing && rd.Stage == StageDone && nothingChanged(rd)
		noBeat = noBeat && rd.Stage == StageDone && strings.HasPrefix(rd.Why, screenWhyPrefix)
	}
	switch {
	case nothing:
		return StopNothingToChange
	case noBeat:
		return StopNoIdeaBeat
	}
	return ""
}

// nothingChanged reports a round where no slot returned a change.
func nothingChanged(rd *Round) bool {
	if rd.Candidate != nil {
		return false
	}
	for _, c := range rd.Candidates {
		if c.Dropped != whyNoChange {
			return false
		}
	}
	return rd.Why == whyNoChange
}

const (
	whyNoChange     = "the improver changed nothing"
	screenWhyPrefix = "Not tested on the gate: no idea beat the current version on the practice tasks"
)

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
		if rd.Candidates == nil && rd.Decision == "" {
			if err := r.improve(ctx, rec, rd, prot); err != nil {
				return false, err
			}
		}
		if len(rd.Tripwires) > 0 && rd.Decision == "" {
			if !rd.approved(StageImprove) {
				return r.pause(rec, rd), nil
			}
			for i := range rd.Candidates { // the approval lets the paused idea go on
				if c := &rd.Candidates[i]; c.Version != nil && rd.Candidate != nil && c.Version.Ref == rd.Candidate.Ref {
					c.Dropped = ""
				}
			}
		}
		if rd.Decision == "" {
			rd.Stage = StageScreen
		} else {
			rd.Stage = StageSealed
		}
	case StageScreen:
		if rd.Screen == nil && rd.Decision == "" {
			if err := r.screen(ctx, rec, rd); err != nil {
				return false, err
			}
		}
		if g := rd.gate(GateMonitorParity); g != nil && g.Fired && rd.Decision == "" && !rd.approved(StageScreen) {
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
		if rec.Spec.Sealed.Curve && rd.Decision == DecisionCommitted && rd.Sealed == nil {
			if err := r.sealed(ctx, rec, rd); err != nil {
				return false, err
			}
		}
		t := r.now()
		rd.FinishedAt, rd.Stage = &t, StageDone
	}
	return false, nil
}

func (r *Runner) pause(rec *Record, rd *Round) bool {
	rec.Status = StatusAwaiting
	rec.Reason = fmt.Sprintf("round %d paused: %s; approve with a reason to continue, or stop", rd.N, strings.Join(rd.Tripwires, ", "))
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

func (r *Runner) proposer() Proposer {
	switch {
	case r.Proposer != nil:
		return r.Proposer
	case DefaultProposer != nil:
		return DefaultProposer
	}
	return keepSetProposer{}
}

func (r *Runner) screener() Screener {
	switch {
	case r.Screener != nil:
		return r.Screener
	case DefaultScreener != nil:
		return DefaultScreener
	}
	return halving{}
}

func (r *Runner) commitRule() CommitRule {
	if r.Commit != nil {
		return r.Commit
	}
	return DefaultCommitRule
}

func (r *Runner) claimRule() ClaimRule {
	if r.Claim != nil {
		return r.Claim
	}
	return DefaultClaimRule
}

func (r *Runner) evidenceBuilder() EvidenceBuilder {
	if r.Evidence != nil {
		return r.Evidence
	}
	return DefaultEvidenceBuilder
}

// measure stores what an experiment is planned to be before it starts
// (A1.2). Audit and sealed measures hold only a task list hash and count.
func (r *Runner) measure(rec *Record, rd *Round, key, phase string, arms []Version, tasks []string, trials int, level float64) error {
	if r.Objects.Root == "" {
		return nil
	}
	if rd != nil && rd.Measures[key] != "" {
		return nil
	}
	ts := slices.Clone(tasks)
	slices.Sort(ts)
	th, err := utils.HashJSON(ts)
	if err != nil {
		return err
	}
	m := MeasureObject{Phase: phase, Look: len(rec.Looks) + 1, TasksHash: th, NTasks: len(ts), Trials: trials, Level: level,
		Seed: rec.Spec.Seed, SpecHash: rec.SpecHash, Arms: []string{}}
	if phase != PhaseAudit {
		m.Look = 0
	}
	if rd != nil {
		m.Round = rd.N
	}
	for _, a := range arms {
		m.Arms = append(m.Arms, a.ConfigHash)
	}
	h, err := r.Objects.Put(ObjMeasure, m)
	if err != nil {
		return err
	}
	if rd != nil {
		if rd.Measures == nil {
			rd.Measures = map[string]string{}
		}
		rd.Measures[key] = h
	}
	return nil
}

// baseline scores version 0 on dev (the first evidence) and, when the
// held-out curve is on, on sealed (round 0 of the curve).
func (r *Runner) baseline(ctx context.Context, rec *Record) error {
	if err := r.measure(rec, nil, PhaseBaseline, PhaseBaseline, []Version{rec.Base}, rec.Splits.Dev, rec.Spec.Screen.Trials, 0); err != nil {
		return err
	}
	if err := r.hardStop(rec, "the first practice check", scoreCost(rec, 1, len(rec.Splits.Dev), rec.Spec.Screen.Trials)); err != nil {
		return err
	}
	dev, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseBaseline,
		Arms: []Version{rec.Base}, Tasks: rec.Splits.Dev, Trials: rec.Spec.Screen.Trials})
	if err != nil {
		return fmt.Errorf("baseline on practice tasks: %w", err)
	}
	rec.Baseline = &Baseline{Dev: score(dev.Runs, 0), DevExp: dev.ExpID, CostUSD: dev.CostUSD, Sealed: ArmScore{PerTask: map[string]float64{}}}
	rec.Evidence = evidence(dev.Runs, 0)
	if !rec.Spec.Sealed.Curve {
		return nil
	}
	if err := r.hardStop(rec, "the first held-out check", scoreCost(rec, 1, len(rec.Splits.Sealed), rec.Spec.Sealed.Trials)); err != nil {
		return err
	}
	sealed, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseSealed,
		Arms: []Version{rec.Base}, Tasks: rec.Splits.Sealed, Trials: rec.Spec.Sealed.Trials})
	if err != nil {
		rec.Baseline = nil
		return fmt.Errorf("baseline on held-out tasks: %w", err)
	}
	rec.Baseline.Sealed, rec.Baseline.SealedExp = score(sealed.Runs, 0), sealed.ExpID
	rec.Baseline.CostUSD += sealed.CostUSD
	rec.Baseline.SealedUSD = sealed.CostUSD
	return nil
}

// evidenceDoc builds the evidence document of the next ideas from the dev
// runs scored so far (A3.1). Nil when no builder is set.
func (r *Runner) evidenceDoc(ctx context.Context, rec *Record, rd *Round, prot Protected) (*EvidenceDoc, error) {
	b := r.evidenceBuilder()
	if b == nil {
		return nil, nil
	}
	exps := []string{}
	if rec.Baseline != nil && rec.Baseline.DevExp != "" {
		exps = append(exps, rec.Baseline.DevExp)
	}
	hist := []HistoryEntry{}
	for _, p := range rec.Rounds {
		for _, s := range p.Stages {
			if s.ExpID != "" && !slices.Contains(exps, s.ExpID) {
				exps = append(exps, s.ExpID)
			}
		}
		for _, c := range p.Candidates {
			h := HistoryEntry{Round: p.N, Slot: c.Slot, Kind: c.Kind, PracticeDiff: c.DevDiff, Sentence: p.Why, Edits: []string{}}
			for _, e := range c.Edits {
				h.Edits = append(h.Edits, e.Op+" "+e.ID+e.Path)
			}
			hist = append(hist, h)
		}
	}
	refs := []RunRef{}
	for _, id := range exps {
		rs, err := r.Exec.Runs(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read practice runs of %s: %w", id, err)
		}
		for _, x := range rs {
			if slices.Contains(rec.Splits.Dev, x.Task) { // dev runs only
				refs = append(refs, x)
			}
		}
	}
	pb := PlaybookObject{Items: []PlaybookItem{}}
	if h := rd.Parent.PlaybookObj; h != "" && r.Objects.Root != "" {
		if err := r.Objects.Get(h, ObjPlaybook, &pb); err != nil {
			return nil, err
		}
	}
	return b.Build(ctx, EvidenceRequest{ImprovementID: rec.ID, Round: rd.N, Runs: refs, Prot: prot, History: hist, Playbook: pb,
		Limits: EvidenceLimits{DigestTokensPerRun: 6000, DigestTokensTotal: 60000, RawBytesPerRun: 2 << 20, RawBytesTotal: 50 << 20, Clusters: 6, Exemplars: 2}})
}

// improve runs the round's ideas in parallel (A3.4), freezes each changed
// bundle and runs the bundle gates on it. An idea that trips a gate is
// dropped; the round pauses only when every idea tripped. An improver that
// fails or changes nothing drops its idea.
func (r *Runner) improve(ctx context.Context, rec *Record, rd *Round, prot Protected) error {
	props := r.proposer().Plan(rec, rd.N)
	if len(props) == 0 {
		props = []ProposalSpec{{Slot: 1, Kind: KindMutation, Source: rd.Parent, Seed: proposalSeed(&rec.Spec, rd.N, 1)}}
	}
	llm := 0
	for _, p := range props {
		if p.Kind != KindMerge {
			llm++
		}
	}
	if err := r.hardStop(rec, "the improver", float64(llm)*rec.Spec.Improver.BudgetUSD); err != nil {
		return err
	}
	doc, err := r.evidenceDoc(ctx, rec, rd, prot)
	if err != nil {
		return err
	}
	trackOpened(rec, rd, doc)
	type out struct {
		res *ImproveResult
		err error
	}
	outs := make([]out, len(props))
	var wg sync.WaitGroup
	for i, p := range props {
		if p.Kind == KindMerge {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := r.Exec.Improve(ctx, ImproveRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Incumbent: p.Source,
				Evidence: slices.Clone(rec.Evidence), EvidenceDoc: doc, BudgetUSD: rec.Spec.Improver.BudgetUSD, WallS: rec.Spec.Improver.WallS,
				Kind: p.Kind, Second: p.Second, Model: rec.Spec.Improver.Model, Provider: rec.Spec.Improver.Provider, Seed: p.Seed, Limits: DefaultLimits})
			outs[i] = out{res, err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	rd.Candidates = []CandidateRun{}
	for i, p := range props {
		c := CandidateRun{ID: fmt.Sprintf("c%d", p.Slot), Slot: p.Slot, Kind: p.Kind, Source: p.Source.Ref}
		var res *ImproveResult
		if p.Kind == KindMerge {
			res, err = r.merge(rec, p)
		} else {
			res, err = outs[i].res, outs[i].err
		}
		if err != nil {
			c.Dropped = "the improver run failed: " + err.Error()
			rd.Candidates = append(rd.Candidates, c)
			continue
		}
		if doc != nil && res.Edits != nil {
			// Rule 2 of A3.3: a ref the improver made up is not evidence.
			known := map[string]bool{}
			for _, it := range doc.Items {
				if it.RunRef != "" {
					known[it.RunRef] = true
				}
			}
			e := KeepKnownRefs(*res.Edits, known)
			res.Edits = &e
		}
		if err := r.candidate(ctx, rec, rd, p, res, &c, prot); err != nil {
			return err
		}
		rd.Cost.Improver += res.CostUSD
		if p.Slot == 1 {
			rd.Improver = res
		}
		rd.Candidates = append(rd.Candidates, c)
	}
	live, tripped := 0, 0
	for _, c := range rd.Candidates {
		switch {
		case c.Version != nil && len(c.Tripwires) > 0:
			tripped++
		case c.Version != nil && c.Dropped == "":
			live++
		}
	}
	first := rd.Candidates[0]
	if first.Version != nil {
		rd.Candidate = first.Version
		rd.Diff = Diff(rd.Parent.Bundle, first.Version.Bundle)
		rd.Gates = slices.Clone(first.Gates)
	}
	if live == 0 && tripped > 0 {
		// Every idea tripped: pause on the first tripped idea (slot order);
		// an approval lets that idea go on.
		for _, c := range rd.Candidates {
			if c.Version != nil && len(c.Tripwires) > 0 {
				rd.Candidate = c.Version
				rd.Diff = Diff(rd.Parent.Bundle, c.Version.Bundle)
				rd.Gates = slices.Clone(c.Gates)
				rd.Tripwires = slices.Clone(c.Tripwires)
				break
			}
		}
		return nil
	}
	if live == 0 {
		why := first.Dropped
		if why == "" {
			why = "every idea was dropped"
		}
		for _, c := range rd.Candidates {
			if c.Version == nil {
				rd.Tripwires = append(rd.Tripwires, c.Tripwires...)
			}
		}
		rd.Decision, rd.Why = DecisionRejected, why
	}
	return nil
}

// candidate turns one improver result into a frozen, gated idea.
func (r *Runner) candidate(ctx context.Context, rec *Record, rd *Round, p ProposalSpec, res *ImproveResult, c *CandidateRun, prot Protected) error {
	c.CostUSD = res.CostUSD
	if res.Bundle == nil {
		res.Bundle = Bundle{}
	}
	pbHash := p.Source.PlaybookObj
	if DefaultEditApplier != nil && res.Edits != nil && p.Kind != KindMerge {
		pb := PlaybookObject{Items: []PlaybookItem{}}
		if pbHash != "" && r.Objects.Root != "" {
			if err := r.Objects.Get(pbHash, ObjPlaybook, &pb); err != nil {
				return err
			}
		}
		// The parent's raw paths stay raw, so the placeholder rule applies
		// only to templated files.
		parent, err := BundleObjectOf(p.Source.Doc, p.Source.Bundle)
		if err != nil {
			return err
		}
		nb, npb, applied, err := DefaultEditApplier(parent, pb, *res.Edits, rec.Spec.Editable, DefaultLimits, p.Kind, rd.N)
		if errors.Is(err, ErrNothingApplied) {
			// Every edit was refused or there was none: no idea, not a tripwire.
			c.Edits, c.Dropped = applied, whyNoChange
			return nil
		}
		if err != nil {
			c.Tripwires = append(c.Tripwires, TripError)
			c.Dropped = "the improver's edits do not apply: " + err.Error()
			return nil
		}
		if p.Slot == 1 && p.Kind == KindMutation {
			nb, npb, applied = r.prune(rec, rd, nb, npb, applied)
		}
		c.Edits = applied
		res.Bundle = Bundle(nb.Files)
		if r.Objects.Root != "" {
			h, err := r.Objects.Put(ObjPlaybook, npb)
			if err != nil {
				return err
			}
			pbHash = h
		}
	}
	if cap := rec.Spec.Improver.BudgetUSD; cap > 0 && res.CostUSD >= TripSpendShare*cap {
		c.Tripwires = append(c.Tripwires, TripSpend)
	}
	if len(res.Flags) > 0 {
		c.Tripwires = append(c.Tripwires, TripFlag)
	}
	editable := Bundle{}
	for k, v := range res.Bundle {
		if rec.Spec.Editable.Allows(k) {
			editable[k] = v
		}
	}
	if editable.Hash() == p.Source.BundleHash && len(c.Tripwires) == 0 && len(editable) == len(res.Bundle) {
		c.Dropped = whyNoChange
		return nil
	}
	doc, err := ApplyBundle(p.Source.Doc, rec.Spec.Editable, editable)
	if err != nil {
		c.Tripwires = append(c.Tripwires, TripError)
		c.Dropped = "the improver's bundle does not apply: " + err.Error()
		return nil
	}
	c.Gates = CheckBundle(&rec.Spec, p.Source.Bundle, res.Bundle, p.Source.Doc, doc, prot)
	for _, g := range c.Gates {
		if g.Fired {
			c.Tripwires = append(c.Tripwires, g.Name)
		}
	}
	cand, err := r.Exec.Freeze(ctx, p.Source, doc, Lineage{ImprovementID: rec.ID, Round: rd.N, BundleHash: editable.Hash(),
		ProducedByRun: res.RunID, Note: fmt.Sprintf("improvement %s round %d idea %d", rec.ID, rd.N, p.Slot), Editable: rec.Spec.Editable})
	if err != nil {
		return fmt.Errorf("freeze round %d idea %d: %w", rd.N, p.Slot, err)
	}
	if r.Objects.Root != "" {
		if err := r.candidateObject(rec, rd, p, res, &cand, pbHash, c); err != nil {
			return err
		}
	}
	c.Version = &cand
	if len(c.Tripwires) > 0 {
		c.Dropped = "tripped: " + strings.Join(c.Tripwires, ", ")
	}
	_, err = appendNode(r.dir(rec.ID), Node{Round: rd.N, Ref: cand.Ref, Version: cand.Number, Parent: p.Source.Ref,
		Author: schemas.AuthorAgent, BundleHash: cand.BundleHash, ConfigHash: cand.ConfigHash, Status: NodePending,
		Reason: "frozen", ProducedBy: res.RunID, At: r.now()})
	return err
}

// candidateObject writes the idea's bundle and candidate objects (A1.2).
func (r *Runner) candidateObject(rec *Record, rd *Round, p ProposalSpec, res *ImproveResult, cand *Version, pbHash string, c *CandidateRun) error {
	put := func(b Bundle) (string, error) {
		return r.Objects.Put(ObjBundle, BundleObject{Files: map[string]string(b.Clone()), Raw: []string{}})
	}
	parent, err := put(p.Source.Bundle)
	if err != nil {
		return err
	}
	bh, err := put(cand.Bundle)
	if err != nil {
		return err
	}
	o := CandidateObject{Source: p.Kind, ParentBundle: parent, Bundle: bh, Playbook: pbHash, ConfigHash: cand.ConfigHash,
		Improver: rec.Spec.Improver.Task, Model: rec.Spec.Improver.Model, RunIDs: []string{}, Round: rd.N, Slot: p.Slot}
	if p.Second != nil {
		if o.SecondBundle, err = put(p.Second.Bundle); err != nil {
			return err
		}
	}
	if res.RunID != "" {
		o.RunIDs = append(o.RunIDs, res.RunID)
	}
	if res.Edits != nil {
		if o.Edits, err = r.Objects.Put(ObjEdits, res.Edits); err != nil {
			return err
		}
	}
	h, err := r.Objects.Put(ObjCandidate, o)
	if err != nil {
		return err
	}
	c.Obj, cand.BundleObj, cand.PlaybookObj = h, bh, pbHash
	if rd.ParentObj == "" {
		rd.ParentObj = parent
	}
	return nil
}

// merge combines two keep set members without a model call (A3.4): the
// second member's applied edits are replayed on the first member's bundle;
// an edit on an item the first member also edited is skipped, so the member
// that led more practice tasks wins a conflict.
func (r *Runner) merge(rec *Record, p ProposalSpec) (*ImproveResult, error) {
	if DefaultEditApplier == nil || p.Second == nil {
		return nil, errors.New("this build cannot merge ideas")
	}
	editsOf := func(ref string) []AppliedEdit {
		for _, rd := range rec.Rounds {
			for _, c := range rd.Candidates {
				if c.Version != nil && c.Version.Ref == ref {
					return c.Edits
				}
			}
		}
		return nil
	}
	mine := map[string]bool{}
	for _, e := range editsOf(p.Source.Ref) {
		if e.ID != "" {
			mine[e.ID] = true
		}
	}
	ef := EditsFile{Rationale: "merge of two kept ideas", Edits: []Edit{}}
	for _, e := range editsOf(p.Second.Ref) {
		if e.Result == "applied" && (e.ID == "" || !mine[e.ID]) {
			ef.Edits = append(ef.Edits, e.Edit)
		}
	}
	if len(ef.Edits) == 0 {
		return nil, errors.New("the two ideas have no edits to combine")
	}
	return &ImproveResult{Edits: &ef, Bundle: p.Source.Bundle.Clone()}, nil
}

// screen is Sequential Halving on dev (A2.3): the round's live ideas run
// chunk by chunk, half are cut after each chunk, and the one left goes to
// the gate only when its mean practice difference is above 0.
func (r *Runner) screen(ctx context.Context, rec *Record, rd *Round) error {
	alive := []string{}
	byID := map[string]*CandidateRun{}
	for i := range rd.Candidates {
		c := &rd.Candidates[i]
		byID[c.ID] = c
		if c.Version != nil && c.Dropped == "" {
			alive = append(alive, c.ID)
		}
	}
	k := len(alive)
	sc := r.screener()
	levels := sc.Levels(k, rec.Splits.Dev, rec.Spec.Seed*1000+int64(rd.N))
	incDev, incCh, cached := r.incumbentDev(rec, rd)
	incAll := maps2(incDev)
	diffs := map[string][]float64{}
	dev := map[string]map[string]float64{}
	cost := map[string]float64{}
	evi := map[string][]EvidenceItem{}
	parentEvi := []EvidenceItem{}
	words := map[string]int{}
	for _, id := range alive {
		words[id] = wordCount(byID[id].Version.Bundle)
		dev[id] = map[string]float64{}
	}
	parity := map[string]GateResult{}
	candCh := map[string][]string{}
	lastExp := ""
	survivors := slices.Clone(alive)
	for j, chunk := range levels {
		if len(survivors) == 0 {
			break
		}
		arms := []Version{}
		runInc := !cached || !hasAll(incDev, chunk)
		if runInc {
			arms = append(arms, rd.Parent)
		}
		for _, id := range survivors {
			arms = append(arms, *byID[id].Version)
		}
		key := fmt.Sprintf("%s-%d", PhaseScreen, j+1)
		if err := r.measure(rec, rd, key, PhaseScreen, arms, chunk, rec.Spec.Screen.Trials, 0); err != nil {
			return err
		}
		if err := r.hardStop(rec, "the practice check", scoreCost(rec, len(arms), len(chunk), rec.Spec.Screen.Trials)); err != nil {
			return err
		}
		res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Phase: PhaseScreen,
			Arms: arms, Tasks: chunk, Trials: rec.Spec.Screen.Trials})
		if err != nil {
			return fmt.Errorf("round %d practice check: %w", rd.N, err)
		}
		lastExp = res.ExpID
		rd.Cost.Screen += res.CostUSD
		off := 0
		inc := incDev
		if runInc {
			off = 1
			m, _ := TaskMeans(res.Runs, 0, chunk)
			for t, v := range m {
				incAll[t] = v
			}
			inc = incAll
			parentEvi = append(parentEvi, evidence(res.Runs, 0)...)
			if len(res.Channels) > 0 {
				incCh = res.Channels[0]
			}
		}
		for i, id := range survivors {
			arm := i + off
			m, _ := TaskMeans(res.Runs, arm, chunk)
			for t, v := range m {
				dev[id][t] = v
			}
			diffs[id] = append(diffs[id], TaskDiffs(inc, m, chunk)...)
			evi[id] = append(evi[id], evidence(res.Runs, arm)...)
			for _, x := range res.Runs {
				if x.Arm == arm {
					cost[id] += x.CostUSD
				}
			}
			if arm < len(res.Channels) {
				candCh[id] = res.Channels[arm]
			}
			if incCh != nil && arm < len(res.Channels) {
				g := MonitorParity(incCh, res.Channels[arm])
				c := byID[id]
				if _, seen := parity[id]; g.Fired && !seen {
					c.Gates = append(c.Gates, g)
					c.Tripwires = append(c.Tripwires, g.Name)
					parity[id] = g
				}
			}
		}
		in := CutInput{Diffs: map[string][]float64{}, Words: words, Cost: cost, Keep: (len(survivors) + 1) / 2}
		if j == len(levels)-1 {
			in.Keep = 1
		}
		for _, id := range survivors {
			if _, tripped := parity[id]; !tripped {
				in.Diffs[id] = diffs[id]
			}
		}
		if len(in.Diffs) == 0 { // every survivor tripped the parity check: keep the best for an approval
			for _, id := range survivors {
				in.Diffs[id] = diffs[id]
			}
		}
		cut := sc.Cut(in)
		for id, why := range cut.Dropped {
			byID[id].Dropped = why
		}
		for _, id := range survivors {
			if _, ok := in.Diffs[id]; !ok && byID[id].Dropped == "" {
				byID[id].Dropped = "tripped: " + strings.Join(byID[id].Tripwires, ", ")
			}
		}
		rd.Stages = append(rd.Stages, StageResult{Level: j + 1, Tasks: len(chunk), ExpID: res.ExpID, Survivors: slices.Clone(cut.Keep)})
		survivors = cut.Keep
	}
	for _, id := range alive {
		c := byID[id]
		c.DevDiff, c.DevTasks = mean(diffs[id]), len(diffs[id])
	}
	updateKeep(rec, rd, dev, incAll)
	if err := r.updateCounters(alive, byID, dev, incAll); err != nil {
		return err
	}
	best, bestID := math.Inf(-1), ""
	for _, id := range alive {
		if m := mean(diffs[id]); len(diffs[id]) > 0 && m > best {
			best, bestID = m, id
		}
	}
	if len(survivors) > 0 {
		bestID = survivors[0]
		best = mean(diffs[bestID])
	}
	if bestID == "" {
		best = 0
	}
	w := byID[bestID]
	rd.Screen = &Comparison{ExpID: lastExp, A: armScore(incAll)}
	if w != nil {
		rd.Screen.B = armScore(dev[bestID])
		rd.Screen.MeanDiff = best
		rd.Screen.Pairs = len(diffs[bestID])
		p := stats.SignFlipUpper(diffs[bestID]).P
		rd.Screen.P = &p
		rd.Screen.ChannelsA, rd.Screen.ChannelsB = incCh, candCh[bestID]
		rd.CandEvidence = evi[bestID]
	}
	if len(parentEvi) > 0 {
		rd.ParentEvidence = parentEvi
	}
	if len(survivors) == 0 || best <= 0 {
		r.decide(rec, rd, DecisionRejected, fmt.Sprintf("%s (%s points for the best of %d).", screenWhyPrefix, formatPoints(best), k))
		return nil
	}
	rd.Winner = bestID
	rd.Candidate = w.Version
	rd.Diff = Diff(rd.Parent.Bundle, w.Version.Bundle)
	if g, ok := parity[bestID]; ok {
		rd.Gates = append(rd.Gates, g)
		rd.Tripwires = append(rd.Tripwires, g.Name)
	}
	return nil
}

// incumbentDev is the incumbent's cached per-task practice results and its
// event channels: version 0's from the baseline, or a committed winner's
// from the screen that chose it. cached is false when the screen must run
// the incumbent as arm A (version 0 has no stored channels, so its first
// screen runs it).
func (r *Runner) incumbentDev(rec *Record, rd *Round) (map[string]float64, []string, bool) {
	for i := len(rec.Rounds) - 1; i >= 0; i-- {
		p := rec.Rounds[i]
		if p == rd || p.Decision != DecisionCommitted || p.Screen == nil || p.Candidate == nil {
			continue
		}
		if p.Candidate.ConfigHash != rd.Parent.ConfigHash {
			break
		}
		for _, c := range p.Candidates {
			if c.ID == p.Winner && c.Version != nil {
				ch := []string(nil)
				if rs := p.Screen; rs != nil {
					ch = rs.ChannelsB
				}
				return maps2(p.Screen.B.PerTask), ch, ch != nil
			}
		}
	}
	if rec.Baseline != nil && rd.Parent.ConfigHash == rec.Base.ConfigHash {
		return maps2(rec.Baseline.Dev.PerTask), nil, false
	}
	return map[string]float64{}, nil, false
}

func maps2(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func hasAll(m map[string]float64, tasks []string) bool {
	for _, t := range tasks {
		if _, ok := m[t]; !ok {
			return false
		}
	}
	return true
}

// armScore folds per-task means into an ArmScore.
func armScore(m map[string]float64) ArmScore {
	s := ArmScore{PerTask: maps2(m)}
	for _, v := range m {
		s.Mean += v
	}
	if len(m) > 0 {
		s.Mean /= float64(len(m))
	}
	s.Runs, s.Known = len(m), len(m)
	return s
}

// audit is the commit gate (A2.2): incumbent vs the screen's winner on audit
// plus honeypots, the one-sided exact sign-flip test on per-task means at
// the level the online error budget gives the next look. A look is recorded
// only when the test ran.
func (r *Runner) audit(ctx context.Context, rec *Record, rd *Round) error {
	if rd.Candidate == nil {
		r.decide(rec, rd, DecisionRejected, "no idea reached the gate")
		return nil
	}
	rule := r.commitRule()
	level := rule.Level(&rec.Spec, rec.Looks)
	tasks := slices.Concat(rec.Splits.Audit, rec.Splits.Honeypots)
	arms := []Version{rd.Parent, *rd.Candidate}
	if err := r.measure(rec, rd, PhaseAudit, PhaseAudit, arms, tasks, rec.Spec.Commit.Trials, level); err != nil {
		return err
	}
	if err := r.hardStop(rec, "the gate check", scoreCost(rec, 2, len(tasks), rec.Spec.Commit.Trials)); err != nil {
		return err
	}
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Phase: PhaseAudit,
		Arms: arms, Tasks: tasks, Trials: rec.Spec.Commit.Trials})
	if err != nil {
		return fmt.Errorf("round %d gate check: %w", rd.N, err)
	}
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
	c.Greedy = c.MeanDiff > 0
	rd.Audit = c
	a, _ := TaskMeans(scored, 0, rec.Splits.Audit)
	b, _ := TaskMeans(scored, 1, rec.Splits.Audit)
	d := TaskDiffs(a, b, rec.Splits.Audit)
	missing := len(rec.Splits.Audit) - len(d)
	look := func(dec CommitDecision) {
		rd.Commit = &dec
		if !dec.Skipped {
			rec.Looks = append(rec.Looks, LookRecord{Round: rd.N, Level: dec.Level, Committed: dec.Commit})
		}
		rec.AuditLooks = len(rec.Looks)
	}
	switch {
	case c.HoneypotPasses > 0:
		why := fmt.Sprintf("blocked: the candidate passed %d impossible task(s)", c.HoneypotPasses)
		look(CommitDecision{Level: level, N: len(d), Missing: missing, P: 1, Mean: mean(d), NNonzero: nonzero(d), Why: why})
		r.decide(rec, rd, DecisionRejected, why)
	case c.B.Vetoed > c.A.Vetoed+1:
		why := fmt.Sprintf("blocked: flagged runs rose from %d to %d", c.A.Vetoed, c.B.Vetoed)
		look(CommitDecision{Level: level, N: len(d), Missing: missing, P: 1, Mean: mean(d), NNonzero: nonzero(d), Why: why})
		r.decide(rec, rd, DecisionRejected, why)
	default:
		dec := rule.Decide(d, missing, len(rec.Splits.Audit), level)
		look(dec)
		if dec.Commit {
			r.decide(rec, rd, DecisionCommitted, dec.Why)
		} else {
			r.decide(rec, rd, DecisionRejected, dec.Why)
		}
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
		why += " (a paused check was approved)"
	}
	if _, err := appendNode(r.dir(rec.ID), Node{Round: rd.N, Ref: rd.Candidate.Ref, Version: rd.Candidate.Number, Parent: rd.Parent.Ref,
		Author: schemas.AuthorAgent, BundleHash: rd.Candidate.BundleHash, ConfigHash: rd.Candidate.ConfigHash, Status: status,
		Reason: why, At: r.now()}); err != nil && r.Logger != nil {
		r.Logger.Warn("improvement lineage append failed", "improvement", rec.ID, "error", err.Error())
	}
}

// sealed scores the new incumbent on the sealed split for the held-out
// curve (A2.4). Nothing in the loop reads the result; only the report does.
func (r *Runner) sealed(ctx context.Context, rec *Record, rd *Round) error {
	if err := r.measure(rec, rd, PhaseSealed, PhaseSealed, []Version{rec.Incumbent}, rec.Splits.Sealed, rec.Spec.Sealed.Trials, 0); err != nil {
		return err
	}
	if err := r.hardStop(rec, "the held-out curve", scoreCost(rec, 1, len(rec.Splits.Sealed), rec.Spec.Sealed.Trials)); err != nil {
		return err
	}
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: rd.N, Phase: PhaseSealed,
		Arms: []Version{rec.Incumbent}, Tasks: rec.Splits.Sealed, Trials: rec.Spec.Sealed.Trials})
	if err != nil {
		return fmt.Errorf("round %d held-out scoring: %w", rd.N, err)
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

// GateTest is the commit test on its own: B minus A per (task, trial),
// ordered by task then trial, under an anytime-valid e-value test.
func GateTest(runs []RunScore, tasks []string, alpha float64, minPairs, maxPairs int) stats.EValueTest {
	return stats.PairedEValue(pairDiffs(runs, tasks), alpha, minPairs, maxPairs)
}
