package suite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// Paths and limits of script and answer controls.
const (
	controlScriptPath = "/tmp/bench/control.sh"
	controlAnswerPath = "/tmp/bench/control.answer"
	answerFile        = "answer.txt"
	// writeAnswerScript copies the control answer ($2) to the answer path
	// ($1); the paths are arguments, never part of the script.
	writeAnswerScript = `mkdir -p "$(dirname "$1")" && cp "$2" "$1"`
)

// TaskImageEnsurer is implemented by an environment that can make a task's
// own image present (it mirrors engine.TaskImageEnsurer): it returns the tag
// and the image id, or empty strings for a task without its own image.
type TaskImageEnsurer interface {
	EnsureTaskImage(ctx context.Context, s *schemas.Suite, t *schemas.Task) (string, string, error)
}

// control is one script or answer control: what runs in the agent's place.
// task, when set, is the task whose verifier grades it (originalTask).
type control struct {
	name   string
	want   string
	argv   []string
	files  map[string]string
	detail string
	task   *schemas.Task
}

// admitScenario runs the admission controls of a task whose controls are
// scripts or answers. Every control runs through the real environment, with
// the task's services up, its sessions begun and the run's environment
// variables set, in the agent's place: the reference passes 3 times in a
// row, the empty control fails, every mutant fails (on an answer task every
// wrong answer of the hidden AnswerSpec is a mutant too), the exploit fails,
// and the null agents (noop, refuse) fail. The static checks are those of a
// patch task.
func (a *admitter) admitScenario(ctx context.Context, rec *schemas.AdmissionRecord, checks []schemas.AdmissionCheck) (*schemas.AdmissionRecord, error) {
	paths, err := a.controlPaths()
	if err != nil {
		return nil, err
	}
	kind := utils.ControlKind(a.s, a.t)
	mk := func(name, want, file string) control {
		c, err := a.fileControl(kind, name, want, file)
		if err != nil {
			return control{name: name, want: want, detail: err.Error()}
		}
		return c
	}
	honest, honestIsRef, err := a.honestControl()
	if err != nil {
		return nil, err
	}
	var controls []control
	if !honestIsRef {
		for i := 1; i <= referenceRuns; i++ {
			controls = append(controls, mk(fmt.Sprintf("reference_%d", i), GotPass, paths.reference))
		}
	}
	if honest != "" {
		orig := a.originalTask()
		for i := 1; i <= referenceRuns; i++ {
			c := mk(fmt.Sprintf("honest_original_%d", i), GotPass, honest)
			c.task = orig
			controls = append(controls, c)
		}
		controls = append(controls, mk("honest_mutated", GotFail, honest))
	}
	controls = append(controls, mk("empty_control", GotFail, paths.empty))
	for i, m := range paths.mutants {
		controls = append(controls, mk(fmt.Sprintf("mutant_%d", i+1), GotFail, m))
	}
	if kind == utils.ControlAnswer {
		wrong, err := a.wrongAnswers()
		if err != nil {
			return nil, err
		}
		for i, w := range wrong {
			controls = append(controls, a.answerControl(fmt.Sprintf("wrong_answer_%d", i+1), GotFail, w, "wrong answer from the answer spec"))
		}
	}
	exploit, err := a.fileControl(utils.ControlScript, "exploit", GotFail, paths.exploit)
	if err != nil {
		exploit = control{name: "exploit", want: GotFail, detail: err.Error()}
	}
	controls = append(controls, exploit)
	controls = append(controls,
		control{name: "null_noop", want: GotFail, argv: []string{"true"}, files: map[string]string{}, detail: "noop"},
		control{name: "null_refuse", want: GotFail, argv: []string{"sh", "-c", "echo \"" + refuseMessage + "\""}, files: map[string]string{}, detail: "refuse"},
	)
	image, err := a.controlImage(ctx)
	for _, c := range controls {
		var check schemas.AdmissionCheck
		switch {
		case err != nil:
			check = failed(schemas.AdmissionCheck{Name: c.name, Want: c.want}, err)
		case c.argv == nil:
			check = failed(schemas.AdmissionCheck{Name: c.name, Want: c.want}, errors.New(c.detail))
		default:
			check = a.runControl(ctx, c, image)
		}
		if strings.HasPrefix(c.name, "reference_") || (honestIsRef && strings.HasPrefix(c.name, "honest_original_")) {
			rec.ReferenceResults = append(rec.ReferenceResults, check.Got)
		}
		checks = append(checks, check)
	}
	checks = append(checks, a.instructionEdited(), a.secretScan(), a.verifierNetwork())
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("admit task %s: %w", a.t.ID, err)
	}
	rec.Checks = checks
	for _, c := range checks {
		if !c.Passed {
			rec.Admitted = false
		}
	}
	a.deps.Logger.Info("task admission", "suite", a.s.ID, "task", a.t.ID, "version", a.t.Version, "admitted", rec.Admitted, "controls", kind)
	return rec, nil
}

