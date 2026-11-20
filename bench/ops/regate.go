package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Regate recomputes the gates of every counted sealed run of expID and
// writes them as regrade regradeID. Every run's seal is verified, and a run
// with an evidence problem (a seal that does not verify, a slot with more
// than one counted run) refuses the regate; every regate is computed before
// anything is written, so a failure leaves no partial regrade. It returns the
// regrade record and the runs as sealed and as regated. frozenLock is the
// path of the experiment's suite.lock when the suite's lock has changed since
// the freeze (see CheckSuiteDrift). reg must hold st as its store.
func Regate(ctx context.Context, reg *schemas.Registry, st *store.FSStore, logger schemas.Logger, expID, regradeID, note, frozenLock string) (*store.RegradeInfo, []schemas.SealedRun, []schemas.SealedRun, error) {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	m, err := st.LoadManifest(ctx, expID)
	if err != nil {
		return nil, nil, nil, err
	}
	s, err := engine.LoadSuite(ctx, reg, m.Experiment.Suite)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := CheckSuiteDrift(m, s, frozenLock); err != nil {
		return nil, nil, nil, err
	}
	if err := CheckEvaluator(ctx, reg, m, s); err != nil {
		return nil, nil, nil, err
	}
	runs, err := st.SealedRuns(ctx, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load sealed runs: %w", err)
	}
	results := make([]*store.GateRegrade, len(runs))
	for i := range runs {
		sr := &runs[i]
		if len(sr.Problems) > 0 {
			return nil, nil, nil, fmt.Errorf("run %s: %s", sr.Run.RunID, strings.Join(sr.Problems, "; "))
		}
		if err := st.Verify(ctx, expID, sr.Run.RunID); err != nil {
			return nil, nil, nil, fmt.Errorf("run %s: seal does not verify: %w", sr.Run.RunID, err)
		}
		r, err := engine.Regate(ctx, reg, m, s, sr)
		if err != nil {
			return nil, nil, nil, err
		}
		results[i] = &store.GateRegrade{Gates: r.Gates, Recomputed: r.Recomputed, SealedOutcome: sr.Run.Outcome,
			Outcome: r.Outcome, Y: r.Y, Metrics: r.Metrics, Notes: r.Notes}
		for _, n := range r.Notes {
			logger.Warn("regate note", "run_id", sr.Run.RunID, "note", n)
		}
	}
	info := store.RegradeInfo{SuiteVersion: m.SuiteVersion, GateVersions: engine.RegateVersions(reg, m), Note: note}
	if s.LockHash != m.SuiteLockHash {
		info.SuiteLockHash = s.LockHash
	}
	batch := make(map[string]*store.GateRegrade, len(runs))
	for i := range runs {
		batch[runs[i].Run.RunID] = results[i]
	}
	// One atomic publish: a failure or interruption leaves no regrade
	// (audit 8-D-03).
	if err := st.WriteGateRegrades(ctx, expID, regradeID, info, batch); err != nil {
		return nil, nil, nil, err
	}
	after, err := st.SealedRuns(ctx, expID, regradeID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load regated runs: %w", err)
	}
	return &info, runs, after, nil
}

// CheckSuiteDrift makes sure the suite the gates read now is the one the
// experiment froze. With the same lock hash it is. When the lock changed (a
// probe or task added later), the caller must hand in the frozen suite.lock:
// its sha256 must be the manifest's suite_lock_hash, and the entry of every
// task and probe condition the experiment's slots use must be the same in
// both locks, so the protected paths and the tests the gates compare against
// did not move.
func CheckSuiteDrift(m *schemas.Manifest, s *schemas.Suite, frozenLock string) error {
	if suite.LockHashMatches(s, m.SuiteLockHash) { // Lock fingerprints: a lock that only gained fingerprints still matches
		return nil
	}
	if frozenLock == "" {
		return fmt.Errorf("suite %s lock hash %s differs from the manifest's %s; pass the frozen lock with -suite-lock to check the tasks it uses",
			s.ID, short(s.LockHash), short(m.SuiteLockHash))
	}
	if s.Lock == nil {
		return fmt.Errorf("suite %s has no lock to compare", s.ID)
	}
	raw, err := os.ReadFile(frozenLock)
	if err != nil {
		return fmt.Errorf("read frozen lock: %w", err)
	}
	if sum := utils.SHA256Bytes(raw); sum != m.SuiteLockHash {
		return fmt.Errorf("frozen lock %s hashes to %s, the manifest froze %s", frozenLock, short(sum), short(m.SuiteLockHash))
	}
	var frozen schemas.SuiteLock
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return fmt.Errorf("decode frozen lock: %w", err)
	}
	for _, sl := range m.Slots {
		if err := sameEntry("task "+sl.Task, gateContent(frozen.Tasks[sl.Task]), gateContent(s.Lock.Tasks[sl.Task])); err != nil {
			return err
		}
		if sl.Condition == schemas.ConditionClean {
			continue
		}
		if err := sameEntry("probe "+sl.Condition, frozen.Probes[sl.Condition], s.Lock.Probes[sl.Condition]); err != nil {
			return err
		}
	}
	return nil
}

