package livescore

import (
	"fmt"
	"math"
	"time"
)

// CompareState is a running comparison, kept in state.json. It consumes
// scored sessions once, in order of scoring, so retention dropping old
// sessions never changes it, and looking at it never re-tests old data.
type CompareState struct {
	Baseline     string                  `json:"baseline"`
	Candidate    string                  `json:"candidate"`
	Alpha        float64                 `json:"alpha"`
	MinPairs     int                     `json:"min_pairs"`
	Signals      []string                `json:"signals"`
	SettingsHash string                  `json:"settings_hash"`
	StartedAt    time.Time               `json:"started_at"`
	StartSeq     int64                   `json:"start_seq"` // sessions scored at or before this are not consumed
	LastSeq      int64                   `json:"last_seq"`
	Restarts     int                     `json:"restarts,omitempty"`
	SessionsA    int                     `json:"sessions_a"`
	SessionsB    int                     `json:"sessions_b"`
	Tests        map[string]*SignalState `json:"tests"`
	DecidedAt    *time.Time              `json:"decided_at,omitempty"`
	Result       string                  `json:"result"`
}

// SignalState is one signal's queues, process and descriptive sums.
type SignalState struct {
	QueueA  []float64  `json:"queue_a,omitempty"` // baseline values waiting for a partner
	QueueB  []float64  `json:"queue_b,omitempty"`
	Dropped int        `json:"dropped,omitempty"` // values dropped from a full queue (oldest first)
	E       *EProcess  `json:"e"`
	SumA    float64    `json:"sum_a"`
	SumB    float64    `json:"sum_b"`
	NA      int        `json:"n_a"`
	NB      int        `json:"n_b"`
	Worse   *time.Time `json:"worse_at,omitempty"`
	Better  *time.Time `json:"better_at,omitempty"`
}

// newCompare starts a comparison from the config's settings.
func newCompare(c *Config, startSeq int64, now time.Time, restarts int) *CompareState {
	cc := c.compareSettings()
	st := &CompareState{Baseline: cc.Baseline, Candidate: cc.Candidate, Alpha: cc.Alpha, MinPairs: cc.MinPairs,
		Signals: cc.Signals, SettingsHash: c.settingsHash(), StartedAt: now, StartSeq: startSeq, LastSeq: startSeq,
		Restarts: restarts, Tests: map[string]*SignalState{}, Result: ResultUnknown}
	for _, s := range cc.Signals {
		st.Tests[s] = &SignalState{E: NewEProcess()}
	}
	return st
}

// logThreshold is log(1/a) for each one-sided process.
func (st *CompareState) logThreshold() float64 {
	return -math.Log(perSignalLevel(st.Alpha, len(st.Signals)))
}

// consume feeds sessions (in increasing Seq) into the comparison. Sessions
// of other versions, and sessions at or before LastSeq, are skipped. It
// returns the signals that decided in this call.
func (st *CompareState) consume(recs []SessionRecord, now time.Time) []string {
	var decided []string
	lt := st.logThreshold()
	for i := range recs {
		r := &recs[i]
		if r.Seq <= st.LastSeq {
			continue
		}
		st.LastSeq = r.Seq
		arm := 0
		switch r.Version {
		case st.Baseline:
			arm = 1
			st.SessionsA++
		case st.Candidate:
			arm = 2
			st.SessionsB++
		default:
			continue
		}
		for _, name := range st.Signals {
			def, ok := signalByName(name)
			t := st.Tests[name]
			if !ok || t == nil {
				continue
			}
			v, known := def.value(&r.Signals)
			if !known || (name == "judge_score" && !r.Signals.JudgeCalibrated) {
				continue // an uncalibrated judge score is reported, never tested
			}
			if arm == 1 {
				t.SumA += v
				t.NA++
				if len(t.QueueB) > 0 {
					b := t.QueueB[0]
					t.QueueB = t.QueueB[1:]
					decided = append(decided, st.addPair(name, def, t, v, b, lt, now)...)
				} else {
					t.QueueA = pushCapped(t.QueueA, v, &t.Dropped)
				}
			} else {
				t.SumB += v
				t.NB++
				if len(t.QueueA) > 0 {
					a := t.QueueA[0]
					t.QueueA = t.QueueA[1:]
					decided = append(decided, st.addPair(name, def, t, a, v, lt, now)...)
				} else {
					t.QueueB = pushCapped(t.QueueB, v, &t.Dropped)
				}
			}
		}
	}
	st.Result = st.result()
	if st.Result != ResultUnknown && st.DecidedAt == nil {
		t := now
		st.DecidedAt = &t
	}
	return decided
}

