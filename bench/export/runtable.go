package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RunRow is one sealed run in the flat run table: ids, outcome, cost and
// every metric the run recorded. A metric the run did not observe is null
// (JSONL) or empty (CSV), never zero.
type RunRow struct {
	ExpID        string              `json:"exp_id"`
	RunID        string              `json:"run_id"`
	SlotID       string              `json:"slot_id"`
	PairID       string              `json:"pair_id"`
	Suite        string              `json:"suite"`
	Task         string              `json:"task"`
	TaskVersion  string              `json:"task_version"`
	Arm          string              `json:"arm"`
	ArmLabel     string              `json:"arm_label"`
	AgentConfig  string              `json:"agent_config"`
	Model        string              `json:"model"`
	Provider     string              `json:"provider"`
	Condition    string              `json:"condition"`
	Trial        int                 `json:"trial"`
	Attempt      int                 `json:"attempt"`
	Outcome      string              `json:"outcome"`
	Success      *float64            `json:"verified_success"` // 1 or 0 when the outcome scores; null when it does not
	Passed       *bool               `json:"grade_passed"`     // the grade, when the run has one
	GradeSource  string              `json:"grade_source"`
	CostUSD      *float64            `json:"cost_usd"`
	Synthetic    bool                `json:"synthetic"`
	Started      string              `json:"started,omitempty"`
	Sealed       string              `json:"sealed,omitempty"`
	ManifestHash string              `json:"manifest_hash"`
	SealHash     string              `json:"seal_hash"`
	Problems     []string            `json:"problems,omitempty"` // evidence problems found while loading (seal, duplicate counted run)
	Metrics      map[string]*float64 `json:"metrics"`
}

// RunTable turns an experiment's sealed runs into rows in slot order.
func RunTable(in EEEInput) []RunRow {
	m := in.Manifest
	rows := make([]RunRow, 0, len(in.Runs))
	for i := range in.Runs {
		sr := &in.Runs[i]
		r := RunRow{
			ExpID: sr.Slot.ExpID, RunID: sr.Run.RunID, SlotID: sr.Slot.SlotID, PairID: sr.Slot.PairID,
			Suite: sr.Slot.Suite, Task: sr.Slot.Task, TaskVersion: sr.Slot.TaskVersion, Arm: sr.Slot.Arm,
			Condition: sr.Slot.Condition, Trial: sr.Slot.Trial, Attempt: sr.Run.Attempt, Outcome: string(sr.Run.Outcome),
			GradeSource: sr.GradeSource, Synthetic: in.Synthetic[sr.Slot.Arm], ManifestHash: sr.Run.ManifestHash,
			SealHash: sr.SealHash, Problems: sr.Problems, Metrics: map[string]*float64{}, AgentConfig: sr.Run.Agent.ConfigID,
		}
		if m != nil {
			if a, ok := m.Arms[sr.Slot.Arm]; ok {
				r.ArmLabel, r.Model, r.Provider = a.Label, a.Config.Model, a.Config.Gateway.Provider
				r.AgentConfig = firstNonEmpty(a.Config.ID, r.AgentConfig)
			}
		}
		if y, ok := sr.Run.Outcome.Score(); ok {
			r.Success = &y
		}
		if sr.Grade != nil {
			p := sr.Grade.Passed
			r.Passed = &p
		}
		if !sr.Run.Timing.Started.IsZero() {
			r.Started = sr.Run.Timing.Started.UTC().Format("2006-01-02T15:04:05Z")
		}
		if sr.Run.Timing.Sealed != nil {
			r.Sealed = sr.Run.Timing.Sealed.UTC().Format("2006-01-02T15:04:05Z")
		}
		for id, v := range sr.Metrics {
			if v.State == schemas.ValueObserved && v.V != nil {
				x := *v.V
				r.Metrics[id] = &x
			} else {
				r.Metrics[id] = nil
			}
		}
		if c, ok := r.Metrics["cost_usd"]; ok {
			r.CostUSD = c
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].SlotID != rows[j].SlotID {
			return rows[i].SlotID < rows[j].SlotID
		}
		return rows[i].RunID < rows[j].RunID
	})
	return rows
}

// WriteJSONL writes one JSON object per row.
func WriteJSONL(w io.Writer, rows []RunRow) error {
	enc := json.NewEncoder(w)
	for i := range rows {
		if err := enc.Encode(&rows[i]); err != nil {
			return err
		}
	}
	return nil
}

// csvFixed is the fixed column list; metric columns follow as metric.<id>.
var csvFixed = []string{"exp_id", "run_id", "slot_id", "pair_id", "suite", "task", "task_version", "arm", "arm_label",
	"agent_config", "model", "provider", "condition", "trial", "attempt", "outcome", "verified_success", "grade_passed",
	"grade_source", "cost_usd", "synthetic", "started", "sealed", "manifest_hash", "seal_hash", "problems"}

// WriteCSV writes a header and one line per row. Metric columns are the
// union of the metric ids of every row, sorted.
func WriteCSV(w io.Writer, rows []RunRow) error {
	ids := map[string]bool{}
	for _, r := range rows {
		for id := range r.Metrics {
			ids[id] = true
		}
	}
	metricIDs := make([]string, 0, len(ids))
	for id := range ids {
		metricIDs = append(metricIDs, id)
	}
	sort.Strings(metricIDs)
	cw := csv.NewWriter(w)
	header := append([]string{}, csvFixed...)
	for _, id := range metricIDs {
		header = append(header, "metric."+id)
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	num := func(p *float64) string {
		if p == nil {
			return ""
		}
		return strconv.FormatFloat(*p, 'g', -1, 64)
	}
	for _, r := range rows {
		passed := ""
		if r.Passed != nil {
			passed = strconv.FormatBool(*r.Passed)
		}
		rec := []string{r.ExpID, r.RunID, r.SlotID, r.PairID, r.Suite, r.Task, r.TaskVersion, r.Arm, csvSafe(r.ArmLabel),
			r.AgentConfig, r.Model, r.Provider, r.Condition, strconv.Itoa(r.Trial), strconv.Itoa(r.Attempt), r.Outcome,
			num(r.Success), passed, r.GradeSource, num(r.CostUSD), strconv.FormatBool(r.Synthetic), r.Started, r.Sealed,
			r.ManifestHash, r.SealHash, csvSafe(strings.Join(r.Problems, "; "))}
		for _, id := range metricIDs {
			rec = append(rec, num(r.Metrics[id]))
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("write csv: %w", err)
	}
	return nil
}

// csvSafe defuses a free-text cell a spreadsheet would run as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
