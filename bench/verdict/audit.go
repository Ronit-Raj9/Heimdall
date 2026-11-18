package verdict

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Endpoint labels an audit verdict carries: the result was published, and
// each task scored a pass count.
const (
	AuditCondition = "published"
	AuditMetric    = "pass"
)

// auditColumns is the CSV header ParseAuditCSV requires, in any order.
var auditColumns = []string{"task", "a_pass", "a_n", "b_pass", "b_n"}

// AuditRow is one task of a published two-arm comparison: arm A passed APass
// of AN attempts and arm B passed BPass of BN.
type AuditRow struct {
	Task  string
	APass int
	AN    int
	BPass int
	BN    int
}

// Audit applies the paired sign-flip rule to a published result given only
// per-task pass counts. It answers whether the published comparison could
// have supported a directional claim at all. Published counts carry no
// unknowns, so both missingness bounds equal the point estimate, there are no
// guards, and the MDE uses the assumed SD (no A/A run exists). Integrity is
// left empty: a published table says nothing about veto gates. Unequal
// attempts within a task make the verdict invalid, as in the policy.
func Audit(rows []AuditRow, alpha float64) (*schemas.Verdict, error) {
	if len(rows) == 0 {
		return nil, errors.New("audit: no rows")
	}
	if alpha <= 0 || alpha >= 1 {
		alpha = defaultAlpha
	}
	a, b := map[string]cell{}, map[string]cell{}
	var invalid []string
	for i, r := range rows {
		if err := validateAuditRow(r); err != nil {
			return nil, fmt.Errorf("audit row %d: %w", i+1, err)
		}
		if _, dup := a[r.Task]; dup {
			return nil, fmt.Errorf("audit row %d: duplicate task %s", i+1, r.Task)
		}
		a[r.Task] = cell{n: r.AN, nObs: r.AN, obsSum: float64(r.APass)}
		b[r.Task] = cell{n: r.BN, nObs: r.BN, obsSum: float64(r.BPass)}
		if r.AN != r.BN {
			invalid = append(invalid, fmt.Sprintf("unequal trials on task %s: arm %s %d, arm %s %d", r.Task, ArmBaseline, r.AN, ArmCandidate, r.BN))
		}
	}
	eff := pairedEffect(AuditCondition, AuditMetric, ArmBaseline, ArmCandidate, a, b, true)
	decision := decide(invalid, eff, nil, alpha)
	// A published table carries its own per-task differences, so the MDE uses
	// their SD when there is one; the assumed SD only stands in when the
	// differences are all equal or there is a single task.
	sd, assumed := defaultAssumedSD, true
	if ds := taskDiffs(eff); len(ds) >= 2 {
		if v := stats.SD(ds); v > 0 {
			sd, assumed = v, false
		}
	}
	needed := tasksNeeded(eff.DBar, sd, alpha)
	over := 0
	if needed == 0 && eff.DBar != 0 {
		over = auditMaxTasks
	}
	mde := mdePP(eff.NTasks, sd, alpha)
	return &schemas.Verdict{
		Primary:           schemas.Endpoint{Condition: AuditCondition, Metric: AuditMetric},
		Decision:          decision,
		Action:            actionFor(decision, nil, "", nil),
		Effect:            eff,
		Alpha:             alpha,
		MDE:               mde,
		MDEAssumedSD:      assumed,
		SD:                sd,
		Variance:          auditVariance(sd, assumed, eff.NTasks, mde),
		TasksNeeded:       needed,
		TasksNeededOver:   over,
		RandomizationUnit: randomizationUnit,
		TieRule:           tieRule,
		Guards:            []schemas.GuardResult{},
		InvalidReasons:    invalid,
		Integrity:         schemas.IntegrityUnknown, // published counts carry no gate data
	}, nil
}

// auditMaxTasks is the largest suite TasksNeeded searches; beyond it the
// verdict reports "more than auditMaxTasks".
const auditMaxTasks = 200

// auditVariance is an audit's variance card. A published table has no A/A
// arm, so its SD is the SD of its own per-task B-minus-A differences
// (VarianceObservedEffect) or, when those cannot give one, the assumed SD.
// The first includes whatever real effect the table holds, so it is an upper
// estimate of the noise, not a measured noise floor.
func auditVariance(sd float64, assumed bool, n int, mde float64) *schemas.VarianceCard {
	c := &schemas.VarianceCard{Source: VarianceObservedEffect, SD: sd, AssumedSD: defaultAssumedSD, MDE: mde,
		Detail: fmt.Sprintf("SD %.3f of the table's own per-task differences over %d tasks; a published table has no A/A arm, and this SD includes any real effect, so it is not a measured noise floor", sd, n)}
	if assumed {
		c.Source = schemas.VarianceAssumed
		c.Detail = fmt.Sprintf("SD %.2f assumed: a published table has no A/A arm, and its per-task differences over %d task(s) give no SD above 0", sd, n)
	}
	return c
}

// taskDiffs returns the per-task differences of an effect.
func taskDiffs(e *schemas.Effect) []float64 {
	out := make([]float64, 0, len(e.PerTask))
	for _, t := range e.PerTask {
		out = append(out, t.D)
	}
	return out
}

// ParseAuditCSV reads audit rows from a CSV whose header names the columns
// task, a_pass, a_n, b_pass and b_n, in any order. It is pure: it reads r and
// nothing else, and leaves range checks to Audit.
func ParseAuditCSV(r io.Reader) ([]AuditRow, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read audit csv header: %w", err)
	}
	col := map[string]int{}
	for i, name := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "\ufeff")))] = i
	}
	for _, name := range auditColumns {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("audit csv header: missing column %s", name)
		}
	}
	var rows []AuditRow
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read audit csv line %d: %w", line, err)
		}
		row, err := parseAuditRecord(rec, col)
		if err != nil {
			return nil, fmt.Errorf("audit csv line %d: %w", line, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// parseAuditRecord turns one CSV record into a row.
func parseAuditRecord(rec []string, col map[string]int) (AuditRow, error) {
	row := AuditRow{Task: strings.TrimSpace(rec[col["task"]])}
	fields := []struct {
		name string
		dst  *int
	}{{"a_pass", &row.APass}, {"a_n", &row.AN}, {"b_pass", &row.BPass}, {"b_n", &row.BN}}
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(rec[col[f.name]]))
		if err != nil {
			return AuditRow{}, fmt.Errorf("column %s: %w", f.name, err)
		}
		*f.dst = n
	}
	return row, nil
}

// validateAuditRow checks that a row names a task and that each arm's passes
// lie within its attempts.
func validateAuditRow(r AuditRow) error {
	switch {
	case r.Task == "":
		return errors.New("empty task id")
	case r.AN <= 0 || r.BN <= 0:
		return fmt.Errorf("task %s: attempts must be positive (a_n %d, b_n %d)", r.Task, r.AN, r.BN)
	case r.APass < 0 || r.APass > r.AN:
		return fmt.Errorf("task %s: a_pass %d outside 0..%d", r.Task, r.APass, r.AN)
	case r.BPass < 0 || r.BPass > r.BN:
		return fmt.Errorf("task %s: b_pass %d outside 0..%d", r.Task, r.BPass, r.BN)
	}
	return nil
}
