package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Limits of the task detail. A visible repo can be large; the page only
// needs to show what the agent starts from.
const (
	// FilesLimit is the most repo files one task detail lists.
	FilesLimit = 500
	// InstructionLimit is the most instruction bytes one task detail serves.
	InstructionLimit = 256 << 10
)

// hiddenTaskDirs are the conventional task sub-folders that hold what the
// agent must never see: hidden tests, the reference solution and the
// admission controls. They are refused even when task.yaml points elsewhere,
// so a task whose repo_dir is "." still never lists them.
var hiddenTaskDirs = []string{"verifier", "solution", "controls"}

// checkSummaryToken matches the grader counts an admission check detail
// carries ("tests_ran=7 failures=0 ..."). Only these tokens are served: the
// rest of the detail can name hidden tests or control files.
var checkSummaryToken = regexp.MustCompile(`^(tests_ran|failures|errors|skipped|exit)=-?[0-9]+$`)

// SuiteSource is the read side of the benchmarks folder. cmd/bifrost-bench
// satisfies it with the suite package's local loader, so the API reads packs
// exactly as `suite lint` and the planner do.
type SuiteSource interface {
	// SuiteIDs lists the pack folders under the benchmarks root, sorted.
	SuiteIDs(ctx context.Context) ([]string, error)
	// LoadSuite loads one pack by id: suite.yaml, its tasks and probes, and
	// suite.lock when present, with absolute Root and Task.Dir.
	LoadSuite(ctx context.Context, id string) (*schemas.Suite, error)
	// LockDrift lists what no longer matches s.Lock: what `suite lock` would
	// change. It is only asked about a suite with a lock.
	LockDrift(ctx context.Context, s *schemas.Suite) ([]schemas.SuiteDrift, error)
	// FingerprintDrift lists what no longer matches the fingerprints s.Lock
	// recorded (docs/lock-fingerprints.md); empty for a legacy lock. It is
	// only asked about a suite whose lock has fingerprints.
	FingerprintDrift(ctx context.Context, s *schemas.Suite) ([]schemas.FingerprintDrift, error)
}

// suiteOrigin is the optional SuiteSource method that reports a pack loaded
// from the server's read-only built-in library (cmd/bifrost-bench
// servesuites.go); GET /suites marks such a pack builtin.
type suiteOrigin interface {
	SuiteBuiltin(id string) bool
}

// isBuiltinSuite reports that src serves suite id from the built-in library.
func isBuiltinSuite(src SuiteSource, id string) bool {
	o, ok := src.(suiteOrigin)
	return ok && id != "" && o.SuiteBuiltin(id)
}

// sendBuiltinReadOnly answers a change to a built-in library suite: 403.
func sendBuiltinReadOnly(ctx *fasthttp.RequestCtx, mode schemas.Mode, id string) {
	lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
		schemas.ErrReadOnly.Error()+": "+id+" is shared by every organization and cannot be edited, re-imported, admitted or deleted here")
}

// refuseBuiltin wraps a write route on /suites/{id}/...: a built-in library
// suite is refused with 403 before next runs, never copied into the org.
func refuseBuiltin(mode schemas.Mode, src SuiteSource, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if id := pathParam(ctx, "id"); isBuiltinSuite(src, id) {
			sendBuiltinReadOnly(ctx, mode, id)
			return
		}
		next(ctx)
	}
}

// SuitesHandler serves the benchmark packs on disk: what each suite holds,
// whether its lock still matches its files, and per task its provenance and
// admission record. It is read-only and never serves a hidden test, a
// reference solution or an admission control.
type SuitesHandler struct {
	mode   schemas.Mode
	suites SuiteSource

	// Suite health area: serve held-out tasks like public ones (suitehealth.go).
	heldOutVisible bool
}

// NewSuitesHandler creates the suites handler. suites may be nil.
func NewSuitesHandler(mode schemas.Mode, suites SuiteSource) *SuitesHandler {
	return &SuitesHandler{mode: mode, suites: suites}
}

// RegisterRoutes registers GET /suites and GET /suites/{id}/tasks/{task}.
func (h *SuitesHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/suites", lib.ChainMiddlewares(h.getSuites, middlewares...))
	r.GET(APIPrefix+"/suites/{id}/tasks/{task}", lib.ChainMiddlewares(h.getSuiteTask, middlewares...))
}

