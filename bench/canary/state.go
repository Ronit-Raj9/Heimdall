package canary

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// States of a canary.
const (
	StateNoBaseline    = "no_baseline"    // nothing pinned yet: run `canary baseline`
	StateSteady        = "steady"         // the last look found no drift
	StateWatching      = "watching"       // one look flagged; a confirmation look decides
	StateDrifted       = "drifted"        // two consecutive looks flagged the same target and metric
	StateInvalid       = "invalid"        // the last look could not be compared: a harness problem
	StateBaselineStale = "baseline_stale" // the frozen config no longer matches the baseline, or the baseline is too old
)

// States lists every state.
var States = []string{StateNoBaseline, StateSteady, StateWatching, StateDrifted, StateInvalid, StateBaselineStale}

// Record kinds.
const (
	KindBaseline     = "baseline"
	KindRun          = "run"
	KindConfirmation = "confirmation"
)

// Look outcomes.
const (
	OutcomePinned  = "pinned"  // a baseline was pinned
	OutcomeClear   = "clear"   // compared; nothing flagged
	OutcomeFlagged = "flagged" // compared; at least one test worse
	OutcomeInvalid = "invalid" // not comparable (harness problem)
	OutcomeStale   = "stale"   // refused before running: the frozen config changed since the baseline
	OutcomeRefused = "refused" // refused before running: caps or forecast
	OutcomeError   = "error"   // planning or running failed
)

// Record is one canary look (or baseline), appended to records.jsonl.
type Record struct {
	ID           string        `json:"id"` // the experiment id, or r_<time> when nothing was frozen
	Canary       string        `json:"canary"`
	Kind         string        `json:"kind"`
	Started      time.Time     `json:"started"`
	Finished     time.Time     `json:"finished"`
	ExpID        string        `json:"exp_id,omitempty"`
	ManifestHash string        `json:"manifest_hash,omitempty"`
	Fingerprint  string        `json:"fingerprint,omitempty"`
	Baseline     string        `json:"baseline,omitempty"` // baseline experiment compared against
	Injected     string        `json:"injected,omitempty"` // test hook: probe forced on a smoke canary
	Smoke        bool          `json:"smoke,omitempty"`
	Outcome      string        `json:"outcome"`
	State        string        `json:"state"` // the canary's state after this record
	Reason       string        `json:"reason,omitempty"`
	CostUSD      float64       `json:"cost_usd"`
	ForecastUSD  *float64      `json:"forecast_usd,omitempty"`
	Runs         int           `json:"runs"`
	RunErrors    int           `json:"run_errors"`
	Comparison   *Comparison   `json:"comparison,omitempty"`
	Changes      *Changes      `json:"changes,omitempty"`
	Equivalence  []Equivalence `json:"equivalence,omitempty"`
	Served       []Served      `json:"served,omitempty"`
	Invalid      []string      `json:"invalid,omitempty"`
	Alerts       []string      `json:"alerts,omitempty"` // events alerted
	Points       []ArmPoint    `json:"points,omitempty"` // per arm success, for the history chart
}

// ArmPoint is one arm's headline numbers in a record, for the history chart.
type ArmPoint struct {
	Arm     string   `json:"arm"`
	Success *float64 `json:"success"` // mean over tasks of the per-task success rate
	Tasks   int      `json:"tasks"`
	Runs    int      `json:"runs"`
}

// Baseline is one pinned known-good experiment, appended to baselines.jsonl;
// the last line is the current baseline and older lines are the history.
type Baseline struct {
	ExpID        string    `json:"exp_id"`
	ManifestHash string    `json:"manifest_hash"`
	Fingerprint  string    `json:"fingerprint"`
	PinnedAt     time.Time `json:"pinned_at"`
	CreatedAt    time.Time `json:"created_at"` // the experiment's creation time
	Served       []Served  `json:"served"`
	Clusters     []string  `json:"clusters"` // failure cluster signatures of the baseline
	Smoke        bool      `json:"smoke,omitempty"`
	Note         string    `json:"note,omitempty"`
}

// stateDir is one canary's folder under the state root.
func stateDir(root, name string) string { return filepath.Join(root, name) }

const (
	recordsFile   = "records.jsonl"
	baselinesFile = "baselines.jsonl"
	lockFile      = "run.lock"
)

// ReadRecords returns a canary's records, oldest first. A missing file is
// no records; a torn last line from a crash is skipped.
func ReadRecords(root, name string) ([]Record, error) {
	var out []Record
	err := readLines(filepath.Join(stateDir(root, name), recordsFile), func(b []byte) {
		var r Record
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	})
	return out, err
}

// ReadBaselines returns a canary's baselines, oldest first.
func ReadBaselines(root, name string) ([]Baseline, error) {
	var out []Baseline
	err := readLines(filepath.Join(stateDir(root, name), baselinesFile), func(b []byte) {
		var r Baseline
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	})
	return out, err
}

// CurrentBaseline returns the last pinned baseline, or nil.
func CurrentBaseline(root, name string) (*Baseline, error) {
	bs, err := ReadBaselines(root, name)
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	b := bs[len(bs)-1]
	return &b, nil
}

func appendRecord(root string, r Record) error {
	return appendLine(filepath.Join(stateDir(root, r.Canary), recordsFile), r)
}

func appendBaseline(root, name string, b Baseline) error {
	return appendLine(filepath.Join(stateDir(root, name), baselinesFile), b)
}

// readLines calls f with each line of a JSON-lines file.
func readLines(p string, f func([]byte)) error {
	fh, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			f(sc.Bytes())
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	return nil
}

// appendLine appends v as one JSON line.
func appendLine(p string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// SpentOn sums the cost of a canary's records that started on day (in loc).
func SpentOn(records []Record, day string, loc *time.Location) float64 {
	total := 0.0
	for _, r := range records {
		if r.Started.In(loc).Format(time.DateOnly) == day {
			total += r.CostUSD
		}
	}
	return total
}

// lastLook returns the newest record since the current baseline whose look
// was compared (clear or flagged), or nil. Invalid, refused and stale looks
// carry no information about drift, so they never break a pair of
// consecutive flags.
func lastLook(records []Record, baseline *Baseline) *Record {
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.Kind == KindBaseline && r.Outcome != OutcomePinned {
			continue // a baseline attempt that pinned nothing changes nothing
		}
		if r.Kind == KindBaseline {
			return nil
		}
		if baseline != nil && r.Baseline != baseline.ExpID {
			return nil
		}
		if r.Outcome == OutcomeClear || r.Outcome == OutcomeFlagged {
			return &records[i]
		}
	}
	return nil
}

// NextState is the confirmation rule. A clear look is steady. A flagged
// look after a flagged look (the previous compared look since the same
// baseline) that shares at least one flagged key is drifted; any other
// flagged look is watching, and asks for a confirmation look. prevState is
// the state of that previous compared look ("" when there is none).
func NextState(prevState string, prevFlagged []string, outcome string, flagged []string) string {
	switch outcome {
	case OutcomeClear:
		return StateSteady
	case OutcomeFlagged:
		if (prevState == StateWatching || prevState == StateDrifted) && len(Overlap(flagged, prevFlagged)) > 0 {
			return StateDrifted
		}
		return StateWatching
	case OutcomeInvalid:
		return StateInvalid
	case OutcomeStale:
		return StateBaselineStale
	}
	return prevState
}