// addPair adds one pair and reports a new decision.
func (st *CompareState) addPair(name string, def SignalDef, t *SignalState, a, b, lt float64, now time.Time) []string {
	before := t.E.Crossed
	t.E.Add(pairDiff(def.Kind, a, b), lt, st.MinPairs)
	var out []string
	// "up" is candidate higher. For a lower-is-better signal that is worse.
	worseDir, betterDir := dirUp, dirDown
	if !def.LowerBetter {
		worseDir, betterDir = dirDown, dirUp
	}
	if before[worseDir] == 0 && t.E.Crossed[worseDir] != 0 && t.Worse == nil {
		tm := now
		t.Worse = &tm
		out = append(out, name+": "+ResultWorse)
	}
	if before[betterDir] == 0 && t.E.Crossed[betterDir] != 0 && t.Better == nil {
		tm := now
		t.Better = &tm
		out = append(out, name+": "+ResultBetter)
	}
	return out
}

func pushCapped(q []float64, v float64, dropped *int) []float64 {
	q = append(q, v)
	if len(q) > MaxQueue {
		*dropped += len(q) - MaxQueue
		q = append([]float64(nil), q[len(q)-MaxQueue:]...)
	}
	return q
}

// result is worse when any signal decided worse, better when at least one
// decided better and none worse, else not yet known. A signal that crossed
// both ways (possible only by chance, within the bound) counts as worse.
func (st *CompareState) result() string {
	better := false
	for _, name := range st.Signals {
		t := st.Tests[name]
		if t == nil {
			continue
		}
		if t.Worse != nil {
			return ResultWorse
		}
		if t.Better != nil {
			better = true
		}
	}
	if better {
		return ResultBetter
	}
	return ResultUnknown
}

// Comparison is the comparison as status and the API show it.
type Comparison struct {
	Evidence     string         `json:"evidence"` // live
	Baseline     string         `json:"baseline"`
	Candidate    string         `json:"candidate"`
	Result       string         `json:"result"` // better | worse | not_yet_known
	Alpha        float64        `json:"alpha"`
	PerTestLevel float64        `json:"per_test_level"` // alpha / (2 x signals)
	Threshold    float64        `json:"threshold"`      // e-value a one-sided process must reach
	MinPairs     int            `json:"min_pairs"`
	StartedAt    time.Time      `json:"started_at"`
	DecidedAt    *time.Time     `json:"decided_at,omitempty"`
	SessionsA    int            `json:"sessions_baseline"`
	SessionsB    int            `json:"sessions_candidate"`
	Restarts     int            `json:"restarts"`
	Signals      []SignalResult `json:"signals"`
	Method       string         `json:"method"`
	Stale        string         `json:"stale,omitempty"` // set when the config changed since the comparison started
}

