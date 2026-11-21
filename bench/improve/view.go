package improve

import (
	"time"

	"github.com/maximhq/bifrost/bench/stats"
)

// Summary is one improvement in the list.
type Summary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Agent      string    `json:"agent"`
	Suite      string    `json:"suite"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason"`
	Running    bool      `json:"running"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	CreatedBy  string    `json:"created_by"`
	Rounds     int       `json:"rounds"`
	RoundsDone int       `json:"rounds_done"`
	Commits    int       `json:"commits"`
	Incumbent  string    `json:"incumbent"`
	Verdict    string    `json:"verdict,omitempty"`
	SealedGain *float64  `json:"sealed_gain,omitempty"`
	TotalUSD   float64   `json:"total_usd"`
}

// List is GET /improvements.
type List struct {
	Improvements []Summary `json:"improvements"`
}

// Detail is GET /improvements/{id}: the report. Sealed and audit task ids
// are never served; their scores are.
type Detail struct {
	Summary
	SpecHash      string         `json:"spec_hash"`
	Spec          Spec           `json:"spec"`
	Splits        SplitsView     `json:"splits"`
	Base          Version        `json:"base"`
	Curve         []CurvePoint   `json:"curve"`
	Band          *ControlBand   `json:"control_band,omitempty"`
	Rounds        []RoundView    `json:"round_views"`
	Lineage       LineageView    `json:"lineage"`
	Integrity     []IntegrityRow `json:"integrity"`
	Cost          CostView       `json:"cost"`
	Verdict       *Verdict       `json:"verdict_detail,omitempty"`
	Error         string         `json:"error,omitempty"`
	AuditLooks    int            `json:"audit_looks"`
	AuditLooksMax int            `json:"audit_looks_max"`
}

// SplitsView is the partition as served: dev task ids, audit and sealed
// counts only.
type SplitsView struct {
	Dev       []string `json:"dev"`
	Audit     int      `json:"audit"`
	Sealed    int      `json:"sealed"`
	Honeypots int      `json:"honeypots"`
	Hash      string   `json:"hash"`
}

// CurvePoint is the incumbent after one round (round 0: version 0) on each
// split, with the candidate's dev and audit scores and the round's event.
type CurvePoint struct {
	Round     int      `json:"round"`
	Dev       *float64 `json:"dev,omitempty"`
	Audit     *float64 `json:"audit,omitempty"`
	Sealed    *float64 `json:"sealed,omitempty"`
	CandDev   *float64 `json:"cand_dev,omitempty"`
	CandAudit *float64 `json:"cand_audit,omitempty"`
	Event     string   `json:"event"` // base | committed | rejected | tripped | pending
}

// ControlBand is what the sealed curve is read against.
type ControlBand struct {
	Low      float64  `json:"low"`
	High     float64  `json:"high"`
	Mean     float64  `json:"mean"`
	BestOfK  float64  `json:"best_of_k"`
	Trials   int      `json:"trials"`
	LoopUSD  float64  `json:"loop_usd"`
	NullMean *float64 `json:"null_mean,omitempty"`
	NullRef  string   `json:"null_ref,omitempty"`
}

// ScoreView is an arm's score without per-task detail.
type ScoreView struct {
	Mean   float64 `json:"mean"`
	Runs   int     `json:"runs"`
	Known  int     `json:"known"`
	Vetoed int     `json:"vetoed"`
}

// CompareView is a screen or audit comparison as served.
type CompareView struct {
	ExpID          string            `json:"exp_id"`
	A              ScoreView         `json:"a"`
	B              ScoreView         `json:"b"`
	MeanDiff       float64           `json:"mean_diff"`
	Pairs          int               `json:"pairs"`
	P              *float64          `json:"p,omitempty"`
	EValue         *stats.EValueTest `json:"e_value,omitempty"`
	Greedy         bool              `json:"greedy"`
	HoneypotPasses int               `json:"honeypot_passes"`
}

// RoundView is one round as served.
type RoundView struct {
	N          int            `json:"n"`
	Stage      string         `json:"stage"`
	Decision   string         `json:"decision"`
	Why        string         `json:"why"`
	Parent     string         `json:"parent"`
	Candidate  string         `json:"candidate,omitempty"`
	Improver   *ImproveResult `json:"improver,omitempty"`
	Diff       []FileDiff     `json:"diff"`
	Gates      []GateResult   `json:"gates"`
	Tripwires  []string       `json:"tripwires"`
	Approvals  []Approval     `json:"approvals"`
	Screen     *CompareView   `json:"screen,omitempty"`
	Audit      *CompareView   `json:"audit,omitempty"`
	Sealed     *ScoreView     `json:"sealed,omitempty"`
	Cost       RoundCost      `json:"cost"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