// scriptArgv runs a control script in the agent's place.
var scriptArgv = []string{"sh", controlScriptPath}

// fileControl reads one control file: a script to run, or an answer to write.
func (a *admitter) fileControl(kind, name, want, file string) (control, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return control{}, fmt.Errorf("read control: %w", err)
	}
	if kind == utils.ControlAnswer {
		return a.answerControl(name, want, string(b), rel(a.t.Dir, file)), nil
	}
	return control{name: name, want: want, argv: scriptArgv, files: map[string]string{controlScriptPath: string(b)}, detail: rel(a.t.Dir, file)}, nil
}

// answerControl writes text to the task's answer path in the agent's place.
func (a *admitter) answerControl(name, want, text, detail string) control {
	return control{name: name, want: want, detail: detail,
		argv:  []string{"sh", "-c", writeAnswerScript, "write-answer", utils.AnswerContainerPath(a.s, a.t), controlAnswerPath},
		files: map[string]string{controlAnswerPath: text}}
}

// wrongAnswers reads the wrong answers of the task's hidden AnswerSpec
// (verifier.answer, else answer.yaml in the verifier tests folder). A task
// without an answer file has none.
func (a *admitter) wrongAnswers() ([]string, error) {
	rp := a.t.Verifier.Answer
	if rp == "" {
		rp = path.Join(filepath.ToSlash(a.t.Verifier.Tests), defaultAnswerSpec)
	}
	b, err := utils.ReadFileInside(a.t.Dir, rp)
	if errors.Is(err, os.ErrNotExist) && a.t.Verifier.Answer == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("admit task %s: answer spec: %w", a.t.ID, err)
	}
	var spec schemas.AnswerSpec
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("admit task %s: answer spec %s: %w", a.t.ID, rp, err)
	}
	return spec.WrongOnes, nil
}

// controlImage is the image a control runs in: the task's own image when it
// has one (built or pulled through the environment), else the suite's base
// image.
func (a *admitter) controlImage(ctx context.Context) (string, error) {
	env := a.t.Environment
	if env == nil {
		return a.s.BaseImage.Tag, nil
	}
	if ens, ok := a.deps.Env.(TaskImageEnsurer); ok {
		// Parallel admissions: tasks sharing an image key build it once,
		// one at a time; distinct images still build concurrently.
		unlock := lockImageKey(a.s.ID + "\x00" + taskImageKey(a.t))
		tag, _, err := ens.EnsureTaskImage(ctx, a.s, a.t)
		unlock()
		if err != nil {
			return "", fmt.Errorf("task image: %w", err)
		}
		if tag != "" {
			return tag, nil
		}
	}
	if env.Image != "" {
		return env.Image, nil
	}
	return "", fmt.Errorf("task %s builds its own image, but environment %s cannot build it", a.t.ID, a.deps.Env.GetName())
}

