package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/resultsindex"
)

// Lists the UI reads from the results index. Every query is scoped to one
// organization. Limit defaults to 50 and is capped at 500; Total counts the
// rows matching the filters before paging.

// ExperimentQuery filters the experiments list.
type ExperimentQuery struct {
	OrgID string
	State string // created | partial | done | rejected; "" all
	Suite string // exact id@version, or an id alone to match every version
	Text  string // case-insensitive substring of the experiment id or name
	// Sort: "created" (newest first, the default), "created_asc", "name",
	// "cost" (highest first, unknown last), "finished" (newest first).
	Sort   string
	Limit  int
	Offset int
}

// ExperimentPage is one page of experiments.
type ExperimentPage struct {
	Experiments []IndexedExperiment `json:"experiments"`
	Total       int                 `json:"total"`
}

const expCols = `org_id, exp_id, name, suite, arms, state, decision, action, analysis_id, cost_usd, created_at, frozen_at,
	finished_at, runner_pool, manifest_hash, slots, runs, rejected, fingerprint, indexed_at`

func pageBounds(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	return min(limit, 500), max(offset, 0)
}

// ListIndexedExperiments lists an organization's experiments.
func (d *DB) ListIndexedExperiments(ctx context.Context, q ExperimentQuery) (ExperimentPage, error) {
	where := []string{"org_id = ?"}
	args := []any{q.OrgID}
	if q.State != "" {
		where, args = append(where, "state = ?"), append(args, q.State)
	}
	if q.Suite != "" {
		if strings.Contains(q.Suite, "@") {
			where, args = append(where, "suite = ?"), append(args, q.Suite)
		} else {
			where, args = append(where, `(suite = ? OR suite LIKE ? ESCAPE '\')`), append(args, q.Suite, likePrefix(q.Suite+"@"))
		}
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		pat := "%" + likeEscape(strings.ToLower(t)) + "%"
		where, args = append(where, `(lower(exp_id) LIKE ? ESCAPE '\' OR lower(name) LIKE ? ESCAPE '\')`), append(args, pat, pat)
	}
	order := "created_at DESC, exp_id DESC"
	switch q.Sort {
	case "", "created":
	case "created_asc":
		order = "created_at ASC, exp_id ASC"
	case "name":
		order = "lower(name) ASC, exp_id ASC"
	case "cost":
		order = "CASE WHEN cost_usd IS NULL THEN 1 ELSE 0 END, cost_usd DESC, exp_id ASC"
	case "finished":
		order = "CASE WHEN finished_at IS NULL THEN 1 ELSE 0 END, finished_at DESC, exp_id DESC"
	default:
		return ExperimentPage{}, fmt.Errorf("db: unknown experiment sort %q", q.Sort)
	}
	cond := strings.Join(where, " AND ")
	page := ExperimentPage{Experiments: []IndexedExperiment{}}
	if err := d.QueryRow(ctx, `SELECT count(*) FROM idx_experiments WHERE `+cond, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	limit, offset := pageBounds(q.Limit, q.Offset)
	rows, err := d.Query(ctx, `SELECT `+expCols+` FROM idx_experiments WHERE `+cond+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanIndexedExperiment(rows)
		if err != nil {
			return page, err
		}
		page.Experiments = append(page.Experiments, *e)
	}
	return page, rows.Err()
}

// IndexedExperimentByID returns one experiment row or ErrNotFound.
func (d *DB) IndexedExperimentByID(ctx context.Context, orgID, expID string) (*IndexedExperiment, error) {
	e, err := scanIndexedExperiment(d.QueryRow(ctx, `SELECT `+expCols+` FROM idx_experiments WHERE org_id = ? AND exp_id = ?`, orgID, expID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

func scanIndexedExperiment(row interface{ Scan(...any) error }) (*IndexedExperiment, error) {
	var e IndexedExperiment
	var arms string
	var cost sql.NullFloat64
	var created, frozen, indexed int64
	var finished sql.NullInt64
	if err := row.Scan(&e.OrgID, &e.ExpID, &e.Name, &e.Suite, &arms, &e.State, &e.Decision, &e.Action, &e.AnalysisID, &cost,
		&created, &frozen, &finished, &e.RunnerPool, &e.ManifestHash, &e.Slots, &e.Runs, &e.Rejected, &e.Fingerprint, &indexed); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(arms), &e.Arms); err != nil || e.Arms == nil {
		e.Arms = []string{}
	}
	e.CostUSD = floatPtr(cost)
	e.CreatedAt, e.FrozenAt, e.IndexedAt = Time(created), Time(frozen), Time(indexed)
	if finished.Valid {
		t := Time(finished.Int64)
		e.FinishedAt = &t
	}
	return &e, nil
}

// RunQuery filters the runs list. Empty strings match all.
type RunQuery struct {
	OrgID      string
	Experiment string
	Task       string
	Arm        string
	Condition  string
	Outcome    string
	Agent      string // agent config id
	Synthetic  string // resultsindex.SyntheticInclude (default) | SyntheticExclude | SyntheticOnly
	Limit      int
	Offset     int

	all bool // Load: every row, no paging, JSONL order
}

// RunPage is one page of runs, newest start first.
type RunPage struct {
	Runs  []IndexedRun `json:"runs"`
	Total int          `json:"total"`
}

const runCols = `exp_id, run_id, pair_id, slot_id, suite, task, arm, condition, trial, outcome, score, success, cost_usd, tokens,
	wall_s, started_at, exp_created_at, seal_hash, synthetic, agent_id, config_hash, model, grade_source, not_repeatable, metrics, problems`

// ListIndexedRuns lists an organization's verified runs.
func (d *DB) ListIndexedRuns(ctx context.Context, q RunQuery) (RunPage, error) {
	q.all = false
	runs, total, err := d.queryRuns(ctx, q)
	if runs == nil {
		runs = []IndexedRun{}
	}
	return RunPage{Runs: runs, Total: total}, err
}

func (d *DB) queryRuns(ctx context.Context, q RunQuery) ([]IndexedRun, int, error) {
	where := []string{"org_id = ?"}
	args := []any{q.OrgID}
	for _, p := range []struct{ col, v string }{{"exp_id", q.Experiment}, {"task", q.Task}, {"arm", q.Arm},
		{"condition", q.Condition}, {"outcome", q.Outcome}, {"agent_id", q.Agent}} {
		if p.v != "" {
			where, args = append(where, p.col+" = ?"), append(args, p.v)
		}
	}
	switch q.Synthetic {
	case "", resultsindex.SyntheticInclude:
	case resultsindex.SyntheticExclude:
		where = append(where, "synthetic = 0")
	case resultsindex.SyntheticOnly:
		where = append(where, "synthetic = 1")
	default:
		return nil, 0, fmt.Errorf("db: synthetic %q is not include, exclude or only", q.Synthetic)
	}
	cond := strings.Join(where, " AND ")
	total := 0
	query := `SELECT ` + runCols + ` FROM idx_runs WHERE ` + cond
	if q.all {
		query += ` ORDER BY exp_id, run_id`
	} else {
		if err := d.QueryRow(ctx, `SELECT count(*) FROM idx_runs WHERE `+cond, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
		limit, offset := pageBounds(q.Limit, q.Offset)
		query += ` ORDER BY started_at DESC, exp_id ASC, run_id ASC LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := d.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []IndexedRun
	for rows.Next() {
		var r IndexedRun
		var score, success, cost, tokens, wall sql.NullFloat64
		var started, created int64
		var synthetic, notRep int
		var metricsJSON, problemsJSON string
		if err := rows.Scan(&r.Experiment, &r.RunID, &r.PairID, &r.SlotID, &r.Suite, &r.Task, &r.Arm, &r.Condition, &r.Trial,
			&r.Outcome, &score, &success, &cost, &tokens, &wall, &started, &created, &r.SealHash, &synthetic, &r.Agent,
			&r.ConfigHash, &r.Model, &r.GradeSource, &notRep, &metricsJSON, &problemsJSON); err != nil {
			return nil, 0, err
		}
		r.Score, r.Success, r.CostUSD, r.Tokens, r.WallTimeS = floatPtr(score), floatPtr(success), floatPtr(cost), floatPtr(tokens), floatPtr(wall)
		r.Started, r.ExperimentCreated = Time(started), Time(created)
		r.Synthetic, r.NotRepeatable = synthetic != 0, notRep != 0
		r.Metrics = map[string]float64{}
		_ = json.Unmarshal([]byte(metricsJSON), &r.Metrics)
		var problems []string
		if json.Unmarshal([]byte(problemsJSON), &problems) == nil && len(problems) > 0 {
			r.Problems = problems
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// IndexedAnalyses lists an experiment's analyses oldest first, or every
// analysis of the org when expID is "".
func (d *DB) IndexedAnalyses(ctx context.Context, orgID, expID string) ([]resultsindex.AnalysisRow, error) {
	q := `SELECT exp_id, analysis_id, decision, action, policy, grade_source, created_at FROM idx_analyses WHERE org_id = ?`
	args := []any{orgID}
	if expID != "" {
		q, args = q+` AND exp_id = ?`, append(args, expID)
	}
	rows, err := d.Query(ctx, q+` ORDER BY exp_id, created_at, analysis_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []resultsindex.AnalysisRow
	for rows.Next() {
		var a resultsindex.AnalysisRow
		var created int64
		if err := rows.Scan(&a.Experiment, &a.AnalysisID, &a.Decision, &a.Action, &a.Policy, &a.GradeSource, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = Time(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// IndexedRejected lists runs and experiments that failed verification, of
// one experiment or of the whole org when expID is "".
func (d *DB) IndexedRejected(ctx context.Context, orgID, expID string) ([]resultsindex.Rejected, error) {
	q := `SELECT exp_id, run_id, seal_hash, reason FROM idx_rejected WHERE org_id = ?`
	args := []any{orgID}
	if expID != "" {
		q, args = q+` AND exp_id = ?`, append(args, expID)
	}
	rows, err := d.Query(ctx, q+` ORDER BY exp_id, run_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []resultsindex.Rejected
	for rows.Next() {
		var r resultsindex.Rejected
		if err := rows.Scan(&r.Experiment, &r.RunID, &r.SealHash, &r.Reason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
