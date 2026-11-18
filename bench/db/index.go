package db

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// The results index in the database: idx_experiments, idx_runs,
// idx_analyses, idx_rejected and idx_meta, per organization.
//
// Every row is derived by resultsindex.Index.Derive, the same verified path
// the JSONL index uses: the manifest, store.SealedRuns (each run checked
// against its seal.json and seals.log) and the analysis folders. A run that
// does not verify is an idx_rejected row and never an idx_runs row. The
// tables are a cache: RebuildOrg drops an org's rows and derives them again,
// and nothing that decides (verdict, leaderboard, release) reads them.
//
// SyncOrg is incremental by seal: an experiment is re-derived only when the
// content of its seals.log (one line per sealed run with its seal hash) or
// its analysis folders changed since it was indexed. Like the JSONL Sync, it
// trusts rows of unchanged experiments; RebuildOrg re-verifies everything.

// tokensMetric is the metrics.json entry copied into idx_runs.tokens.
const tokensMetric = "tokens"

// Index states of an experiment, derived from verified runs only.
const (
	IndexStateCreated  = "created"  // no verified run
	IndexStatePartial  = "partial"  // some slots have a verified run
	IndexStateDone     = "done"     // every slot has a verified run
	IndexStateRejected = "rejected" // the experiment could not be read; see idx_rejected
)