// runControl runs one control in a fresh run of the real environment and
// grades what it left. The workspace and the services are always destroyed.
func (a *admitter) runControl(ctx context.Context, c control, image string) (check schemas.AdmissionCheck) {
	check = schemas.AdmissionCheck{Name: c.name, Want: c.want}
	rc, err := a.runContext(c.name)
	if err != nil {
		return failed(check, err)
	}
	if c.task != nil {
		rc.Task = c.task
	}
	rc.Agent = &schemas.AgentConfig{ID: "admission-" + c.name, Adapter: "scripted", Image: image,
		Notes: schemas.AgentNotes{ProjectFile: "AGENTS.md"}, Budgets: schemas.Budgets{WallS: exploitWallS, CPUs: 1, MemGB: 1}}
	defer func() {
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), destroyTimeout)
		defer cancel()
		if err := a.deps.Env.Destroy(dctx, rc); err != nil {
			a.deps.Logger.Warn("admission: destroy workspace", "task", a.t.ID, "check", c.name, "run", rc.Run.RunID, "error", err)
			check.Detail = joinDetail(check.Detail, "destroy: "+err.Error())
		}
	}()
	handle, err := a.deps.Env.Create(ctx, rc)
	if err != nil {
		return failed(check, fmt.Errorf("create workspace: %w", err))
	}
	rc.Handle = handle
	if err := a.startServices(ctx, rc); err != nil {
		return failed(check, err)
	}
	if err := a.beginSessions(ctx, rc); err != nil {
		return failed(check, err)
	}
	env, err := controlEnv(rc)
	if err != nil {
		return failed(check, err)
	}
	plan := &schemas.RunPlan{Image: image, Argv: c.argv, Env: env, Files: c.files, Workdir: a.s.Workdir,
		WallS: exploitWallS, CPUs: 1, MemGB: 1, Network: "none", User: exploitUser}
	a.gpuControlBudget(plan) // X7: a GPU task's controls train on the GPU (gpu.go)
	// The reference runs in the agent's place, so it may take as long as the
	// agent may (timeouts.agent_s): a long-horizon reference (Vending Bench's
	// scripted operator plays a simulated year in about 2 h) cannot fit the
	// short budget of the other controls.
	if strings.HasPrefix(c.name, "reference_") && a.t.Timeouts.AgentS > plan.WallS {
		plan.WallS = a.t.Timeouts.AgentS
	}
	res, err := a.deps.Env.Exec(ctx, rc, plan)
	if err != nil {
		return failed(check, fmt.Errorf("run %s: %w", c.name, err))
	}
	rc.Exec = res
	detail := fmt.Sprintf("%s exit %d", c.detail, res.ExitCode)
	if res.TimedOut {
		detail += " (timed out)"
	}
	diff, why, fail, err := a.exportControl(ctx, rc, res)
	switch {
	case err != nil:
		check.Detail = detail
		return failed(check, err)
	case why != "":
		check.Got, check.Detail = GotInvalidEvaluator, joinDetail(detail, why)
		return check
	case fail != "":
		// The grader's input is the agent's own result and it is incomplete:
		// a fail, decided without running the verifier, as in a real run.
		check.Got, check.Detail = GotFail, joinDetail(detail, fail)
		check.Passed = check.Got == check.Want
		a.deps.Logger.Info("admission check", "task", a.t.ID, "check", check.Name, "want", check.Want, "got", check.Got)
		return check
	}
	return a.grade(ctx, rc, diff, check, detail)
}

// startServices starts the task's services for one control run.
func (a *admitter) startServices(ctx context.Context, rc *schemas.RunContext) error {
	if len(utils.TaskServices(a.s, a.t)) == 0 && len(utils.ExternalAPIs(a.s, a.t)) == 0 { // External APIs area: external APIs run through the tool proxy
		return nil
	}
	se, ok := a.deps.Env.(schemas.ServiceEnvironment)
	if !ok {
		return fmt.Errorf("task %s has services, but environment %s cannot run services", a.t.ID, a.deps.Env.GetName())
	}
	handles, results, err := se.StartServices(ctx, rc)
	rc.Services = handles
	var bad []string
	for _, r := range results {
		if !r.Ready {
			bad = append(bad, r.ID+" ("+r.Error+")")
		}
	}
	switch {
	case err != nil:
		return fmt.Errorf("start services: %w", err)
	case len(bad) > 0:
		return fmt.Errorf("start services: not ready: %s", strings.Join(bad, ", "))
	}
	return nil
}

// beginSessions prepares the workspace of every session in order, so the
// control runs where the agent's last session would.
func (a *admitter) beginSessions(ctx context.Context, rc *schemas.RunContext) error {
	if len(a.t.Sessions) == 0 {
		return nil
	}
	se, ok := a.deps.Env.(schemas.SessionEnvironment)
	if !ok {
		return fmt.Errorf("task %s has sessions, but environment %s cannot run sessions", a.t.ID, a.deps.Env.GetName())
	}
	for i := range a.t.Sessions {
		if err := se.BeginSession(ctx, rc, i); err != nil {
			return fmt.Errorf("begin session %s: %w", a.t.Sessions[i].ID, err)
		}
	}
	last := len(a.t.Sessions) - 1
	rc.Session, rc.SessionIndex = &a.t.Sessions[last], last
	return nil
}

