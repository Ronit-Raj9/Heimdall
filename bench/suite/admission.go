package suite

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/compose"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Admission parameters, matching tools/checktask.sh.
const (
	exploitScriptPath = "/tmp/bench/exploit.sh"
	exploitWallS      = 120
	exploitUser       = "1000"
	destroyTimeout    = 2 * time.Minute
)

// referenceRuns is how many times admission grades the reference
// solution. HEIMDALL_ADMIT_REFERENCE_RUNS (1-3) lowers it for a quick
// first pass; the record keeps the count used.
var referenceRuns = admitReferenceRuns()

func admitReferenceRuns() int {
	if n, err := strconv.Atoi(os.Getenv("HEIMDALL_ADMIT_REFERENCE_RUNS")); err == nil && n >= 1 && n <= 3 {
		return n
	}
	return 3
}

// Admission check outcomes, used as AdmissionCheck.Want and Got.
const (
	GotPass             = "pass"
	GotFail             = "fail"
	GotInvalidEvaluator = "invalid_evaluator"
	GotError            = "error"
	// WantPassOrFail is the Want of a control that is run and recorded but
	// may go either way: the null refusal of an abstention task, where
	// declining to answer can be the right behaviour. An evaluator error
	// still fails it.
	WantPassOrFail = "pass_or_fail"
)

// wantMet reports whether got satisfies want.
func wantMet(want, got string) bool {
	if want == WantPassOrFail {
		return got == GotPass || got == GotFail
	}
	return got == want
}

// Wanted values of the static admission checks.
const (
	wantEdited    = "edited"
	wantNoSecrets = "none"
	wantNoNetwork = "none"
	wantClean     = "clean"
	refuseMessage = "I can't complete this task."
)

// StaticValidationCheck is the admission check that records the static
// validation (ValidateTask) admission ran before any control.
const StaticValidationCheck = "static_validation"

// maxDetailProblems is how many problems an admission check detail names.
const maxDetailProblems = 5

// VerifierNetworkReporter is implemented by a grader that can say which
// Docker network its verifier container gets. Admission requires "none".
type VerifierNetworkReporter interface {
	VerifierNetwork() string
}

// isolatedGraders are the shipped graders whose verifier container always
// runs with --network none, for graders that predate VerifierNetworkReporter.
var isolatedGraders = map[string]bool{"hidden_tests": true, "verifier_command": true}

// staleNotesFiles are where the stale notes are overlaid for the goal-preserving
// check: both project notes names an agent may read, as checktask.sh does.
var staleNotesFiles = []string{"CLAUDE.md", "AGENTS.md"}

// AdmitDeps are the parts admission runs the controls through. Logger may be
// nil.
type AdmitDeps struct {
	Env    schemas.Environment
	Grader schemas.Grader
	Logger schemas.Logger
	GPUs   schemas.GPUAllocator // X7: GPUs for a GPU task's controls and verifier (gpu.go); nil refuses such a task
}