// IndexedExperiment is a row of idx_experiments.
type IndexedExperiment struct {
	OrgID        string     `json:"org_id"`
	ExpID        string     `json:"exp_id"`
	Name         string     `json:"name"`
	Suite        string     `json:"suite"`
	Arms         []string   `json:"arms"`
	State        string     `json:"state"`
	Decision     string     `json:"decision,omitempty"`
	Action       string     `json:"action,omitempty"`
	AnalysisID   string     `json:"analysis_id,omitempty"`
	CostUSD      *float64   `json:"cost_usd"`
	CreatedAt    time.Time  `json:"created_at"`
	FrozenAt     time.Time  `json:"frozen_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	RunnerPool   string     `json:"runner_pool,omitempty"`
	ManifestHash string     `json:"manifest_hash"`
	Slots        int        `json:"slots"`
	Runs         int        `json:"runs"`
	Rejected     int        `json:"rejected"`
	Fingerprint  string     `json:"-"`
	IndexedAt    time.Time  `json:"indexed_at"`
}

// IndexedRun is a row of idx_runs: the JSONL run row plus slot, pair and
// token count.
type IndexedRun struct {
	resultsindex.RunRow
	SlotID string   `json:"slot_id"`
	PairID string   `json:"pair_id"`
	Tokens *float64 `json:"tokens"`
}

// derived is one experiment's rows ready to write.
type derived struct {
	exp      IndexedExperiment
	runs     []IndexedRun
	analyses []resultsindex.AnalysisRow
	rejects  []resultsindex.Rejected
}

// OrgIndex is one organization's results index in the database over the
// org's run store. It implements resultsindex.Source, so trends and the
// query command read it as they read the JSONL index.
type OrgIndex struct {
	d   *DB
	org string
	st  *store.FSStore
	ix  *resultsindex.Index // derivation only; its JSONL file is never touched
	now func() time.Time
}

var _ resultsindex.Source = (*OrgIndex)(nil)

// OrgIndex returns the index of orgID over st (the org's store: runs/orgs/<id>
// hosted, the runs root in local mode).
func (d *DB) OrgIndex(orgID string, st *store.FSStore, logger schemas.Logger) *OrgIndex {
	return &OrgIndex{d: d, org: orgID, st: st, ix: resultsindex.New(st, logger), now: time.Now}
}

// SyncOrg brings the org's index up to date incrementally (OrgIndex.Sync).
func (d *DB) SyncOrg(ctx context.Context, orgID string, st *store.FSStore) (resultsindex.Status, error) {
	return d.OrgIndex(orgID, st, nil).Sync(ctx)
}

// RebuildOrg derives the org's whole index again (OrgIndex.Rebuild).
func (d *DB) RebuildOrg(ctx context.Context, orgID string, st *store.FSStore) (resultsindex.Status, error) {
	return d.OrgIndex(orgID, st, nil).Rebuild(ctx)
}

// Sync re-derives experiments whose seals or analyses changed, drops
// experiments that are gone and leaves the rest. The first sync of an org
// is a full build.
func (o *OrgIndex) Sync(ctx context.Context) (resultsindex.Status, error) {
	o.d.ixMu.Lock()
	defer o.d.ixMu.Unlock()
	ids, err := o.st.ListExperiments(ctx)
	if err != nil {
		return resultsindex.Status{}, fmt.Errorf("sync index of %s: %w", o.org, err)
	}
	have, err := o.fingerprints(ctx)
	if err != nil {
		return resultsindex.Status{}, err
	}
	_, built, err := o.meta(ctx)
	if err != nil {
		return resultsindex.Status{}, err
	}
	changed := false
	for id := range have {
		if !slices.Contains(ids, id) {
			if err := o.write(ctx, id, nil, false); err != nil {
				return resultsindex.Status{}, err
			}
			changed = true
		}
	}
	for _, id := range ids {
		fp, err := fingerprint(o.st.Root(), id)
		if err != nil {
			return resultsindex.Status{}, err
		}
		if old, ok := have[id]; ok && old == fp {
			continue
		}
		dv, err := o.derive(ctx, id, fp)
		if err != nil {
			return resultsindex.Status{}, err
		}
		if err := o.write(ctx, id, dv, false); err != nil {
			return resultsindex.Status{}, err
		}
		changed = true
	}
	if !changed && !built.IsZero() {
		return o.status(ctx)
	}
	if err := o.touch(ctx, built.IsZero()); err != nil {
		return resultsindex.Status{}, err
	}
	return o.status(ctx)
}

// Rebuild derives every experiment again, verifying each seal, and replaces
// the org's rows in one transaction.
func (o *OrgIndex) Rebuild(ctx context.Context) (resultsindex.Status, error) {
	o.d.ixMu.Lock()
	defer o.d.ixMu.Unlock()
	ids, err := o.st.ListExperiments(ctx)
	if err != nil {
		return resultsindex.Status{}, fmt.Errorf("rebuild index of %s: %w", o.org, err)
	}
	all := make([]*derived, 0, len(ids))
	for _, id := range ids {
		fp, err := fingerprint(o.st.Root(), id)
		if err != nil {
			return resultsindex.Status{}, err
		}
		dv, err := o.derive(ctx, id, fp)
		if err != nil {
			return resultsindex.Status{}, err
		}
		all = append(all, dv)
	}
	now := Millis(o.now())
	err = o.d.Tx(ctx, func(tx *sql.Tx) error {
		if err := o.lockMeta(ctx, tx); err != nil {
			return err
		}
		for _, t := range []string{"idx_runs", "idx_analyses", "idx_rejected", "idx_experiments"} {
			if _, err := tx.ExecContext(ctx, o.d.Rebind(`DELETE FROM `+t+` WHERE org_id = ?`), o.org); err != nil {
				return err
			}
		}
		for _, dv := range all {
			if err := o.insert(ctx, tx, dv); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, o.d.Rebind(`UPDATE idx_meta SET built_at = ?, updated_at = ? WHERE org_id = ?`), now, now, o.org)
		return err
	})
	if err != nil {
		return resultsindex.Status{}, fmt.Errorf("rebuild index of %s: %w", o.org, err)
	}
	return o.status(ctx)
}

// UpdateExperiment re-derives one experiment (after a run seals or an
// analysis is written); an experiment that no longer exists loses its rows.
func (o *OrgIndex) UpdateExperiment(ctx context.Context, expID string) error {
	o.d.ixMu.Lock()
	defer o.d.ixMu.Unlock()
	if _, err := o.st.LoadManifest(ctx, expID); err != nil {
		if errors.Is(err, schemas.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			if err := o.write(ctx, expID, nil, true); err != nil {
				return err
			}
			return nil
		}
	}
	fp, err := fingerprint(o.st.Root(), expID)
	if err != nil {
		return err
	}
	dv, err := o.derive(ctx, expID, fp)
	if err != nil {
		return err
	}
	return o.write(ctx, expID, dv, true)
}

// Load reads the org's whole index into a resultsindex.Snapshot (the Reader
// trends and the query command use).
func (o *OrgIndex) Load(ctx context.Context) (*resultsindex.Snapshot, error) {
	exists, built, err := o.meta(ctx)
	if err != nil {
		return nil, err
	}
	s := resultsindex.NewSnapshot(o.d.driver, o.location(), exists && !built.IsZero())
	if err := o.loadInto(ctx, s); err != nil {
		return nil, err
	}
	s.Sort()
	return s, nil
}

func (o *OrgIndex) location() string {
	if o.d.path != "" {
		return o.d.path + "#" + o.org
	}
	return "postgres#" + o.org
}

// derive reads one experiment through resultsindex.Derive and adds the
// experiment row.
func (o *OrgIndex) derive(ctx context.Context, expID, fp string) (*derived, error) {
	snap, err := o.ix.Derive(ctx, expID)
	if err != nil {
		return nil, fmt.Errorf("index %s: %w", expID, err)
	}
	dv := &derived{analyses: snap.AnalysisRows, rejects: snap.Rejects}
	e := &dv.exp
	e.OrgID, e.ExpID, e.Fingerprint, e.IndexedAt = o.org, expID, fp, Time(Millis(o.now()))
	e.Arms = []string{}
	m, err := o.st.LoadManifest(ctx, expID)
	if err != nil {
		e.State = IndexStateRejected
		e.Rejected = len(dv.rejects)
		return dv, nil
	}
	slots := map[string]schemas.Slot{}
	for _, s := range m.Slots {
		slots[slotKey(s.Task, s.Arm, s.Condition, s.Trial)] = s
	}
	covered := map[string]bool{}
	var cost float64
	var costs int
	for _, r := range snap.RunRows {
		ir := IndexedRun{RunRow: r}
		if s, ok := slots[slotKey(r.Task, r.Arm, r.Condition, r.Trial)]; ok {
			ir.SlotID, ir.PairID = s.SlotID, s.PairID
			covered[s.SlotID] = true
		}
		if v, ok := r.Metrics[tokensMetric]; ok {
			ir.Tokens = &v
		}
		if r.CostUSD != nil {
			cost += *r.CostUSD
			costs++
		}
		dv.runs = append(dv.runs, ir)
	}
	e.Name, e.Suite, e.ManifestHash = m.Experiment.Name, suiteRef(m), m.Hash
	for id := range m.Arms {
		e.Arms = append(e.Arms, id)
	}
	slices.Sort(e.Arms)
	e.CreatedAt, e.FrozenAt = m.CreatedAt.UTC(), m.CreatedAt.UTC()
	if m.Experiment.Runner != nil {
		e.RunnerPool = m.Experiment.Runner.Pool
	}
	e.Slots, e.Runs, e.Rejected = len(m.Slots), len(dv.runs), len(dv.rejects)
	if costs > 0 {
		e.CostUSD = &cost
	}
	switch {
	case len(dv.runs) == 0:
		e.State = IndexStateCreated
	case len(covered) >= len(m.Slots):
		e.State = IndexStateDone
		if info, err := os.Stat(filepath.Join(o.st.Root(), expID, "seals.log")); err == nil {
			t := info.ModTime().UTC().Truncate(time.Millisecond)
			e.FinishedAt = &t
		}
	default:
		e.State = IndexStatePartial
	}
	if n := len(dv.analyses); n > 0 {
		newest := slices.MaxFunc(dv.analyses, func(a, b resultsindex.AnalysisRow) int {
			if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
				return c
			}
			return strings.Compare(a.AnalysisID, b.AnalysisID)
		})
		e.Decision, e.Action, e.AnalysisID = newest.Decision, newest.Action, newest.AnalysisID
	}
	return dv, nil
}

func slotKey(task, arm, condition string, trial int) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d", task, arm, condition, trial)
}

// suiteRef is the manifest's suite as id@version (resultsindex does the same).
func suiteRef(m *schemas.Manifest) string {
	if m.SuiteVersion == "" {
		return cmp.Or(m.SuiteID, m.Experiment.Suite)
	}
	return m.SuiteID + "@" + m.SuiteVersion
}

// fingerprint is what an experiment's rows depend on: the bytes of seals.log
// (each sealed run and its seal hash), the size and change time of
// manifest.json and the names of the analysis folders.
// It only detects change; the rows themselves come from the verified path.
func fingerprint(root, expID string) (string, error) {
	dir := filepath.Join(root, expID)
	h := sha256.New()
	b, err := os.ReadFile(filepath.Join(dir, "seals.log"))
	switch {
	case err == nil:
		fmt.Fprintf(h, "seals %d\n", len(b))
		h.Write(b)
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintln(h, "seals none")
	default:
		return "", fmt.Errorf("fingerprint %s: %w", expID, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "manifest.json")); err == nil {
		fmt.Fprintf(h, "manifest %d %d\n", info.Size(), info.ModTime().UnixNano())
	}
	entries, err := os.ReadDir(filepath.Join(dir, "analysis"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("fingerprint %s: %w", expID, err)
	}
	for _, e := range entries {
		fmt.Fprintf(h, "analysis %s\n", e.Name())
	}
	return hex.EncodeToString(h.Sum(nil))[:32], nil
}

// lockMeta makes sure the org's idx_meta row exists and locks it, so index
// writers of one org take turns (Postgres row lock; SQLite's transaction
// already holds the write lock).
func (o *OrgIndex) lockMeta(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, o.d.Rebind(`INSERT INTO idx_meta (org_id, built_at, updated_at) VALUES (?, 0, 0)
		ON CONFLICT (org_id) DO NOTHING`), o.org); err != nil {
		return err
	}
	var id string
	return tx.QueryRowContext(ctx, o.d.Rebind(`SELECT org_id FROM idx_meta WHERE org_id = ?`+o.d.ForUpdate()), o.org).Scan(&id)
}

// write replaces one experiment's rows with dv (nil drops them).
func (o *OrgIndex) write(ctx context.Context, expID string, dv *derived, touch bool) error {
	err := o.d.Tx(ctx, func(tx *sql.Tx) error {
		if err := o.lockMeta(ctx, tx); err != nil {
			return err
		}
		for _, t := range []string{"idx_runs", "idx_analyses", "idx_rejected", "idx_experiments"} {
			if _, err := tx.ExecContext(ctx, o.d.Rebind(`DELETE FROM `+t+` WHERE org_id = ? AND exp_id = ?`), o.org, expID); err != nil {
				return err
			}
		}
		if dv != nil {
			if err := o.insert(ctx, tx, dv); err != nil {
				return err
			}
		}
		if touch {
			_, err := tx.ExecContext(ctx, o.d.Rebind(`UPDATE idx_meta SET updated_at = ? WHERE org_id = ?`), Millis(o.now()), o.org)
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("write index of %s/%s: %w", o.org, expID, err)
	}
	return nil
}

// touch stamps updated_at, and built_at on a first full build.
func (o *OrgIndex) touch(ctx context.Context, first bool) error {
	now := Millis(o.now())
	return o.d.Tx(ctx, func(tx *sql.Tx) error {
		if err := o.lockMeta(ctx, tx); err != nil {
			return err
		}
		q := `UPDATE idx_meta SET updated_at = ? WHERE org_id = ?`
		args := []any{now, o.org}
		if first {
			q = `UPDATE idx_meta SET built_at = ?, updated_at = ? WHERE org_id = ?`
			args = []any{now, now, o.org}
		}
		_, err := tx.ExecContext(ctx, o.d.Rebind(q), args...)
		return err
	})
}

func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// insert writes the rows of one derived experiment.
func (o *OrgIndex) insert(ctx context.Context, tx *sql.Tx, dv *derived) error {
	d, e := o.d, &dv.exp
	var finished sql.NullInt64
	if e.FinishedAt != nil {
		finished = NullMillis(*e.FinishedAt)
	}
	if _, err := tx.ExecContext(ctx, d.Rebind(`INSERT INTO idx_experiments (org_id, exp_id, name, suite, arms, state, decision, action,
		analysis_id, cost_usd, created_at, frozen_at, finished_at, runner_pool, manifest_hash, slots, runs, rejected, fingerprint, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		e.OrgID, e.ExpID, e.Name, e.Suite, mustJSON(e.Arms), e.State, e.Decision, e.Action,
		e.AnalysisID, nullFloat(e.CostUSD), Millis(e.CreatedAt), Millis(e.FrozenAt), finished, e.RunnerPool, e.ManifestHash,
		e.Slots, e.Runs, e.Rejected, e.Fingerprint, Millis(e.IndexedAt)); err != nil {
		return err
	}
	if len(dv.runs) > 0 {
		stmt, err := tx.PrepareContext(ctx, d.Rebind(`INSERT INTO idx_runs (org_id, exp_id, run_id, pair_id, slot_id, suite, task, arm,
			condition, trial, outcome, score, success, cost_usd, tokens, wall_s, started_at, exp_created_at, seal_hash, synthetic,
			agent_id, config_hash, model, grade_source, not_repeatable, metrics, problems)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`))
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := range dv.runs {
			r := &dv.runs[i]
			problems := r.Problems
			if problems == nil {
				problems = []string{}
			}
			metricsMap := r.Metrics
			if metricsMap == nil {
				metricsMap = map[string]float64{}
			}
			if _, err := stmt.ExecContext(ctx, o.org, r.Experiment, r.RunID, r.PairID, r.SlotID, r.Suite, r.Task, r.Arm,
				r.Condition, r.Trial, r.Outcome, nullFloat(r.Score), nullFloat(r.Success), nullFloat(r.CostUSD), nullFloat(r.Tokens),
				nullFloat(r.WallTimeS), Millis(r.Started), Millis(r.ExperimentCreated), r.SealHash, Bool(r.Synthetic),
				r.Agent, r.ConfigHash, r.Model, r.GradeSource, Bool(r.NotRepeatable), mustJSON(metricsMap), mustJSON(problems)); err != nil {
				return err
			}
		}
	}
	for _, a := range dv.analyses {
		if _, err := tx.ExecContext(ctx, d.Rebind(`INSERT INTO idx_analyses (org_id, exp_id, analysis_id, decision, action, policy, grade_source, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), o.org, a.Experiment, a.AnalysisID, a.Decision, a.Action, a.Policy, a.GradeSource, Millis(a.CreatedAt)); err != nil {
			return err
		}
	}
	for _, r := range dv.rejects {
		if _, err := tx.ExecContext(ctx, d.Rebind(`INSERT INTO idx_rejected (org_id, exp_id, run_id, seal_hash, reason) VALUES (?, ?, ?, ?, ?)`),
			o.org, cmp.Or(r.Experiment, e.ExpID), r.RunID, r.SealHash, r.Reason); err != nil {
			return err
		}
	}
	return nil
}

// fingerprints maps exp id to the fingerprint it was indexed with.
func (o *OrgIndex) fingerprints(ctx context.Context) (map[string]string, error) {
	rows, err := o.d.Query(ctx, `SELECT exp_id, fingerprint FROM idx_experiments WHERE org_id = ?`, o.org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, fp string
		if err := rows.Scan(&id, &fp); err != nil {
			return nil, err
		}
		out[id] = fp
	}
	return out, rows.Err()
}

// meta returns whether the org has an idx_meta row and its built_at.
func (o *OrgIndex) meta(ctx context.Context) (bool, time.Time, error) {
	var built int64
	err := o.d.QueryRow(ctx, `SELECT built_at FROM idx_meta WHERE org_id = ?`, o.org).Scan(&built)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	return true, Time(built), nil
}

// status counts the org's rows.
func (o *OrgIndex) status(ctx context.Context) (resultsindex.Status, error) {
	st := resultsindex.Status{Format: o.d.driver, Path: o.location()}
	var built, updated int64
	err := o.d.QueryRow(ctx, `SELECT built_at, updated_at FROM idx_meta WHERE org_id = ?`, o.org).Scan(&built, &updated)
	switch {
	case err == nil:
		st.Exists = built != 0
		st.BuiltAt, st.UpdatedAt = Time(built), Time(updated)
	case !errors.Is(err, sql.ErrNoRows):
		return st, err
	}
	for _, c := range []struct {
		dst   *int
		table string
	}{{&st.Experiments, "idx_experiments"}, {&st.Runs, "idx_runs"}, {&st.Analyses, "idx_analyses"}, {&st.Rejected, "idx_rejected"}} {
		if err := o.d.QueryRow(ctx, `SELECT count(*) FROM `+c.table+` WHERE org_id = ?`, o.org).Scan(c.dst); err != nil {
			return st, err
		}
	}
	return st, nil
}

// Status reports the org's index without changing it.
func (o *OrgIndex) Status(ctx context.Context) (resultsindex.Status, error) { return o.status(ctx) }

// loadInto fills s with every row of the org.
func (o *OrgIndex) loadInto(ctx context.Context, s *resultsindex.Snapshot) error {
	var built, updated int64
	if err := o.d.QueryRow(ctx, `SELECT built_at, updated_at FROM idx_meta WHERE org_id = ?`, o.org).Scan(&built, &updated); err == nil {
		s.BuiltAt, s.UpdatedAt = Time(built), Time(updated)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	fps, err := o.fingerprints(ctx)
	if err != nil {
		return err
	}
	s.Experiments = fps
	runs, _, err := o.d.queryRuns(ctx, RunQuery{OrgID: o.org, all: true})
	if err != nil {
		return err
	}
	for _, r := range runs {
		s.RunRows = append(s.RunRows, r.RunRow)
	}
	if s.AnalysisRows, err = o.d.IndexedAnalyses(ctx, o.org, ""); err != nil {
		return err
	}
	s.Rejects, err = o.d.IndexedRejected(ctx, o.org, "")
	return err
}
