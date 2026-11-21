package resultsindex

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// Files read beside the sealed runs. analysis/ and seals.log belong to the
// experiment folder; the proxy ledger is a sealed run file.
const (
	sealsLogFile = "seals.log"
	analysisDir  = "analysis"
	verdictFile  = "verdict.json"
	ledgerFile   = "proxy.ledger.jsonl"
)

// sealProblemPrefix starts the problem store.SealedRuns reports for a run
// whose files do not match its seal.
const sealProblemPrefix = "seal does not verify"

// Metric ids copied into the row's own columns.
const (
	metricVerifiedSuccess = "verified_success"
	metricCostUSD         = "cost_usd"
	metricWallTime        = "wall_time_s"
	metricFalseCompletion = "false_completion"
)

// Index maintains the results index file of one run store. Every row it
// writes was read through store.SealedRuns, which compares each run with its
// seal.json and with seals.log; a run that does not verify becomes a
// Rejected entry instead of a row.
type Index struct {
	st     *store.FSStore
	path   string
	logger schemas.Logger
	now    func() time.Time
	mu     sync.Mutex // one writer at a time in this process
}

// New returns the index of a store. The file lives at
// <store root>/.index/results.jsonl and is created on the first write.
func New(st *store.FSStore, logger schemas.Logger) *Index {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &Index{st: st, path: filepath.Join(st.Root(), DirName, FileName), logger: logger, now: time.Now}
}

// Path returns the index file path.
func (ix *Index) Path() string { return ix.path }

// Snapshot reads the index file as it is now. A missing file is an empty
// snapshot with Status().Exists false.
func (ix *Index) Snapshot() (*Snapshot, error) {
	return readSnapshot(ix.path)
}

// Rebuild derives the whole index again from the sealed run folders,
// verifying every seal, and replaces the file.
func (ix *Index) Rebuild(ctx context.Context) (Status, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids, err := ix.st.ListExperiments(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("rebuild results index: %w", err)
	}
	next := &Snapshot{Experiments: map[string]string{}, path: ix.path}
	for _, id := range ids {
		if err := ix.addExperiment(ctx, next, id); err != nil {
			return Status{}, fmt.Errorf("rebuild results index: %w", err)
		}
	}
	now := ix.now().UTC()
	next.BuiltAt, next.UpdatedAt = now, now
	return ix.write(next)
}

// UpdateExperiment re-derives one experiment's rows, verifying its seals
// through the store's cache, and leaves every other row as it is. It is the
// incremental update a run seal triggers. An experiment that no longer
// exists has its rows removed.
func (ix *Index) UpdateExperiment(ctx context.Context, expID string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	snap, err := readSnapshot(ix.path)
	if err != nil {
		return err
	}
	snap.drop(expID)
	if _, err := ix.st.LoadManifest(ctx, expID); err == nil {
		if err := ix.addExperiment(ctx, snap, expID); err != nil {
			return fmt.Errorf("update results index: %w", err)
		}
	} else if !errors.Is(err, schemas.ErrNotFound) && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("update results index %s: %w", expID, err)
	}
	snap.UpdatedAt = ix.now().UTC()
	_, err = ix.write(snap)
	return err
}

// Sync brings the index up to date incrementally: experiments whose seals.log
// and analysis folders are unchanged since they were indexed keep their rows,
// the others are re-derived, and experiments that are gone are dropped. A
// missing index file is built in full. Sync trusts rows it wrote earlier for
// unchanged experiments; Rebuild re-verifies everything.
func (ix *Index) Sync(ctx context.Context) (Status, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	snap, err := readSnapshot(ix.path)
	if err != nil {
		return Status{}, err
	}
	ids, err := ix.st.ListExperiments(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("sync results index: %w", err)
	}
	changed := !snap.exists
	for id := range snap.Experiments {
		if !slices.Contains(ids, id) {
			snap.drop(id)
			changed = true
		}
	}
	for _, id := range ids {
		fp, err := ix.fingerprint(id)
		if err != nil {
			return Status{}, fmt.Errorf("sync results index: %w", err)
		}
		if old, ok := snap.Experiments[id]; ok && old == fp {
			continue
		}
		snap.drop(id)
		if err := ix.addExperiment(ctx, snap, id); err != nil {
			return Status{}, fmt.Errorf("sync results index: %w", err)
		}
		changed = true
	}
	if !changed {
		return snap.Status(), nil
	}
	now := ix.now().UTC()
	if !snap.exists {
		snap.BuiltAt = now
	}
	snap.UpdatedAt = now
	return ix.write(snap)
}

// write sorts and writes a snapshot and returns its status.
func (ix *Index) write(s *Snapshot) (Status, error) {
	s.sortRows()
	if err := writeSnapshot(ix.path, s); err != nil {
		return Status{}, err
	}
	s.exists = true
	return s.Status(), nil
}