// Admit runs the admission controls of one task version, the checks of
// tools/checktask.sh and docs/bring-your-own-design.md: the reference passes
// 3 times in a row (determinism), the empty patch fails, every mutant fails,
// the exploit script fails under clean grading, the reference still passes
// with the stale notes overlaid when the task has stale notes (the
// stale-notes probe is goal-preserving), the null agents (noop, refuse) fail,
// the instruction no longer holds the EDIT ME marker, no task file matches a
// secret pattern, and the verifier runs with no network. Patches are graded
// directly by deps.Grader; the exploit and the null agents run in workspaces
// from deps.Env and their exported diffs are graded. A failed control is a
// failed check in the record, not an error; Admit returns an error only when
// it cannot run the checks at all.
//
// An impossible task with an honest solution (controls.honest) must also
// show it is possible without its mutation: the honest solution passes the
// original tests (verifier.original_tests) 3 times in a row and fails the
// task's own tests. When the honest solution is the reference itself, the
// reference is not required to pass the task's tests (and the stale-notes
// check, which grades the reference, is skipped); a separate reference (a
// gaming solution) keeps its checks.
//
// A task whose controls are scripts or answers (utils.ControlKind) runs its
// controls through the real environment instead (admitScenario); a patch
// task is admitted exactly as before.
//
// Before any control runs, Admit runs ValidateTask, the static validation of
// `task validate`. A task with an error problem gets a record holding only a
// failed static_validation check, and nothing is created in Docker; a clean
// one records static_validation as passed, ahead of the controls.
func Admit(ctx context.Context, deps AdmitDeps, s *schemas.Suite, t *schemas.Task) (*schemas.AdmissionRecord, error) {
	if deps.Env == nil || deps.Grader == nil {
		return nil, errors.New("admit: environment and grader are required")
	}
	if deps.Logger == nil {
		deps.Logger = schemas.NoopLogger{}
	}
	static := staticValidation(ValidateTask(ctx, s, t))
	if !static.Passed {
		deps.Logger.Warn("task admission refused by static validation", "suite", s.ID, "task", t.ID, "detail", static.Detail)
		return &schemas.AdmissionRecord{Admitted: false, At: time.Now().UTC(), Grader: deps.Grader.GetName(),
			Checks: []schemas.AdmissionCheck{static}}, nil
	}
	tmp, err := os.MkdirTemp("", "bench-admit-"+t.ID+"-")
	if err != nil {
		return nil, fmt.Errorf("admit task %s: %w", t.ID, err)
	}
	defer os.RemoveAll(tmp)
	a := &admitter{deps: deps, s: s, t: t, tmp: tmp, expID: utils.NewID("e", 10)}
	releaseGPU, err := a.holdGPU(ctx) // X7: a GPU task holds its GPUs for the whole admission (gpu.go)
	if err != nil {
		return nil, err
	}
	defer releaseGPU()
	if utils.ControlKind(s, t) != utils.ControlPatch {
		rec := &schemas.AdmissionRecord{Admitted: true, At: time.Now().UTC(), Grader: deps.Grader.GetName(), ReferenceRepeats: referenceRuns}
		return a.admitScenario(ctx, rec, []schemas.AdmissionCheck{static})
	}

	paths, err := a.controlPaths()
	if err != nil {
		return nil, err
	}
	rec := &schemas.AdmissionRecord{Admitted: true, At: time.Now().UTC(), Grader: deps.Grader.GetName(), ReferenceRepeats: referenceRuns}
	checks := []schemas.AdmissionCheck{static}
	honest, honestIsRef, err := a.honestControl()
	if err != nil {
		return nil, err
	}
	if !honestIsRef {
		for i := 1; i <= referenceRuns; i++ {
			c := a.gradePatch(ctx, fmt.Sprintf("reference_%d", i), paths.reference, GotPass)
			rec.ReferenceResults = append(rec.ReferenceResults, c.Got)
			checks = append(checks, c)
		}
	}
	if honest != "" {
		orig := a.originalTask()
		for i := 1; i <= referenceRuns; i++ {
			c := a.gradePatchFor(ctx, fmt.Sprintf("honest_original_%d", i), honest, GotPass, orig)
			if honestIsRef {
				rec.ReferenceResults = append(rec.ReferenceResults, c.Got)
			}
			checks = append(checks, c)
		}
		checks = append(checks, a.gradePatchFor(ctx, "honest_mutated", honest, GotFail, a.t))
	}
	checks = append(checks, a.gradePatch(ctx, "empty_patch", paths.empty, GotFail))
	for i, m := range paths.mutants {
		checks = append(checks, a.gradePatch(ctx, fmt.Sprintf("mutant_%d", i+1), m, GotFail))
	}
	checks = append(checks, a.exploit(ctx, paths.exploit))
	if t.Notes.Stale != "" && !honestIsRef {
		staleCheck, err := a.staleNotes(ctx, paths.reference)
		if err != nil {
			return nil, err
		}
		checks = append(checks, staleCheck)
	}
	checks = append(checks, a.nullAgents(ctx)...)
	checks = append(checks, a.instructionEdited(), a.secretScan(), a.verifierNetwork())
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("admit task %s: %w", t.ID, err)
	}

	rec.Checks = checks
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
// the hashes that were admitted. A lock made by LockWith also stamps the
// record with the suite and task fingerprints it admitted
// (AdmissionFingerprint), so a later change to any evaluator setting of the
// task un-admits it.
func RecordAdmission(l *schemas.SuiteLock, taskID string, rec *schemas.AdmissionRecord) error {
	tl, ok := l.Tasks[taskID]
	if !ok {
		return fmt.Errorf("record admission: task %s is not in the lock", taskID)
	}
	if rec != nil && l.Fingerprints != nil {
		rec.Fingerprint = AdmissionFingerprint(l.Fingerprints, taskID)
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

	// X7: the GPUs a GPU task's controls and verifier get (gpu.go).
	gpu *schemas.GPUAssignment
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
	return a.gradePatchFor(ctx, name, patch, want, a.t)
}

// gradePatchFor is gradePatch against the verifier of task, a copy of the
// admitted task that may name other tests (originalTask).
func (a *admitter) gradePatchFor(ctx context.Context, name, patch, want string, task *schemas.Task) schemas.AdmissionCheck {
	check := schemas.AdmissionCheck{Name: name, Want: want}
	rc, err := a.runContext(name)
	if err != nil {
		return failed(check, err)
	}
	rc.Task = task
	diff, err := patchDiff(patch)
	if err != nil {
		return failed(check, err)
	}
	return a.grade(ctx, rc, diff, check, rel(a.t.Dir, patch))
}

// exploit runs the exploit script in a fresh workspace, exports the diff it
// left and grades that diff cleanly.
func (a *admitter) exploit(ctx context.Context, script string) schemas.AdmissionCheck {
	content, err := os.ReadFile(script)
	if err != nil {
		return failed(schemas.AdmissionCheck{Name: "exploit", Want: GotFail}, fmt.Errorf("read exploit: %w", err))
	}
	return a.scripted(ctx, "exploit", "exploit", []string{"sh", exploitScriptPath}, map[string]string{exploitScriptPath: string(content)})
}

// nullAgents runs the two null controls, each in a fresh workspace: noop
// changes nothing, refuse prints a refusal and changes nothing. Both must
// fail, which proves the verifier does not pass an untouched workspace.
func (a *admitter) nullAgents(ctx context.Context) []schemas.AdmissionCheck {
	return []schemas.AdmissionCheck{
		a.scripted(ctx, "null_noop", "noop", []string{"true"}, map[string]string{}),
		a.scripted(ctx, "null_refuse", "refuse", []string{"sh", "-c", "echo \"" + refuseMessage + "\""}, map[string]string{}),
	}
}

// scripted runs argv as uid 1000 with no network in a fresh workspace of the
// suite's base image, exports the diff it left and grades that diff cleanly,
// wanting a fail. The workspace is always destroyed.
func (a *admitter) scripted(ctx context.Context, name, kind string, argv []string, files map[string]string) (check schemas.AdmissionCheck) {
	check = schemas.AdmissionCheck{Name: name, Want: GotFail}
	rc, err := a.runContext(name)
	if err != nil {
		return failed(check, err)
	}
	rc.Agent = &schemas.AgentConfig{
		ID:       "admission-" + kind,
		Adapter:  "scripted",
		Image:    a.s.BaseImage.Tag,
		Notes:    schemas.AgentNotes{ProjectFile: "AGENTS.md"},
		Budgets:  schemas.Budgets{WallS: exploitWallS, CPUs: 1, MemGB: 1},
		Scripted: &schemas.ScriptedSpec{Kind: kind},
	}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), destroyTimeout)
		defer cancel()
		if err := a.deps.Env.Destroy(dctx, rc); err != nil {
			a.deps.Logger.Warn("admission: destroy workspace", "task", a.t.ID, "check", name, "run", rc.Run.RunID, "error", err)
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
		Argv:    argv,
		Env:     map[string]string{},
		Files:   files,
		Workdir: a.s.Workdir,
		WallS:   exploitWallS,
		CPUs:    1,
		MemGB:   1,
		Network: "none",
		User:    exploitUser,
	}
	res, err := a.deps.Env.Exec(ctx, rc, plan)
	if err != nil {
		return failed(check, fmt.Errorf("run %s: %w", kind, err))
	}
	rc.Exec = res
	diff, err := a.deps.Env.ExportDiff(ctx, rc)
	if err != nil {
		return failed(check, fmt.Errorf("export diff: %w", err))
	}
	detail := fmt.Sprintf("%s exit %d", kind, res.ExitCode)
	if res.TimedOut {
		detail += " (timed out)"
	}
	return a.grade(ctx, rc, diff, check, detail)
}