// LineageView is the lineage tree: one node per version with its last
// status, plus whether the chain verifies.
type LineageView struct {
	Nodes      []Node `json:"nodes"`
	Entries    int    `json:"entries"`
	ChainOK    bool   `json:"chain_ok"`
	ChainError string `json:"chain_error,omitempty"`
}

// IntegrityRow is one round on the integrity strip.
type IntegrityRow struct {
	Round          int      `json:"round"`
	Fired          []string `json:"fired"`
	Tripwires      []string `json:"tripwires"`
	Overridden     bool     `json:"overridden"`
	VetoesA        int      `json:"vetoes_incumbent"`
	VetoesB        int      `json:"vetoes_candidate"`
	HoneypotPasses int      `json:"honeypot_passes"`
}

// CostView is the spend by phase.
type CostView struct {
	Baseline float64     `json:"baseline"`
	Rounds   []RoundCost `json:"rounds"`
	Controls float64     `json:"controls"`
	Total    float64     `json:"total"`
	RoundCap float64     `json:"improver_cap_per_round"`
}

// Summarize is a record's list entry.
func Summarize(rec *Record, running bool) Summary {
	s := Summary{ID: rec.ID, Name: rec.Name, Agent: rec.Spec.Agent, Suite: rec.Spec.Suite, Status: rec.Status, Reason: rec.Reason,
		Running: running, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt, CreatedBy: rec.CreatedBy, Rounds: rec.Spec.Rounds,
		Incumbent: rec.Incumbent.Ref, TotalUSD: totalUSD(rec)}
	for _, rd := range rec.Rounds {
		if rd.Stage == StageDone {
			s.RoundsDone++
		}
		if rd.Decision == DecisionCommitted {
			s.Commits++
		}
	}
	if rec.Verdict != nil {
		s.Verdict = rec.Verdict.State
		g := rec.Verdict.SealedGain
		s.SealedGain = &g
	}
	return s
}

// View builds the report of a record; lineage is read from dir.
func (r *Runner) View(rec *Record) *Detail {
	d := &Detail{Summary: Summarize(rec, r.Running(rec.ID)), SpecHash: rec.SpecHash, Spec: rec.Spec, Base: rec.Base,
		Splits: SplitsView{Dev: rec.Splits.Dev, Audit: len(rec.Splits.Audit), Sealed: len(rec.Splits.Sealed), Honeypots: len(rec.Splits.Honeypots), Hash: rec.Splits.Hash},
		Curve:  []CurvePoint{}, Rounds: []RoundView{}, Integrity: []IntegrityRow{}, Verdict: rec.Verdict, Error: rec.Error,
		AuditLooks: rec.AuditLooks, AuditLooksMax: rec.Spec.Commit.AuditLooks}
	d.Base.Doc = nil
	d.Spec.Splits.Honeypots, d.Spec.Tasks = nil, nil // the pool and seed would give away the sealed tasks
	if b := rec.Baseline; b != nil {
		d.Curve = append(d.Curve, CurvePoint{Round: 0, Dev: ptr(b.Dev.Mean), Sealed: ptr(b.Sealed.Mean), Event: NodeBase})
		d.Cost.Baseline = b.CostUSD
	}
	for _, rd := range rec.Rounds {
		d.Rounds = append(d.Rounds, roundView(rd))
		d.Curve = append(d.Curve, curvePoint(rd))
		d.Integrity = append(d.Integrity, integrityRow(rd))
		d.Cost.Rounds = append(d.Cost.Rounds, rd.Cost)
	}
	if c := rec.Controls; c != nil {
		d.Cost.Controls = c.CostUSD
		if bm := c.BudgetMatched; bm != nil {
			d.Band = &ControlBand{Low: bm.BandLow, High: bm.BandHigh, Mean: bm.Score.Mean, BestOfK: bm.BestOfK, Trials: bm.Trials, LoopUSD: bm.LoopUSD}
		}
		if nc := c.Null; nc != nil {
			if d.Band == nil {
				d.Band = &ControlBand{}
			}
			d.Band.NullMean, d.Band.NullRef = ptr(nc.Score.Mean), nc.Final.Ref
		}
	}
	d.Cost.Total, d.Cost.RoundCap = totalUSD(rec), rec.Spec.Improver.BudgetUSD
	if d.Cost.Rounds == nil {
		d.Cost.Rounds = []RoundCost{}
	}
	d.Lineage = r.lineageView(rec.ID)
	return d
}