// getSuites handles GET /api/bench/suites. A pack that fails to load is
// listed with its error, never dropped and never failing the list.
func (h *SuitesHandler) getSuites(ctx *fasthttp.RequestCtx) {
	if h.suites == nil {
		sendUnavailable(ctx, h.mode, "suite loader")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	list, err := h.ListSummaries(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list suites", err)
		return
	}
	out := schemas.SuiteList{Suites: list}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// ListSummaries is the GET /suites list: every pack with its summary, built-in
// library packs marked. Evaluation packs (bench/packs) read ready task counts
// from it.
func (h *SuitesHandler) ListSummaries(work context.Context) ([]schemas.SuiteSummary, error) {
	if h.suites == nil {
		return nil, errors.New("suite loader is not available")
	}
	ids, err := h.suites.SuiteIDs(work)
	if err != nil {
		return nil, err
	}
	out := schemas.SuiteList{Suites: make([]schemas.SuiteSummary, len(ids))}
	origin, _ := h.suites.(suiteOrigin)
	if _, cached := h.suites.(suiteStamper); !cached {
		// No cache side: load each pack in the request, as before.
		for i, id := range ids {
			if s, err := h.suites.LoadSuite(work, id); err != nil {
				out.Suites[i] = brokenSuite(id, err)
			} else {
				out.Suites[i] = h.summarize(work, s)
			}
		}
	} else {
		// Every pack has its own budget (suitecache.go); the waits run side
		// by side and the loads behind them are bounded by the cache.
		var wg sync.WaitGroup
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sum, ok := h.cachedSummary(id, suiteListBudget)
				if !ok {
					sum = headSummary(h.suites, id)
				}
				out.Suites[i] = sum
			}()
		}
		wg.Wait()
	}
	for i, id := range ids {
		if origin != nil && origin.SuiteBuiltin(id) {
			out.Suites[i].Builtin, out.Suites[i].Source = true, "library"
		}
	}
	return out.Suites, nil
}

// getSuiteTask handles GET /api/bench/suites/{id}/tasks/{task}: one task with
// its instruction text and visible file list.
func (h *SuitesHandler) getSuiteTask(ctx *fasthttp.RequestCtx) {
	if h.suites == nil {
		sendUnavailable(ctx, h.mode, "suite loader")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return
	}
	taskID, ok := idParam(ctx, h.mode, "task", "task")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ids, err := h.suites.SuiteIDs(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list suites", err)
		return
	}
	if !slices.Contains(ids, id) {
		sendNotFound(ctx, h.mode, "suite", id)
		return
	}
	s, err := h.suites.LoadSuite(work, id)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "load suite "+id+": "+err.Error())
		return
	}
	t := s.TaskByID(taskID)
	if t == nil {
		sendNotFound(ctx, h.mode, "task", id+"/"+taskID)
		return
	}
	if h.refuseHeldOut(ctx, id, t) { // Suite health area: held-out tasks are not served (suitehealth.go)
		return
	}
	sum, ok := h.cachedSummary(id, suiteListBudget)
	if !ok || !hasTask(sum, taskID) {
		// No cached summary: this task's entry without lock drift, so a large
		// pack is never rehashed per request (suitecache.go).
		sum = h.taskOnlySummary(s, t)
	}
	detail, err := taskDetail(s, t, sum)
	if err != nil {
		sendDepError(ctx, h.mode, "task "+id+"/"+taskID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, detail)
}

// hasTask reports that sum lists task id.
func hasTask(sum schemas.SuiteSummary, id string) bool {
	for _, st := range sum.Tasks {
		if st.ID == id {
			return true
		}
	}
	return false
}

// taskOnlySummary is the summary of suite s holding task t alone, without
// lock drift: what the task detail needs when no full summary is cached.
func (h *SuitesHandler) taskOnlySummary(s *schemas.Suite, t *schemas.Task) schemas.SuiteSummary {
	out := schemas.SuiteSummary{ID: s.ID, Version: s.Version, VerifierResult: verifierResult(s.Verifier)}
	out.Tasks = []schemas.SuiteTask{summarizeTask(s, t, nil)}
	one := *s // shallow copy: the loaded suite may be shared (library cache)
	one.Tasks = []*schemas.Task{t}
	scenarioSummary(&one, &out)
	h.healthSummary(&one, &out)
	return out
}