// staticValidation turns ValidateTask's problems into the static_validation
// check: passed with no error problem. The detail names the first errors, or
// counts the warnings.
func staticValidation(problems []Problem) schemas.AdmissionCheck {
	c := schemas.AdmissionCheck{Name: StaticValidationCheck, Want: wantClean}
	var errs []string
	for _, p := range problems {
		if p.Severity == SeverityError {
			errs = append(errs, p.Path+": "+p.Message)
		}
	}
	if len(errs) == 0 {
		c.Got, c.Passed = wantClean, true
		c.Detail = fmt.Sprintf("%d warnings", len(problems))
		return c
	}
	c.Got = fmt.Sprintf("%d errors", len(errs))
	if len(errs) > maxDetailProblems {
		errs = append(errs[:maxDetailProblems:maxDetailProblems], fmt.Sprintf("and %d more", len(errs)-maxDetailProblems))
	}
	c.Detail = strings.Join(errs, "; ")
	return c
}

// instructionEdited checks that the draft marker task init writes is gone.
func (a *admitter) instructionEdited() schemas.AdmissionCheck {
	c := schemas.AdmissionCheck{Name: "instruction_edited", Want: wantEdited}
	path, err := resolveIn(a.t.Dir, a.t.Instruction)
	if err != nil {
		return failed(c, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return failed(c, fmt.Errorf("read instruction: %w", err))
	}
	if n := editMeLine(b); n > 0 {
		c.Got = "draft"
		c.Detail = fmt.Sprintf("%s line %d holds %s", a.t.Instruction, n, EditMeMarker)
		return c
	}
	c.Got, c.Passed = wantEdited, true
	return c
}

// secretScan checks that no file of the task matches a secret pattern. The
// detail names file and line with the value masked.
func (a *admitter) secretScan() schemas.AdmissionCheck {
	c := schemas.AdmissionCheck{Name: "secret_scan", Want: wantNoSecrets}
	findings, err := ScanSecrets(a.t.Dir)
	if err != nil {
		return failed(c, err)
	}
	if len(findings) == 0 {
		c.Got, c.Passed = wantNoSecrets, true
		return c
	}
	c.Got = fmt.Sprintf("%d findings", len(findings))
	var parts []string
	for i, f := range findings {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("and %d more", len(findings)-i))
			break
		}
		parts = append(parts, f.String())
	}
	c.Detail = strings.Join(parts, "; ")
	return c
}

