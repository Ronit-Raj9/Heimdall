package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("regate", "recompute the gates of an experiment's sealed runs at the current gate versions, as a regrade; nothing runs again", runRegate)
}

// maxChangedRows caps the changed-outcome rows a report lists.
const maxChangedRows = 40

func runRegate(args []string) int {
	fs := flag.NewFlagSet("regate", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id whose sealed runs are regated")
	id := fs.String("id", "", "regrade id to write (default: a new rg_ id)")
	note := fs.String("note", "", "why the gates are recomputed; every report that uses this regrade shows it")
	frozenLock := fs.String("suite-lock", "", "the suite.lock the experiment was frozen with, needed when the suite's lock changed since; every task and probe entry the experiment uses must still match")
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench regate [-config deploy/bench.yaml] -exp <exp_id> [-id <regrade_id>] [-note text] [-suite-lock frozen/suite.lock]")
		fmt.Fprintln(os.Stderr, "then: bifrost-bench check -exp <exp_id> -grade-source <regrade_id>")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	regradeID := *id
	if regradeID == "" {
		regradeID = utils.NewID("rg", 8)
	}
	info, before, after, err := a.regate(ctx, *exp, regradeID, *note, *frozenLock)
	if err != nil {
		return fail("regate %s: %v", *exp, err)
	}
	fmt.Print(gateSourceTables(before, after, info.GateVersions))
	if info.SuiteLockHash != "" {
		fmt.Printf("\n%s\n", suiteDriftText(info.SuiteLockHash, before))
	}
	fmt.Printf("\nregrade %s written to %s/regrades/%s\nanalyze with: bifrost-bench check -exp %s -grade-source %s\n",
		regradeID, *exp, regradeID, *exp, regradeID)
	return exitOK
}

// regate recomputes the gates of every counted sealed run of expID and writes
// them as regrade regradeID. Every run's seal is verified, and a run with an
// evidence problem (a seal that does not verify, a slot with more than one
// counted run) refuses the regate; every regate is computed before anything
// is written, so a failure leaves no partial regrade. It returns the regrade record and the runs as sealed and as
// regated. frozenLock is the experiment's suite.lock when the suite's lock has
// changed since the freeze (see checkSuiteDrift).
func (a *app) regate(ctx context.Context, expID, regradeID, note, frozenLock string) (*store.RegradeInfo, []schemas.SealedRun, []schemas.SealedRun, error) {
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return nil, nil, nil, err
	}
	suite, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := checkSuiteDrift(m, suite, frozenLock); err != nil {
		return nil, nil, nil, err
	}
	runs, err := a.store.SealedRuns(ctx, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load sealed runs: %w", err)
	}
	results := make([]*store.GateRegrade, len(runs))
	for i := range runs {
		sr := &runs[i]
		if len(sr.Problems) > 0 {
			return nil, nil, nil, fmt.Errorf("run %s: %s", sr.Run.RunID, strings.Join(sr.Problems, "; "))
		}
		if err := a.store.Verify(ctx, expID, sr.Run.RunID); err != nil {
			return nil, nil, nil, fmt.Errorf("run %s: seal does not verify: %w", sr.Run.RunID, err)
		}
		r, err := engine.Regate(ctx, a.reg, m, suite, sr)
		if err != nil {
			return nil, nil, nil, err
		}
		results[i] = &store.GateRegrade{Gates: r.Gates, Recomputed: r.Recomputed, SealedOutcome: sr.Run.Outcome,
			Outcome: r.Outcome, Y: r.Y, Metrics: r.Metrics, Notes: r.Notes}
		for _, n := range r.Notes {
			a.logger.Warn("regate note", "run_id", sr.Run.RunID, "note", n)
		}
	}
	info := store.RegradeInfo{SuiteVersion: m.SuiteVersion, GateVersions: engine.RegateVersions(a.reg, m), Note: note}
	if suite.LockHash != m.SuiteLockHash {
		info.SuiteLockHash = suite.LockHash
	}
	for i := range runs {
		if err := a.store.WriteGateRegrade(ctx, expID, regradeID, runs[i].Run.RunID, info, results[i]); err != nil {
			return nil, nil, nil, err
		}
	}
	after, err := a.store.SealedRuns(ctx, expID, regradeID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load regated runs: %w", err)
	}
	return &info, runs, after, nil
}