// drop removes every row of one experiment.
func (s *Snapshot) drop(expID string) {
	delete(s.Experiments, expID)
	s.RunRows = slices.DeleteFunc(s.RunRows, func(r RunRow) bool { return r.Experiment == expID })
	s.AnalysisRows = slices.DeleteFunc(s.AnalysisRows, func(a AnalysisRow) bool { return a.Experiment == expID })
	s.Rejects = slices.DeleteFunc(s.Rejects, func(r Rejected) bool { return r.Experiment == expID })
}

// addExperiment derives one experiment's rows into s. The fingerprint is
// taken before reading, so a run sealed while the rows are read changes the
// fingerprint and the next Sync reads the experiment again. Only a cancelled
// context fails it: an experiment whose runs cannot be read is one Rejected
// entry with the reason, so one bad folder never hides the rest.
func (ix *Index) addExperiment(ctx context.Context, s *Snapshot, expID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fp, err := ix.fingerprint(expID)
	if err != nil {
		return err
	}
	s.Experiments[expID] = fp
	analyses, err := ix.analyses(expID)
	if err != nil {
		ix.logger.Warn("results index: analyses unreadable", "exp_id", expID, "error", err.Error())
	}
	s.AnalysisRows = append(s.AnalysisRows, analyses...)
	m, err := ix.st.LoadManifest(ctx, expID)
	if err != nil {
		s.Rejects = append(s.Rejects, Rejected{Experiment: expID, Reason: "manifest: " + err.Error()})
		return nil
	}
	srs, err := ix.st.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.Rejects = append(s.Rejects, Rejected{Experiment: expID, Reason: err.Error()})
		return nil
	}
	synthetic := classify(m, srs)
	for i := range srs {
		sr := &srs[i]
		if reason := sealProblem(sr); reason != "" {
			s.Rejects = append(s.Rejects, Rejected{Experiment: expID, RunID: sr.Run.RunID, SealHash: sr.SealHash, Reason: reason})
			continue
		}
		s.RunRows = append(s.RunRows, runRow(m, sr, synthetic[sr.Slot.Arm]))
	}
	return nil
}