// verifierNetwork checks that the verifier container has no network: as the
// grader reports it, or by construction for the shipped graders.
func (a *admitter) verifierNetwork() schemas.AdmissionCheck {
	if names := compose.VerifierServices(a.t); len(names) > 0 { // X2: a compose task's verifier joins only internal run networks (compose.go)
		return composeVerifierNetwork(names)
	}
	c := schemas.AdmissionCheck{Name: "verifier_no_network", Want: wantNoNetwork}
	name := a.deps.Grader.GetName()
	switch r, ok := a.deps.Grader.(VerifierNetworkReporter); {
	case ok:
		c.Got = r.VerifierNetwork()
		c.Detail = "reported by grader " + name
	case isolatedGraders[name]:
		c.Got = wantNoNetwork
		c.Detail = "grader " + name + " runs the verifier with --network none"
	default:
		c.Got = "unknown"
		c.Detail = "grader " + name + " does not report its verifier network"
	}
	c.Passed = c.Got == c.Want
	return c
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
	check.Passed = wantMet(check.Want, check.Got)
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
	return &schemas.RunContext{Suite: a.s, Task: a.t, Slot: slot, Run: run, Dir: dir, Logger: a.deps.Logger, GPU: a.gpu}, nil // X7: GPU
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

// honestControl returns the absolute path of an impossible task's honest
// solution and whether it is the reference itself; "" when the task is not
// impossible or names none.
func (a *admitter) honestControl() (string, bool, error) {
	if !a.t.Impossible || a.t.Controls.Honest == "" {
		return "", false, nil
	}
	abs, err := resolveIn(a.t.Dir, a.t.Controls.Honest)
	if err != nil {
		return "", false, fmt.Errorf("admit task %s: controls.honest: %w", a.t.ID, err)
	}
	same := path.Clean(filepath.ToSlash(a.t.Controls.Honest)) == path.Clean(filepath.ToSlash(a.t.Controls.Reference))
	return abs, same, nil
}

// originalTask is a copy of the admitted task whose verifier runs the
// original, unmutated tests.
func (a *admitter) originalTask() *schemas.Task {
	orig := *a.t
	orig.Verifier.Tests = a.t.Verifier.OriginalTests
	return &orig
}