// checkSuiteDrift makes sure the suite the gates read now is the one the
// experiment froze. With the same lock hash it is. When the lock changed (a
// probe or task added later), the caller must hand in the frozen suite.lock:
// its sha256 must be the manifest's suite_lock_hash, and the entry of every
// task and probe condition the experiment's slots use must be the same in
// both locks, so the protected paths and the tests the gates compare against
// did not move.
func checkSuiteDrift(m *schemas.Manifest, suite *schemas.Suite, frozenLock string) error {
	if lockMatches(suite, m.SuiteLockHash) { // Lock fingerprints: a lock that only gained fingerprints still matches (lockfingerprints.go)
		return nil
	}
	if frozenLock == "" {
		return fmt.Errorf("suite %s lock hash %s differs from the manifest's %s; pass the frozen lock with -suite-lock to check the tasks it uses",
			suite.ID, short(suite.LockHash), short(m.SuiteLockHash))
	}
	if suite.Lock == nil {
		return fmt.Errorf("suite %s has no lock to compare", suite.ID)
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
		if err := sameEntry("task "+sl.Task, frozen.Tasks[sl.Task], suite.Lock.Tasks[sl.Task]); err != nil {
			return err
		}
		if sl.Condition == schemas.ConditionClean {
			continue
		}
		if err := sameEntry("probe "+sl.Condition, frozen.Probes[sl.Condition], suite.Lock.Probes[sl.Condition]); err != nil {
			return err
		}
	}
	return nil
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

// gateSourceSection is the report section that says the gates were
// recomputed after the fact, or "" when gradeSource recomputed none. It is
// built here rather than by the markdown reporter because it reads the
// regrade record and the sealed gates, which the analysis does not carry.
func (a *app) gateSourceSection(ctx context.Context, m *schemas.Manifest, gradeSource string, regated []schemas.SealedRun) (string, error) {
	if gradeSource == "" || gradeSource == schemas.GradeSourceOriginal || gradeSource == schemas.GradeSourceReviewed { // reviewed is not a regrade (review.go)
		return "", nil
	}
	expID := m.ExpID
	info, err := a.store.RegradeInfo(ctx, expID, gradeSource)
	if err != nil {
		return "", err
	}
	if len(info.GateVersions) == 0 {
		return "", nil
	}
	sealed, err := a.store.SealedRuns(ctx, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return "", fmt.Errorf("load sealed runs: %w", err)
	}
	return gateSourceMarkdown(info, m.SuiteLockHash, sealed, regated), nil
}

// gateSourceMarkdown renders the disclosure for a regate: what was
// recomputed, from what, and that the result is post hoc.
func gateSourceMarkdown(info *store.RegradeInfo, frozenLock string, sealed, regated []schemas.SealedRun) string {
	var b strings.Builder
	b.WriteString("## Gate source\n\n")
	fmt.Fprintf(&b, "Gates recomputed after the fact by regrade `%s` (created %s) from each sealed run's own sealed evidence "+
		"(workspace.diff, evidence.json, grade.json). No agent, model or grader ran again. The sealed gates.json files are unchanged "+
		"and analyses that used them stay as they were. Gates whose version did not change were carried over from the seal.\n\n",
		info.RegradeID, info.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	b.WriteString("**Post hoc.** The gate rule changed after these results were seen. Read this verdict beside the analysis on the " +
		"sealed gates, not instead of it, and confirm the new rule on a fresh batch before relying on it.\n\n")
	if info.Note != "" {
		fmt.Fprintf(&b, "Why: %s\n\n", info.Note)
	}
	if info.SuiteLockHash != "" && info.SuiteLockHash != frozenLock {
		fmt.Fprintf(&b, "%s The frozen lock is `%s`.\n\n", suiteDriftText(info.SuiteLockHash, sealed), short(frozenLock))
	}
	b.WriteString(gateSourceTables(sealed, regated, info.GateVersions))
	return b.String()
}

// suiteDriftText says the regate read a newer suite lock whose entries for
// the experiment's tasks and probes match the frozen one.
func suiteDriftText(lockNow string, runs []schemas.SealedRun) string {
	tasks := map[string]bool{}
	for _, r := range runs {
		tasks[r.Slot.Task] = true
	}
	return fmt.Sprintf("The suite lock changed after the freeze (now `%s`); the lock entries of the %d tasks and every probe condition the experiment uses were checked equal to the frozen lock before regating.",
		short(lockNow), len(tasks))
}

// gateSourceTables renders per-gate versions and fired counts before and
// after, and the runs whose outcome changed. versions are the gate versions
// the regate ran at; a result at another version was carried from the seal.
func gateSourceTables(sealed, regated []schemas.SealedRun, versions map[string]string) string {
	var b strings.Builder
	b.WriteString("| Gate | Effect | Sealed version | Regate version | Fired, sealed | Fired, regated | Results carried at another version |\n|---|---|---|---|---|---|---|\n")
	before, after := gateTally(sealed), gateTally(regated)
	for _, name := range gateOrder(sealed, regated) {
		x, y := before[name], after[name]
		want, carried := versions[name], 0
		for v, n := range y.versions {
			if v != want {
				carried += n
			}
		}
		if want == "" {
			want = "-"
		}
		effect := y.effect
		if effect == "" {
			effect = x.effect
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d | %d |\n", name, effect, x.versionList(), want, x.fired, y.fired, carried)
	}
	var rows []string
	moves := map[string]int{}
	for i := range regated {
		s, r := sealed[i], regated[i]
		if s.Run.RunID != r.Run.RunID || s.Run.Outcome == r.Run.Outcome {
			continue
		}
		moves[string(s.Run.Outcome)+" to "+string(r.Run.Outcome)]++
		rows = append(rows, fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s |", r.Run.RunID, r.Slot.Arm, r.Slot.Task, r.Slot.Condition, s.Run.Outcome, r.Run.Outcome))
	}
	fmt.Fprintf(&b, "\nOutcomes changed in %d of %d counted runs", len(rows), len(regated))
	if len(rows) == 0 {
		b.WriteString(".\n")
		return b.String()
	}
	keys := make([]string, 0, len(moves))
	for k := range moves {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %d", k, moves[k])
	}
	fmt.Fprintf(&b, " (%s).\n\n| Run | Arm | Task | Condition | Sealed outcome | Recomputed outcome |\n|---|---|---|---|---|---|\n", strings.Join(parts, ", "))
	for i, r := range rows {
		if i == maxChangedRows {
			fmt.Fprintf(&b, "| and %d more | | | | | |\n", len(rows)-maxChangedRows)
			break
		}
		b.WriteString(r + "\n")
	}
	return b.String()
}

// gateCount is one gate's results over a set of runs.
type gateCount struct {
	effect   schemas.GateEffect
	fired    int
	versions map[string]int
}

// versionList returns the versions seen, sorted, joined with commas.
func (c gateCount) versionList() string {
	vs := make([]string, 0, len(c.versions))
	for v := range c.versions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	if len(vs) == 0 {
		return "-"
	}
	return strings.Join(vs, ", ")
}

// gateTally counts each gate's results and fired results over runs.
func gateTally(runs []schemas.SealedRun) map[string]gateCount {
	out := map[string]gateCount{}
	for _, r := range runs {
		for _, g := range r.Gates {
			c := out[g.Gate]
			if c.versions == nil {
				c.versions = map[string]int{}
			}
			c.effect = g.Effect
			c.versions[g.Version]++
			if g.Fired {
				c.fired++
			}
			out[g.Gate] = c
		}
	}
	return out
}

// gateOrder lists gate names in the order they first appear.
func gateOrder(sets ...[]schemas.SealedRun) []string {
	seen := map[string]bool{}
	var out []string
	for _, runs := range sets {
		for _, r := range runs {
			for _, g := range r.Gates {
				if !seen[g.Gate] {
					seen[g.Gate] = true
					out = append(out, g.Gate)
				}
			}
		}
	}
	return out
}

// insertSection puts sec before the report's first second-level heading, so
// it sits right under the header that names the grade source.
func insertSection(md, sec string) string {
	if sec == "" {
		return md
	}
	if i := strings.Index(md, "\n## "); i >= 0 {
		return md[:i+1] + sec + "\n" + md[i+1:]
	}
	return md + "\n" + sec
}