// summarize builds a loaded suite's summary: lock state, drift, probes and
// every task with its admission record.
func (h *SuitesHandler) summarize(ctx context.Context, s *schemas.Suite) schemas.SuiteSummary {
	out := schemas.SuiteSummary{
		ID: s.ID, Version: s.Version, Description: s.Description,
		Environment: s.Environment, Grader: s.Grader,
		VerifierResult:   verifierResult(s.Verifier),
		VerifierTimeoutS: s.Verifier.TimeoutS,
		PassThreshold:    passThreshold(s.Verifier),
		BaseImage:        schemas.SuiteImage{Tag: s.BaseImage.Tag, Digest: s.BaseImage.Digest},
		LockHash:         s.LockHash,
		LockState:        schemas.SuiteUnlocked,
		Drift:            []schemas.SuiteDrift{},
		Probes:           make([]schemas.SuiteProbe, 0, len(s.Probes)),
		Tasks:            make([]schemas.SuiteTask, 0, len(s.Tasks)),
		TaskCount:        len(s.Tasks),
	}
	if s.Lock != nil {
		at := s.Lock.CreatedAt
		out.LockedAt = &at
		if out.BaseImage.Digest == "" {
			out.BaseImage.Digest = s.Lock.BaseImage.Digest
		}
		drift, err := h.suites.LockDrift(ctx, s)
		switch {
		case err != nil:
			out.LockState, out.LockError = schemas.SuiteLockErr, err.Error()
		case len(drift) > 0:
			out.LockState, out.Drift = schemas.SuiteStale, drift
		default:
			out.LockState = schemas.SuiteLocked
		}
		h.fingerprintSummary(ctx, s, &out)
	}
	for _, p := range s.Probes {
		out.Probes = append(out.Probes, schemas.SuiteProbe{
			ID: p.ID, Version: p.Version, Layer: string(p.Layer), PS2Family: string(p.PS2Family),
			Axis: p.Axis, GoalPreserving: p.GoalPreserving, SafeBehavior: p.SafeBehavior,
		})
	}
	stale := staleTasks(out.Drift)
	for _, t := range s.Tasks {
		st := summarizeTask(s, t, stale)
		if st.Admitted {
			out.AdmittedCount++
		}
		out.Tasks = append(out.Tasks, st)
	}
	scenarioSummary(s, &out) // UI area: scenario fields of the suite and its tasks (imports.go)
	h.healthSummary(s, &out) // Suite health area: quarantine, held-out split, canary (suitehealth.go)
	// U3: tiers (suitetiers.go).
	out.Tiers = suiteTiers(s)
	return out
}

// fingerprintSummary sets the lock kind and the fingerprint drift of a
// locked suite (docs/lock-fingerprints.md). Fingerprint drift refuses
// planning, so a lock that was otherwise current reads as stale.
func (h *SuitesHandler) fingerprintSummary(ctx context.Context, s *schemas.Suite, out *schemas.SuiteSummary) {
	if s.Lock.Fingerprints == nil {
		out.LockKind = schemas.LockKindLegacy
		return
	}
	out.LockKind = schemas.LockKindFingerprinted
	drift, err := h.suites.FingerprintDrift(ctx, s)
	switch {
	case err != nil:
		if out.LockState != schemas.SuiteLockErr {
			out.LockState, out.LockError = schemas.SuiteLockErr, err.Error()
		}
	case len(drift) > 0:
		out.FingerprintDrift = drift
		if out.LockState == schemas.SuiteLocked {
			out.LockState = schemas.SuiteStale
		}
	}
}

// brokenSuite is the list entry of a pack that failed to load.
func brokenSuite(id string, err error) schemas.SuiteSummary {
	return schemas.SuiteSummary{
		ID: id, LockState: schemas.SuiteLockErr, Drift: []schemas.SuiteDrift{},
		Probes: []schemas.SuiteProbe{}, Tasks: []schemas.SuiteTask{}, Error: err.Error(),
	}
}