// fingerprint summarizes what an experiment's rows depend on and what
// changes when a run seals or an analysis is written: the size and change
// time of seals.log and the names of the analysis folders.
func (ix *Index) fingerprint(expID string) (string, error) {
	dir := filepath.Join(ix.st.Root(), expID)
	h := sha256.New()
	info, err := os.Stat(filepath.Join(dir, sealsLogFile))
	switch {
	case err == nil:
		fmt.Fprintf(h, "seals %d %d\n", info.Size(), info.ModTime().UnixNano())
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintln(h, "seals none")
	default:
		return "", fmt.Errorf("fingerprint %s: %w", expID, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, analysisDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("fingerprint %s: %w", expID, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // an analysis still being staged (store.WriteAnalysis) is not published yet
		}
		fmt.Fprintf(h, "analysis %s\n", e.Name())
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// analyses reads every analysis of an experiment. A folder whose
// verdict.json is missing or unreadable is skipped with a warning.
func (ix *Index) analyses(expID string) ([]AnalysisRow, error) {
	base := filepath.Join(ix.st.Root(), expID, analysisDir)
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []AnalysisRow
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue // a dot folder is an analysis still being staged (store.WriteAnalysis)
		}
		var a struct {
			AnalysisID  string    `json:"analysis_id"`
			CreatedAt   time.Time `json:"created_at"`
			GradeSource string    `json:"grade_source"`
			Verdict     struct {
				Decision string `json:"decision"`
				Action   string `json:"action"`
			} `json:"verdict"`
			Policy *struct {
				Decision string `json:"decision"`
			} `json:"policy"`
		}
		b, err := os.ReadFile(filepath.Join(base, e.Name(), verdictFile))
		if err == nil {
			err = json.Unmarshal(b, &a)
		}
		if err != nil {
			ix.logger.Warn("results index: analysis skipped", "exp_id", expID, "analysis", e.Name(), "error", err.Error())
			continue
		}
		row := AnalysisRow{Experiment: expID, AnalysisID: cmp.Or(a.AnalysisID, e.Name()), Decision: a.Verdict.Decision,
			Action: a.Verdict.Action, GradeSource: a.GradeSource, CreatedAt: a.CreatedAt}
		if a.Policy != nil {
			row.Policy = a.Policy.Decision
		}
		out = append(out, row)
	}
	return out, nil
}

// sealProblem returns the seal failure among a run's problems, or "".
func sealProblem(sr *schemas.SealedRun) string {
	for _, p := range sr.Problems {
		if strings.HasPrefix(p, sealProblemPrefix) {
			return p
		}
	}
	return ""
}

// runRow converts one verified sealed run.
func runRow(m *schemas.Manifest, sr *schemas.SealedRun, synthetic bool) RunRow {
	r := RunRow{
		Experiment: m.ExpID, ExperimentCreated: m.CreatedAt.UTC(), RunID: sr.Run.RunID,
		Suite: suiteRef(m), SuiteLockHash: m.SuiteLockHash, Task: sr.Slot.Task, Arm: sr.Slot.Arm, Condition: sr.Slot.Condition, Trial: sr.Slot.Trial,
		Outcome: string(sr.Run.Outcome), Started: sr.Run.Timing.Started.UTC(), SealHash: sr.SealHash,
		Synthetic: synthetic, GradeSource: sr.GradeSource, Metrics: map[string]float64{},
		NotRepeatable: utils.ExternalUnrepeatable(m),
		Agent:         sr.Run.Agent.ConfigID, ConfigHash: sr.Run.Agent.ConfigHash,
	}
	if arm, ok := m.Arms[sr.Slot.Arm]; ok {
		r.Agent, r.Model = cmp.Or(arm.Config.ID, r.Agent), arm.Config.Model
		r.ConfigHash = cmp.Or(arm.ConfigHash, r.ConfigHash)
	}
	if v, ok := sr.Run.Y.Float(); ok {
		r.Score = &v
	}
	for name, v := range sr.Metrics {
		if f, ok := v.Float(); ok {
			r.Metrics[name] = f
		} else if name == metricFalseCompletion && v.State == schemas.ValueNotApplicable {
			// A run whose verifier passed is never a false completion; it counts
			// as 0, as the release policy and the leaderboard read it.
			r.Metrics[name] = 0
		}
	}
	if y, ok := sr.Run.Outcome.Score(); ok {
		if v, has := r.Metrics[metricVerifiedSuccess]; has {
			y = 0
			if v >= 0.5 {
				y = 1
			}
		}
		r.Success = &y
	}
	if v, ok := r.Metrics[metricCostUSD]; ok {
		r.CostUSD = &v
	}
	if v, ok := r.Metrics[metricWallTime]; ok {
		r.WallTimeS = &v
	}
	r.Problems = slices.Clone(sr.Problems)
	return r
}

// suiteRef is the manifest's suite as id@version.
func suiteRef(m *schemas.Manifest) string {
	if m.SuiteVersion == "" {
		return cmp.Or(m.SuiteID, m.Experiment.Suite)
	}
	return m.SuiteID + "@" + m.SuiteVersion
}

// classify decides per arm whether an experiment's runs are synthetic, the
// way the leaderboard does (leaderboard/synthetic.go): a smoke experiment, a
// scripted control agent, or any run of the arm whose proxy ledger shows the
// scripted model. The ledger is read only from runs that verified.
func classify(m *schemas.Manifest, srs []schemas.SealedRun) map[string]bool {
	out := map[string]bool{}
	for id, arm := range m.Arms {
		out[id] = m.Experiment.NoPaidTraffic() || arm.Config.Adapter == adapterScripted || arm.Config.Scripted != nil
	}
	for i := range srs {
		sr := &srs[i]
		arm := sr.Slot.Arm
		if out[arm] || sealProblem(sr) != "" {
			continue
		}
		if _, ok := m.Arms[arm]; !ok && (m.Experiment.NoPaidTraffic() || sr.Run.Agent.Adapter == adapterScripted) {
			out[arm] = true
			continue
		}
		b, err := os.ReadFile(filepath.Join(sr.Dir, ledgerFile))
		if err == nil && scriptedLedger(b) {
			out[arm] = true
		}
	}
	return out
}

// adapterScripted is the adapter type of the scripted control agents.
const adapterScripted = "scripted"

// scriptedResponseID matches the ids the fault proxy's scripted model mints
// (see leaderboard/synthetic.go, which this mirrors).
var scriptedResponseID = regexp.MustCompile(`^(?:(?:msg|resp)_|chatcmpl-)[0-9a-z]{24}$`)

// scriptedLedger reports whether a proxy ledger shows the scripted model: a
// labelled line saying so, or, in a ledger with no labelled line, the
// scripted model's fingerprint (32 output tokens and a scripted response id).
// A line that does not parse is ignored here; the seal already vouched for
// the file, and the leaderboard is where a malformed ledger is an error.
func scriptedLedger(data []byte) bool {
	labelled, scripted, fingerprint := false, false, false
	for _, l := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		var e schemas.LedgerEntry
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		if e.Upstream != "" {
			labelled = true
			scripted = scripted || e.Upstream == schemas.LedgerUpstreamScripted
		}
		fingerprint = fingerprint || (e.Kind == "message" && e.Action == "pass" && e.Status == 200 &&
			e.Tokens != nil && e.Tokens.Out == 32 && scriptedResponseID.MatchString(e.ResponseID))
	}
	if labelled {
		return scripted
	}
	return fingerprint
}
