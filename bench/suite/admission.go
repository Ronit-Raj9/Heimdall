package suite

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Admission parameters, matching tools/checktask.sh.
const (
	referenceRuns     = 3
	exploitScriptPath = "/tmp/bench/exploit.sh"
	exploitWallS      = 120
	exploitUser       = "1000"
	destroyTimeout    = 2 * time.Minute
)

// Admission check outcomes, used as AdmissionCheck.Want and Got.
const (
	GotPass             = "pass"
	GotFail             = "fail"
	GotInvalidEvaluator = "invalid_evaluator"
	GotError            = "error"
)

// staleNotesFiles are where the stale notes are overlaid for the goal-preserving
// check: both project notes names an agent may read, as checktask.sh does.
var staleNotesFiles = []string{"CLAUDE.md", "AGENTS.md"}

// AdmitDeps are the parts admission runs the controls through. Logger may be
// nil.
type AdmitDeps struct {
	Env    schemas.Environment
	Grader schemas.Grader
	Logger schemas.Logger
}

// Admit runs the admission controls of one task version, the same checks as
// tools/checktask.sh: the reference passes 3 of 3, the empty patch fails,
// every mutant fails, the exploit script fails under clean grading, and the
// reference still passes with the stale notes overlaid (the stale-notes probe
// is goal-preserving). Patches are graded directly by deps.Grader; the exploit
// runs in a workspace from deps.Env and its exported diff is graded. A failed
// control is a failed check in the record, not an error; Admit returns an
// error only when it cannot run the checks at all.
func Admit(ctx context.Context, deps AdmitDeps, s *schemas.Suite, t *schemas.Task) (*schemas.AdmissionRecord, error) {
	if deps.Env == nil || deps.Grader == nil {
		return nil, errors.New("admit: environment and grader are required")
	}
	if deps.Logger == nil {
		deps.Logger = schemas.NoopLogger{}
	}
	tmp, err := os.MkdirTemp("", "bench-admit-"+t.ID+"-")
	if err != nil {
		return nil, fmt.Errorf("admit task %s: %w", t.ID, err)
	}
	defer os.RemoveAll(tmp)
	a := &admitter{deps: deps, s: s, t: t, tmp: tmp, expID: utils.NewID("e", 10)}

	paths, err := a.controlPaths()
	if err != nil {
		return nil, err
	}
	var checks []schemas.AdmissionCheck
	for i := 1; i <= referenceRuns; i++ {
		checks = append(checks, a.gradePatch(ctx, fmt.Sprintf("reference_%d", i), paths.reference, GotPass))
	}
	checks = append(checks, a.gradePatch(ctx, "empty_patch", paths.empty, GotFail))
	for i, m := range paths.mutants {
		checks = append(checks, a.gradePatch(ctx, fmt.Sprintf("mutant_%d", i+1), m, GotFail))
	}
	checks = append(checks, a.exploit(ctx, paths.exploit))
	staleCheck, err := a.staleNotes(ctx, paths.reference)
	if err != nil {
		return nil, err
	}
	checks = append(checks, staleCheck)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("admit task %s: %w", t.ID, err)
	}

	rec := &schemas.AdmissionRecord{Admitted: true, Checks: checks, At: time.Now().UTC()}
	for _, c := range checks {
		if !c.Passed {
			rec.Admitted = false
		}
	}
	deps.Logger.Info("task admission", "suite", s.ID, "task", t.ID, "version", t.Version, "admitted", rec.Admitted)
	return rec, nil
}

// RecordAdmission stores rec in the lock entry of taskID and sets its status:
// admitted when every check passed, draft otherwise. Call it on a lock made by
// Lock from the same loaded suite Admit checked, so the record pins exactly
// the hashes that were admitted.
func RecordAdmission(l *schemas.SuiteLock, taskID string, rec *schemas.AdmissionRecord) error {
	tl, ok := l.Tasks[taskID]
	if !ok {
		return fmt.Errorf("record admission: task %s is not in the lock", taskID)
	}
	tl.Admission = rec
	tl.Status = schemas.TaskDraft
	if rec != nil && rec.Admitted {
		tl.Status = schemas.TaskAdmitted
	}
	l.Tasks[taskID] = tl
	return nil
}

// admitter carries the state shared by one task's admission checks.
type admitter struct {
	deps  AdmitDeps
	s     *schemas.Suite
	t     *schemas.Task
	tmp   string // removed when Admit returns
	expID string // synthetic experiment id grouping this admission's runs
}

type controlPaths struct {
	reference, empty, exploit string
	mutants                   []string
}

func (a *admitter) controlPaths() (controlPaths, error) {
	var p controlPaths
	var err error
	resolve := func(field, rel string) string {
		if err != nil {
			return ""
		}
		var abs string
		abs, err = resolveIn(a.t.Dir, rel)
		if err != nil {
			err = fmt.Errorf("admit task %s: %s: %w", a.t.ID, field, err)
		}
		return abs
	}
	p.reference = resolve("controls.reference", a.t.Controls.Reference)
	p.empty = resolve("controls.empty", a.t.Controls.Empty)
	p.exploit = resolve("controls.exploit", a.t.Controls.Exploit)
	for i, m := range a.t.Controls.Mutants {
		p.mutants = append(p.mutants, resolve(fmt.Sprintf("controls.mutants[%d]", i), m))
	}
	return p, err
}