// staleTasks returns the tasks whose admission no longer holds because their
// content changed since the lock: what `suite lock` would send back to draft.
// A changed base image clears every admission, so it marks every task ("*").
// A version-only change (PATCH) keeps the admission, as Lock does.
func staleTasks(drift []schemas.SuiteDrift) map[string]bool {
	out := map[string]bool{}
	for _, d := range drift {
		switch {
		case d.Scope == "suite":
			out["*"] = true
		case d.Scope == "task" && d.Level != "PATCH" && d.Action != "added" && d.Action != "removed":
			out[d.ID] = true
		}
	}
	return out
}

// summarizeTask builds one task's list entry from task.yaml and its lock
// entry. Hidden tests are counted, never named.
func summarizeTask(s *schemas.Suite, t *schemas.Task, stale map[string]bool) schemas.SuiteTask {
	out := schemas.SuiteTask{
		ID: t.ID, Version: t.Version, Title: t.Title, Project: t.Project, Kind: string(t.Kind),
		Difficulty:     t.Difficulty,
		Capabilities:   nonNil(t.Capabilities),
		Tags:           nonNil(t.Tags),
		Status:         schemas.TaskNotLocked,
		Stale:          stale["*"] || stale[t.ID],
		Checks:         []schemas.SuiteAdmissionCheck{},
		RequiredTests:  len(t.Verifier.Required),
		ProtectedGlobs: len(t.Protected),
		Mutants:        len(t.Controls.Mutants),
		AgentTimeoutS:  t.Timeouts.AgentS,
	}
	if t.Provenance != nil {
		p := *t.Provenance
		out.Provenance = &p
	}
	if n, err := countFiles(t.Dir, t.Verifier.Tests); err == nil {
		out.HiddenTestFiles = n
	}
	if s.Lock == nil {
		return out
	}
	tl, ok := s.Lock.Tasks[t.ID]
	if !ok {
		return out
	}
	out.Status = string(tl.Status)
	if rec := tl.Admission; rec != nil {
		at := rec.At
		out.AdmittedAt = &at
		for _, c := range rec.Checks {
			out.Checks = append(out.Checks, schemas.SuiteAdmissionCheck{
				Name: c.Name, Control: controlOf(c.Name), Want: c.Want, Got: c.Got, Passed: c.Passed,
				Summary: checkSummary(c.Detail),
			})
		}
		out.Admitted = tl.Status == schemas.TaskAdmitted && rec.Admitted && !out.Stale
	}
	return out
}

// taskDetail adds what the agent sees to a task's list entry: the
// instruction text and the visible repo files. Both are read through
// visiblePath, which refuses anything inside a hidden folder.
func taskDetail(s *schemas.Suite, t *schemas.Task, sum schemas.SuiteSummary) (*schemas.SuiteTaskDetail, error) {
	out := &schemas.SuiteTaskDetail{
		Suite: s.ID, SuiteVersion: s.Version, VerifierResult: sum.VerifierResult,
		Protected: nonNil(t.Protected), Files: []schemas.SuiteRepoFile{},
	}
	for _, st := range sum.Tasks {
		if st.ID == t.ID {
			out.Task = st
		}
	}
	hidden := hiddenRoots(t)
	instr, err := visiblePath(t.Dir, t.Instruction, hidden)
	if err != nil {
		return nil, fmt.Errorf("instruction: %w", err)
	}
	text, truncated, err := readCapped(instr, InstructionLimit)
	if err != nil {
		return nil, fmt.Errorf("instruction: %w", err)
	}
	out.Instruction, out.InstructionTruncated = text, truncated
	repo, err := visiblePath(t.Dir, t.RepoDir, hidden)
	if err != nil {
		return nil, fmt.Errorf("repo_dir: %w", err)
	}
	files, err := listVisibleFiles(repo, hidden)
	if err != nil {
		return nil, fmt.Errorf("repo_dir: %w", err)
	}
	out.FilesTotal = len(files)
	if len(files) > FilesLimit {
		files, out.FilesTruncated = files[:FilesLimit], true
	}
	out.Files = files
	return out, nil
}

// verifierResult is the suite's result format, junit when unset.
func verifierResult(v schemas.SuiteVerifier) string {
	if v.Result == "" {
		return schemas.VerifierResultJUnit
	}
	return v.Result
}

// passThreshold is the lowest passing reward of a reward suite (default 1),
// nil for the other formats.
func passThreshold(v schemas.SuiteVerifier) *float64 {
	if verifierResult(v) != schemas.VerifierResultReward {
		return nil
	}
	th := v.PassThreshold
	if th == 0 {
		th = 1
	}
	return &th
}