// Lineage returns the lineage view of an improvement.
func (r *Runner) Lineage(id string) (LineageView, error) {
	if _, err := r.load(id); err != nil {
		return LineageView{}, err
	}
	return r.lineageView(id), nil
}

func (r *Runner) lineageView(id string) LineageView {
	nodes, err := ReadLineage(r.dir(id))
	lv := LineageView{Nodes: []Node{}, Entries: len(nodes), ChainOK: err == nil}
	if err != nil {
		lv.ChainError = err.Error()
	}
	idx := map[string]int{}
	for _, n := range nodes {
		if i, ok := idx[n.Ref]; ok {
			// A later entry of a version carries its decision; keep where
			// it was first frozen.
			first := lv.Nodes[i]
			n.Seq, n.At = first.Seq, first.At
			if n.ProducedBy == "" {
				n.ProducedBy = first.ProducedBy
			}
			lv.Nodes[i] = n
			continue
		}
		idx[n.Ref] = len(lv.Nodes)
		lv.Nodes = append(lv.Nodes, n)
	}
	return lv
}

func roundView(rd *Round) RoundView {
	v := RoundView{N: rd.N, Stage: rd.Stage, Decision: rd.Decision, Why: rd.Why, Parent: rd.Parent.Ref, Improver: rd.Improver,
		Diff: rd.Diff, Gates: rd.Gates, Tripwires: rd.Tripwires, Approvals: rd.Approvals, Cost: rd.Cost, StartedAt: rd.StartedAt, FinishedAt: rd.FinishedAt}
	if rd.Candidate != nil {
		v.Candidate = rd.Candidate.Ref
	}
	v.Screen, v.Audit = compareView(rd.Screen), compareView(rd.Audit)
	if rd.Sealed != nil {
		s := scoreView(*rd.Sealed)
		v.Sealed = &s
	}
	return v
}

func compareView(c *Comparison) *CompareView {
	if c == nil {
		return nil
	}
	return &CompareView{ExpID: c.ExpID, A: scoreView(c.A), B: scoreView(c.B), MeanDiff: c.MeanDiff, Pairs: c.Pairs, P: c.P,
		EValue: c.EValue, Greedy: c.Greedy, HoneypotPasses: c.HoneypotPasses}
}

func scoreView(s ArmScore) ScoreView {
	return ScoreView{Mean: s.Mean, Runs: s.Runs, Known: s.Known, Vetoed: s.Vetoed}
}

func curvePoint(rd *Round) CurvePoint {
	p := CurvePoint{Round: rd.N, Event: NodePending}
	switch {
	case rd.Decision == DecisionCommitted:
		p.Event = NodeCommitted
	case rd.Decision == DecisionRejected && len(rd.Tripwires) > 0 && len(rd.Approvals) == 0:
		p.Event = NodeTripped
	case rd.Decision == DecisionRejected:
		p.Event = NodeRejected
	case len(rd.Tripwires) > 0:
		p.Event = NodeTripped
	}
	committed := rd.Decision == DecisionCommitted
	if s := rd.Screen; s != nil {
		p.CandDev = ptr(s.B.Mean)
		if committed {
			p.Dev = ptr(s.B.Mean)
		} else {
			p.Dev = ptr(s.A.Mean)
		}
	}
	if a := rd.Audit; a != nil {
		p.CandAudit = ptr(a.B.Mean)
		if committed {
			p.Audit = ptr(a.B.Mean)
		} else {
			p.Audit = ptr(a.A.Mean)
		}
	}
	if rd.Sealed != nil {
		p.Sealed = ptr(rd.Sealed.Mean)
	}
	return p
}

func integrityRow(rd *Round) IntegrityRow {
	row := IntegrityRow{Round: rd.N, Fired: []string{}, Tripwires: rd.Tripwires, Overridden: len(rd.Approvals) > 0}
	for _, g := range rd.Gates {
		if g.Fired {
			row.Fired = append(row.Fired, g.Name)
		}
	}
	if a := rd.Audit; a != nil {
		row.VetoesA, row.VetoesB, row.HoneypotPasses = a.A.Vetoed, a.B.Vetoed, a.HoneypotPasses
	}
	return row
}

func ptr(f float64) *float64 { return &f }