// gradePatch grades one control patch file and compares the result to want.
func (a *admitter) gradePatch(ctx context.Context, name, patch, want string) schemas.AdmissionCheck {
	check := schemas.AdmissionCheck{Name: name, Want: want}
	rc, err := a.runContext(name)
	if err != nil {
		return failed(check, err)
	}
	diff, err := patchDiff(patch)
	if err != nil {
		return failed(check, err)
	}
	return a.grade(ctx, rc, diff, check, rel(a.t.Dir, patch))
}

// exploit runs the exploit script in a fresh workspace, exports the diff it
// left and grades that diff cleanly. The workspace is always destroyed.
func (a *admitter) exploit(ctx context.Context, script string) (check schemas.AdmissionCheck) {
	check = schemas.AdmissionCheck{Name: "exploit", Want: GotFail}
	content, err := os.ReadFile(script)
	if err != nil {
		return failed(check, fmt.Errorf("read exploit: %w", err))
	}
	rc, err := a.runContext("exploit")
	if err != nil {
		return failed(check, err)
	}
	rc.Agent = &schemas.AgentConfig{
		ID:       "admission-exploit",
		Adapter:  "scripted",
		Image:    a.s.BaseImage.Tag,
		Notes:    schemas.AgentNotes{ProjectFile: "AGENTS.md"},
		Budgets:  schemas.Budgets{WallS: exploitWallS, CPUs: 1, MemGB: 1},
		Scripted: &schemas.ScriptedSpec{Kind: "exploit"},
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), destroyTimeout)
		defer cancel()
		if err := a.deps.Env.Destroy(dctx, rc); err != nil {
			a.deps.Logger.Warn("admission: destroy exploit workspace", "task", a.t.ID, "run", rc.Run.RunID, "error", err)
			check.Detail = joinDetail(check.Detail, "destroy: "+err.Error())
		}
	}()
	handle, err := a.deps.Env.Create(ctx, rc)
	if err != nil {
		return failed(check, fmt.Errorf("create workspace: %w", err))
	}
	rc.Handle = handle
	plan := &schemas.RunPlan{
		Image:   a.s.BaseImage.Tag,
		Argv:    []string{"sh", exploitScriptPath},
		Env:     map[string]string{},
		Files:   map[string]string{exploitScriptPath: string(content)},
		Workdir: a.s.Workdir,
		WallS:   exploitWallS,
		CPUs:    1,
		MemGB:   1,
		Network: "none",
		User:    exploitUser,
	}
	res, err := a.deps.Env.Exec(ctx, rc, plan)
	if err != nil {
		return failed(check, fmt.Errorf("run exploit: %w", err))
	}
	rc.Exec = res
	diff, err := a.deps.Env.ExportDiff(ctx, rc)
	if err != nil {
		return failed(check, fmt.Errorf("export diff: %w", err))
	}
	detail := fmt.Sprintf("exploit exit %d", res.ExitCode)
	if res.TimedOut {
		detail += " (timed out)"
	}
	return a.grade(ctx, rc, diff, check, detail)
}

// staleNotes grades the reference patch combined with new files that hold the
// stale notes, which is the workspace an agent sees under the stale-notes
// probe after a correct fix.
func (a *admitter) staleNotes(ctx context.Context, reference string) (schemas.AdmissionCheck, error) {
	check := schemas.AdmissionCheck{Name: "reference_stale_notes", Want: GotPass}
	if a.t.Notes.Stale == "" {
		return failed(check, errors.New("task has no stale notes")), nil
	}
	notesPath, err := resolveIn(a.t.Dir, a.t.Notes.Stale)
	if err != nil {
		return check, fmt.Errorf("admit task %s: notes.stale: %w", a.t.ID, err)
	}
	notes, err := os.ReadFile(notesPath)
	if err != nil {
		return check, fmt.Errorf("admit task %s: read stale notes: %w", a.t.ID, err)
	}
	ref, err := os.ReadFile(reference)
	if err != nil {
		return check, fmt.Errorf("admit task %s: read reference: %w", a.t.ID, err)
	}
	combined := withNewFiles(ref, notes, staleNotesFiles)
	path := filepath.Join(a.tmp, "reference-stale-notes.diff")
	if err := os.WriteFile(path, combined, 0o644); err != nil {
		return check, fmt.Errorf("admit task %s: write combined patch: %w", a.t.ID, err)
	}
	rc, err := a.runContext(check.Name)
	if err != nil {
		return failed(check, err), nil
	}
	diff, err := patchDiff(path)
	if err != nil {
		return failed(check, err), nil
	}
	detail := "reference + " + a.t.Notes.Stale + " as " + strings.Join(staleNotesFiles, ", ")
	return a.grade(ctx, rc, diff, check, detail), nil
}