// SignalResult is one signal of a comparison.
type SignalResult struct {
	Name        string     `json:"name"`
	Label       string     `json:"label"`
	Kind        string     `json:"kind"`
	LowerBetter bool       `json:"lower_is_better"`
	Unit        string     `json:"unit,omitempty"`
	Pairs       int        `json:"pairs"`
	NeedPairs   int        `json:"need_pairs"` // pairs still missing before it may decide; 0 once it may
	SessionsA   int        `json:"sessions_baseline"`
	SessionsB   int        `json:"sessions_candidate"`
	MeanA       *float64   `json:"mean_baseline,omitempty"`
	MeanB       *float64   `json:"mean_candidate,omitempty"`
	EWorse      float64    `json:"e_worse"`  // current e-value for "candidate is worse"
	EBetter     float64    `json:"e_better"` // current e-value for "candidate is better"
	Decision    string     `json:"decision"` // better | worse | not_yet_known
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	Dropped     int        `json:"dropped,omitempty"`
}

// MethodText describes the test in one paragraph.
const MethodText = "Always-valid sequential test per signal: sessions of the two versions are paired in order of scoring; " +
	"a rate signal bets on the difference of the pair, an ordinal one on which is larger; two one-sided betting e-processes per signal " +
	"(a mixture over a fixed grid of bets) are each tested at alpha / (2 x signals), so by Ville's inequality the chance that any signal " +
	"ever decides when the versions do not differ is at most alpha, over every look. A signal decides only after the minimum pairs. " +
	"Live evidence: no ground truth and no control of inputs, never part of an experiment verdict."

// View renders the comparison.
func (st *CompareState) View(cfg *Config) *Comparison {
	lt := st.logThreshold()
	out := &Comparison{Evidence: EvidenceClass, Baseline: st.Baseline, Candidate: st.Candidate, Result: st.Result, Alpha: st.Alpha,
		PerTestLevel: perSignalLevel(st.Alpha, len(st.Signals)), Threshold: math.Exp(lt), MinPairs: st.MinPairs, StartedAt: st.StartedAt,
		DecidedAt: st.DecidedAt, SessionsA: st.SessionsA, SessionsB: st.SessionsB, Restarts: st.Restarts, Signals: []SignalResult{}, Method: MethodText}
	if cfg != nil && cfg.Compare != nil && cfg.settingsHash() != st.SettingsHash {
		out.Stale = "the comparison settings or the versions' selectors changed since it started; run `livescore compare -restart` to test the new settings from now on"
	}
	for _, name := range st.Signals {
		def, ok := signalByName(name)
		t := st.Tests[name]
		if !ok || t == nil {
			continue
		}
		worseDir, betterDir := dirUp, dirDown
		if !def.LowerBetter {
			worseDir, betterDir = dirDown, dirUp
		}
		sr := SignalResult{Name: name, Label: def.Label, Kind: def.Kind, LowerBetter: def.LowerBetter, Unit: def.Unit, Pairs: t.E.Pairs,
			SessionsA: t.NA, SessionsB: t.NB, EWorse: t.E.E(worseDir), EBetter: t.E.E(betterDir), Decision: ResultUnknown, Dropped: t.Dropped}
		if t.E.Pairs < st.MinPairs {
			sr.NeedPairs = st.MinPairs - t.E.Pairs
		}
		if t.NA > 0 {
			m := t.SumA / float64(t.NA)
			sr.MeanA = &m
		}
		if t.NB > 0 {
			m := t.SumB / float64(t.NB)
			sr.MeanB = &m
		}
		switch {
		case t.Worse != nil:
			sr.Decision, sr.DecidedAt = ResultWorse, t.Worse
		case t.Better != nil:
			sr.Decision, sr.DecidedAt = ResultBetter, t.Better
		}
		out.Signals = append(out.Signals, sr)
	}
	return out
}

// summaryLine is the comparison in one line.
func (c *Comparison) summaryLine() string {
	line := fmt.Sprintf("%s vs %s: %s (%d and %d sessions", c.Candidate, c.Baseline, c.Result, c.SessionsB, c.SessionsA)
	for _, s := range c.Signals {
		if s.Decision != ResultUnknown {
			line += fmt.Sprintf("; %s %s at %d pairs", s.Name, s.Decision, s.Pairs)
		}
	}
	return line + ")"
}
