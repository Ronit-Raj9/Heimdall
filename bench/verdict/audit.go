package verdict

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
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
	return &schemas.Verdict{
		Primary:           schemas.Endpoint{Condition: AuditCondition, Metric: AuditMetric},
		Decision:          decision,
		Action:            actionFor(decision, nil, ""),
		Effect:            eff,
		Alpha:             alpha,
		MDE:               mdePP(eff.NTasks, defaultAssumedSD, alpha),
		MDEAssumedSD:      true,
		SD:                defaultAssumedSD,
		TasksNeeded:       tasksNeeded(eff.DBar, defaultAssumedSD, alpha),
		RandomizationUnit: randomizationUnit,
		TieRule:           tieRule,
		Guards:            []schemas.GuardResult{},
		InvalidReasons:    invalid,
	}, nil
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