// exportControl exports the diff, the services' state, the answer and the
// artifact files, takes the snapshot of a snapshot-graded suite, and writes
// answer.txt (the control's stdout when it wrote no answer file, as the run's
// final message would be). why is set when the grader's input is missing
// through no fault of the control (an evaluator problem); fail is set when
// the control's own result is incomplete (a required artifact is missing),
// which fails it without running the verifier.
func (a *admitter) exportControl(ctx context.Context, rc *schemas.RunContext, res *schemas.ExecResult) (diff *schemas.Diff, why, fail string, err error) {
	if utils.VerifierMode(a.s) == schemas.VerifierSnapshot {
		sn, ok := a.deps.Env.(schemas.SnapshotEnvironment)
		if !ok {
			return nil, "", "", fmt.Errorf("suite grades snapshots, but environment %s cannot take one", a.deps.Env.GetName())
		}
		if _, err := sn.Snapshot(ctx, rc); err != nil {
			return nil, "", "", fmt.Errorf("snapshot: %w", err)
		}
	}
	diff, err = a.deps.Env.ExportDiff(ctx, rc)
	if err != nil {
		return nil, "", "", fmt.Errorf("export diff: %w", err)
	}
	var state []schemas.StateFile
	if len(rc.Services) > 0 {
		se := a.deps.Env.(schemas.ServiceEnvironment)
		if state, err = se.ExportState(ctx, rc); err != nil {
			return nil, "", "", fmt.Errorf("export state: %w", err)
		}
	}
	switch utils.OutputKind(a.s, a.t) {
	case schemas.OutputState:
		if len(state) == 0 {
			return diff, "state output: no service state was exported", "", nil
		}
		for _, f := range state {
			if f.Error != "" {
				return diff, "state output: export failed: " + f.Service + "/" + f.ID + ": " + f.Error, "", nil
			}
		}
	case schemas.OutputArtifact:
		ae, ok := a.deps.Env.(schemas.ArtifactEnvironment)
		if !ok {
			return nil, "", "", fmt.Errorf("task %s has artifact output, but environment %s cannot export artifacts", a.t.ID, a.deps.Env.GetName())
		}
		files, err := ae.ExportArtifacts(ctx, rc)
		if err != nil {
			return diff, "artifact output: export failed: " + err.Error(), "", nil
		}
		if missing := utils.MissingArtifacts(utils.OutputArtifacts(a.s, rc.Task), files); len(missing) > 0 {
			return diff, "", "artifact output: required artifact missing: " + strings.Join(missing, ", "), nil
		}
	case schemas.OutputAnswer:
		ae, ok := a.deps.Env.(schemas.AnswerEnvironment)
		if !ok {
			return nil, "", "", fmt.Errorf("environment %s cannot read answers", a.deps.Env.GetName())
		}
		ans, err := ae.ExportAnswer(ctx, rc)
		if err != nil {
			return nil, "", "", fmt.Errorf("export answer: %w", err)
		}
		text := ""
		if ans != nil && ans.Source != "missing" && ans.Source != "" {
			text = ans.Text
		} else if b, err := os.ReadFile(res.StdoutPath); err == nil {
			text = strings.TrimSpace(string(b))
		}
		if err := os.WriteFile(filepath.Join(rc.Dir, answerFile), []byte(text), 0o644); err != nil {
			return nil, "", "", fmt.Errorf("write answer: %w", err)
		}
	}
	return diff, "", "", nil
}

// controlEnv is the environment a control gets, as a run's agent would:
// the MCP servers, the answer path and the last session's variables.
func controlEnv(rc *schemas.RunContext) (map[string]string, error) {
	env := map[string]string{}
	if len(rc.Services) > 0 {
		servers, err := utils.MCPServersJSON(rc.Services)
		if err != nil {
			return nil, err
		}
		env[utils.EnvMCPServers] = servers
		if err := utils.AddExternalEnv(env, rc.Services); err != nil { // External APIs area: BENCH_EXTERNAL_APIS
			return nil, err
		}
	}
	if utils.OutputKind(rc.Suite, rc.Task) == schemas.OutputAnswer {
		env[utils.EnvAnswerPath] = utils.AnswerContainerPath(rc.Suite, rc.Task)
	}
	if rc.Session != nil {
		env[utils.EnvSessionID] = rc.Session.ID
		env[utils.EnvSessionIndex] = strconv.Itoa(rc.SessionIndex + 1)
		env[utils.EnvSessionCount] = strconv.Itoa(len(rc.Task.Sessions))
		env[utils.EnvMemoryDir] = utils.DefaultMemoryDir
	}
	return env, nil
}

// imageLocks serializes EnsureTaskImage per image key across concurrent
// admissions in one process.
var imageLocks sync.Map // key -> *sync.Mutex

func lockImageKey(key string) func() {
	m, _ := imageLocks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// taskImageKey names the image a task's environment builds or pulls: its
// image tag when it names one, else its task folder.
func taskImageKey(t *schemas.Task) string {
	if t.Environment != nil && t.Environment.Image != "" {
		return "image:" + t.Environment.Image
	}
	return "dir:" + t.Dir
}