// controlOf maps an admission check name to its control family. The stale
// notes check is matched first: its name also starts with "reference".
func controlOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "stale_notes"):
		return schemas.ControlStaleNotes
	case strings.HasPrefix(n, "reference"):
		return schemas.ControlReference
	case strings.HasPrefix(n, "empty"):
		return schemas.ControlEmpty
	case strings.HasPrefix(n, "mutant"):
		return schemas.ControlMutant
	case strings.HasPrefix(n, "exploit"):
		return schemas.ControlExploit
	case strings.HasPrefix(n, "null"), strings.HasPrefix(n, "noop"), strings.HasPrefix(n, "refuse"):
		return schemas.ControlNullAgent
	case strings.Contains(n, "secret"):
		return schemas.ControlSecrets
	case strings.Contains(n, "network"):
		return schemas.ControlNetwork
	default:
		return schemas.ControlOther
	}
}

// checkSummary keeps only the grader counts of an admission check detail,
// in their original order.
func checkSummary(detail string) string {
	var keep []string
	for _, field := range strings.FieldsFunc(detail, func(r rune) bool { return r == ' ' || r == ';' || r == ',' }) {
		if checkSummaryToken.MatchString(field) {
			keep = append(keep, field)
		}
	}
	return strings.Join(keep, " ")
}

// hiddenRoots lists the absolute paths, symlinks resolved, of everything in
// a task the agent must not see: the hidden tests, every control, and the
// conventional hidden folders.
func hiddenRoots(t *schemas.Task) []string {
	rels := []string{t.Verifier.Tests, t.Controls.Reference, t.Controls.Empty, t.Controls.Exploit}
	rels = append(rels, t.Controls.Mutants...)
	rels = append(rels, hiddenTaskDirs...)
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		if rel == "" {
			continue
		}
		abs := filepath.Join(t.Dir, filepath.FromSlash(rel))
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		out = append(out, abs)
	}
	return out
}

// visiblePath resolves rel inside dir, following symlinks, and refuses a
// path that leaves dir, is a hidden root, or lies inside one.
func visiblePath(dir, rel string, hidden []string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q: want a path relative to the task folder", rel)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve task folder: %w", err)
	}
	real, err := filepath.EvalSymlinks(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, schemas.ErrNotFound)
	}
	if !within(realDir, real) {
		return "", fmt.Errorf("%s: path leaves the task folder", rel)
	}
	if isHidden(real, hidden) {
		return "", fmt.Errorf("%s: path is hidden from the agent", rel)
	}
	return real, nil
}

// listVisibleFiles walks root without following symlinks and returns its
// regular files, sorted, minus anything inside a hidden root and any .git
// folder. Sizes come from the directory entries; no file is opened.
func listVisibleFiles(root string, hidden []string) ([]schemas.SuiteRepoFile, error) {
	var out []schemas.SuiteRepoFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if isHidden(path, hidden) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, schemas.SuiteRepoFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// countFiles counts the regular files under rel inside dir, for the hidden
// test count. It never reads them.
func countFiles(dir, rel string) (int, error) {
	if rel == "" {
		return 0, errors.New("empty path")
	}
	n := 0
	err := filepath.WalkDir(filepath.Join(dir, filepath.FromSlash(rel)), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n, err
}

// readCapped reads at most limit bytes of a file as valid UTF-8 text and
// reports whether the file was longer.
func readCapped(path string, limit int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(buf) > limit
	if truncated {
		buf = buf[:limit]
		// Drop a multi-byte rune the cut split in half (at most UTFMax-1 bytes).
		for i := 0; i < utf8.UTFMax-1 && len(buf) > 0; i++ {
			if r, size := utf8.DecodeLastRune(buf); r != utf8.RuneError || size > 1 {
				break
			}
			buf = buf[:len(buf)-1]
		}
	}
	return strings.ToValidUTF8(string(buf), "�"), truncated, nil
}

// isHidden reports whether path is a hidden root or inside one.
func isHidden(path string, hidden []string) bool {
	for _, h := range hidden {
		if within(h, path) {
			return true
		}
	}
	return false
}

// within reports whether path is base or inside it.
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// nonNil returns s, or an empty slice for nil, so the JSON is [] not null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