// CheckEvaluator makes sure the evaluator the gates read now (the suite's
// settings, and the settings and files of every task and probe the
// experiment's slots use, such as protected paths) is the one the lock
// fingerprinted. A matching lock file alone does not prove it: suite.yaml or
// a task folder can be edited after locking, and a regate would then apply
// the edited rules to the sealed diffs (audit 8-D-01). Image drift is not
// checked: gates read the sealed run folder, never an image. A lock without
// fingerprints (legacy) binds nothing the regate could check, so it is
// refused; re-lock the unchanged suite first.
func CheckEvaluator(ctx context.Context, reg *schemas.Registry, m *schemas.Manifest, s *schemas.Suite) error {
	if s == nil || s.Lock == nil || s.Lock.Fingerprints == nil {
		return fmt.Errorf("suite %s lock records no fingerprints, so the evaluator the gates would read cannot be checked against the frozen one; run `bifrost-bench suite lock %s` on the unchanged suite first", m.Experiment.Suite, m.Experiment.Suite)
	}
	drift, _, err := suite.CheckFingerprints(s, engine.FingerprintOptions(ctx, reg, s))
	if err != nil {
		return fmt.Errorf("check suite %s fingerprints: %w", s.ID, err)
	}
	used := map[string]bool{"suite/" + s.ID: true}
	for _, sl := range m.Slots {
		used["task/"+sl.Task] = true
		if sl.Condition != schemas.ConditionClean {
			used["probe/"+sl.Condition] = true
		}
	}
	var hit []schemas.FingerprintDrift
	for _, d := range drift {
		if d.Part != suite.PartImage && used[d.Scope+"/"+d.ID] {
			hit = append(hit, d)
		}
	}
	if len(hit) > 0 {
		return fmt.Errorf("the evaluator changed since suite %s was locked (%s); its gates cannot be recomputed against the current suite", s.ID, suite.DriftText(hit))
	}
	return nil
}

// gateContent is the part of a task lock entry the gates read: the version
// and the content hashes. Admission and lifecycle status are provenance; a
// later `suite admit` or quarantine does not change what a sealed run is
// graded against, so they stay out of the comparison (audit 8-D-02).
func gateContent(t schemas.TaskLock) schemas.TaskLock {
	t.Admission = nil
	t.Status = ""
	return t
}

// sameEntry compares two lock entries by their JSON form.
func sameEntry(what string, frozen, now any) error {
	a, errA := json.Marshal(frozen)
	b, errB := json.Marshal(now)
	if errA != nil || errB != nil {
		return fmt.Errorf("%s: encode lock entry: %w", what, errors.Join(errA, errB))
	}
	if !bytes.Equal(a, b) {
		return fmt.Errorf("%s changed since the experiment was frozen; its gates cannot be recomputed against the current suite", what)
	}
	return nil
}

// IndexRebuild rebuilds the derived results index (resultsindex) from the
// sealed run folders of st, as `index rebuild` does, then runs each extra
// rebuild (the database index of the hosted platform) in order.
func IndexRebuild(ctx context.Context, st *store.FSStore, logger schemas.Logger, extra ...func(context.Context) error) (*resultsindex.Index, resultsindex.Status, error) {
	ix := resultsindex.New(st, logger)
	status, err := ix.Rebuild(ctx)
	if err != nil {
		return ix, status, err
	}
	for _, f := range extra {
		if f == nil {
			continue
		}
		if err := f(ctx); err != nil {
			return ix, status, err
		}
	}
	return ix, status, nil
}