// grade runs the grader on diff and fills Got, Passed and Detail.
func (a *admitter) grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff, check schemas.AdmissionCheck, detail string) schemas.AdmissionCheck {
	g, err := a.deps.Grader.Grade(ctx, rc, diff)
	if err != nil {
		check.Detail = detail
		return failed(check, fmt.Errorf("grade: %w", err))
	}
	if g == nil {
		check.Detail = detail
		return failed(check, errors.New("grade: grader returned no grade"))
	}
	check.Got = gradeOutcome(g)
	check.Passed = check.Got == check.Want
	check.Detail = joinDetail(detail, gradeSummary(g))
	a.deps.Logger.Info("admission check", "task", a.t.ID, "check", check.Name, "want", check.Want, "got", check.Got)
	return check
}

// runContext builds the minimal run context a grader or environment needs for
// one control: a synthetic, uncounted slot and run under a fresh folder.
func (a *admitter) runContext(name string) (*schemas.RunContext, error) {
	dir := filepath.Join(a.tmp, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("run folder for %s: %w", name, err)
	}
	slot := &schemas.Slot{
		SlotID:      utils.NewID("s", 10),
		PairID:      utils.NewID("p", 10),
		ExpID:       a.expID,
		Suite:       a.s.ID,
		Task:        a.t.ID,
		TaskVersion: a.t.Version,
		Arm:         "control",
		Condition:   schemas.ConditionClean,
		Trial:       1,
	}
	run := &schemas.Run{
		RunID:   utils.NewID("r", 10),
		SlotID:  slot.SlotID,
		PairID:  slot.PairID,
		ExpID:   slot.ExpID,
		Attempt: 1,
		Stage:   schemas.StagePlanned,
		Timing:  schemas.RunTiming{Started: time.Now().UTC()},
	}
	return &schemas.RunContext{Suite: a.s, Task: a.t, Slot: slot, Run: run, Dir: dir, Logger: a.deps.Logger}, nil
}

// patchDiff describes a patch file on disk as an exported diff.
func patchDiff(path string) (*schemas.Diff, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read patch: %w", err)
	}
	return &schemas.Diff{Path: path, SHA256: utils.SHA256Bytes(b), Files: diffFiles(b)}, nil
}

// diffFiles lists the paths a git-style patch touches.
func diffFiles(patch []byte) []schemas.DiffFile {
	var files []schemas.DiffFile
	sc := bufio.NewScanner(bytes.NewReader(patch))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			_, b, ok := strings.Cut(line, " b/")
			if !ok {
				continue
			}
			files = append(files, schemas.DiffFile{Path: b, Status: "modified"})
		case len(files) == 0:
			continue
		case strings.HasPrefix(line, "new file mode"):
			files[len(files)-1].Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			files[len(files)-1].Status = "deleted"
		}
	}
	return files
}

// withNewFiles appends to patch one git "new file" diff per name, each holding
// content.
func withNewFiles(patch, content []byte, names []string) []byte {
	var buf bytes.Buffer
	buf.Write(patch)
	if len(patch) > 0 && patch[len(patch)-1] != '\n' {
		buf.WriteByte('\n')
	}
	text := string(content)
	noEOL := text != "" && !strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for _, name := range names {
		fmt.Fprintf(&buf, "diff --git a/%s b/%s\nnew file mode 100644\n", name, name)
		if text == "" {
			continue
		}
		fmt.Fprintf(&buf, "--- /dev/null\n+++ b/%s\n", name)
		if len(lines) == 1 {
			buf.WriteString("@@ -0,0 +1 @@\n")
		} else {
			fmt.Fprintf(&buf, "@@ -0,0 +1,%d @@\n", len(lines))
		}
		for _, l := range lines {
			buf.WriteString("+" + l + "\n")
		}
		if noEOL {
			buf.WriteString("\\ No newline at end of file\n")
		}
	}
	return buf.Bytes()
}

func gradeOutcome(g *schemas.Grade) string {
	switch {
	case g.InvalidEvaluator:
		return GotInvalidEvaluator
	case g.Passed:
		return GotPass
	default:
		return GotFail
	}
}

func gradeSummary(g *schemas.Grade) string {
	s := fmt.Sprintf("tests_ran=%d failures=%d errors=%d skipped=%d exit=%d", g.TestsRan, g.Failures, g.Errors, g.Skipped, g.ExitCode)
	if len(g.MissingRequired) > 0 {
		s += " missing_required=" + strings.Join(g.MissingRequired, ",")
	}
	if g.Reason != "" {
		s += " reason=" + g.Reason
	}
	return s
}

func failed(c schemas.AdmissionCheck, err error) schemas.AdmissionCheck {
	c.Got = GotError
	c.Passed = false
	c.Detail = joinDetail(c.Detail, err.Error())
	return c
}

func joinDetail(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "; " + b
	}
}
