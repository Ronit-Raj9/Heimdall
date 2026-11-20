package graders

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/visual"
)

// X1 vision grading (docs/visual-grading.md).

// VisualType is the part name suites use to select this grader.
const VisualType = "visual"

const visualVersion = "1.0.0"

// Files of a visual grade in the run folder, sealed with it.
const (
	VisualDir           = "visual"
	VisualCaptureFile   = VisualDir + "/capture.png"
	VisualReferenceFile = VisualDir + "/reference.png"
	VisualDiffFile      = VisualDir + "/diff.png"
)

// VisualConfig wires the visual grader: Inner runs the suite's verifier
// command first when the suite names one (a state or artifact check beside
// the looks), Judge is the gateway wiring the vision judge shares with the
// text judge, and Capturer takes the bench's own captures.
type VisualConfig struct {
	Inner    HiddenTestsConfig
	Judge    JudgeConfig
	Capturer visual.Capturer
	Logger   schemas.Logger
}

var _ schemas.Grader = (*visualGrader)(nil)

// visualGrader grades what the work looks like: it captures the final
// state itself after the agent exited (a page in the browser service, the
// desktop's screen, or an artifact file, rendered when it is svg or html),
// seals the capture, runs the deterministic checks of output.visual
// against the hidden reference, and asks the pinned vision judge when the
// task has a rubric.
type visualGrader struct {
	cfg   VisualConfig
	inner *verifierCommand
}

// NewVisual returns the visual grader wired to cfg. The app registers it
// ahead of Builtin's unwired one (the first grader with a name wins).
func NewVisual(cfg VisualConfig) schemas.Grader { return newVisual(cfg) }

func newVisual(cfg VisualConfig) *visualGrader {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	if cfg.Inner.Logger == nil {
		cfg.Inner.Logger = cfg.Logger
	}
	if cfg.Capturer.Docker.Binary == "" {
		cfg.Capturer.Docker = cfg.Inner.Docker
	}
	return &visualGrader{cfg: cfg, inner: newVerifierCommand(cfg.Inner)}
}

// GetName implements schemas.Part.
func (g *visualGrader) GetName() string { return VisualType }

// GetVersion implements schemas.Part.
func (g *visualGrader) GetVersion() string { return visualVersion }

// VerifierNetwork reports "none": the checks run in the bench process on
// sealed files, svg and html are rendered in a container with --network
// none, a browser or desktop capture runs inside the run's own service
// container (the run's internal networks, no route out), and the inner
// verifier command runs with --network none.
func (g *visualGrader) VerifierNetwork() string { return verifierNetwork }

// VisualSpec is the visual check of the run's task: the task's
// output.visual, else the suite's.
func VisualSpec(s *schemas.Suite, t *schemas.Task) *schemas.VisualCheck {
	if t != nil && t.Output != nil && t.Output.Visual != nil {
		return t.Output.Visual
	}
	if s != nil && s.Output != nil {
		return s.Output.Visual
	}
	return nil
}

// Grade runs the inner verifier when the suite has a command, takes the
// capture, runs every check and the judge, and passes only when all of them
// pass. A task without output.visual is graded by the verifier command
// alone. Output the agent did not produce (a missing artifact,
// an image that does not decode) fails; a capture the bench could not take
// (a service missing, the renderer broken) is invalid_evaluator with the
// reason.
func (g *visualGrader) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	if rc == nil || rc.Suite == nil || rc.Task == nil {
		return nil, errors.New("visual: run context has no suite or task")
	}
	if _, _, err := runIDs(rc); err != nil {
		return nil, fmt.Errorf("visual: %w", err)
	}
	spec := VisualSpec(rc.Suite, rc.Task)
	if spec == nil {
		// A task without output.visual in a suite on grader visual is graded
		// by the verifier command alone, as verifier_command grades it.
		if len(rc.Suite.Verifier.Command) == 0 {
			return nil, fmt.Errorf("visual: task %s has no output.visual and the suite no verifier command", rc.Task.ID)
		}
		ig, err := g.inner.Grade(ctx, rc, diff)
		if err != nil {
			return nil, fmt.Errorf("visual: verifier command: %w", err)
		}
		ig.GraderVersion = visualVersion
		return ig, nil
	}
	if rc.Dir == "" {
		return nil, errors.New("visual: run context has no run folder")
	}
	grade := &schemas.Grade{SuiteVersion: rc.Suite.Version, GraderVersion: visualVersion}
	if diff != nil {
		grade.DiffSHA256 = diff.SHA256
	}
	var inner *schemas.Grade
	if len(rc.Suite.Verifier.Command) > 0 {
		ig, err := g.inner.Grade(ctx, rc, diff)
		if err != nil {
			return nil, fmt.Errorf("visual: verifier command: %w", err)
		}
		inner = ig
		grade.ExitCode, grade.ResultFormat, grade.Reward, grade.Dimensions = ig.ExitCode, ig.ResultFormat, ig.Reward, ig.Dimensions
		grade.TestsRan, grade.Failures, grade.Errors, grade.Skipped, grade.FailedTests = ig.TestsRan, ig.Failures, ig.Errors, ig.Skipped, ig.FailedTests
		if ig.InvalidEvaluator {
			ig.GraderVersion = visualVersion
			ig.Reason = "verifier command: " + ig.Reason
			return ig, nil
		}
	}
	vg := &schemas.VisualGrade{Masks: spec.Masks}
	img, fail, invalid, err := g.capture(ctx, rc, spec, vg)
	if err != nil {
		return nil, fmt.Errorf("visual: %w", err)
	}
	switch {
	case invalid != "":
		grade.InvalidEvaluator, grade.Reason = true, "visual capture: "+invalid
		return grade, nil
	case fail != "":
		grade.Reason = "visual capture: " + fail
		return grade, nil
	}
	grade.Visual = vg
	ref, err := g.reference(rc, spec, vg)
	if err != nil {
		grade.InvalidEvaluator, grade.Reason = true, "visual reference: "+err.Error()
		return grade, nil
	}
	vg.Checks = visual.Evaluate(spec, visual.Inputs{Capture: img, Reference: ref, TaskDir: rc.Task.Dir})
	vg.Passed, vg.Score = visual.Score(vg.Checks)
	if ref != nil {
		if err := g.heatmap(rc, spec, img, ref, vg); err != nil {
			return nil, fmt.Errorf("visual: %w", err)
		}
	}
	if spec.Rubric != "" && rc.NoPaidTraffic {
		// A no-paid-traffic run (agent check, smoke, scripted_upstream)
		// never mints an unscripted judge key: the rubric is not judged.
		grade.InvalidEvaluator, grade.Reason = true, NoPaidJudgeReason
		return grade, nil
	}
	if spec.Rubric != "" {
		res, rec, err := g.judge(ctx, rc, spec, img, ref)
		if err != nil {
			return nil, fmt.Errorf("visual judge: %w", err)
		}
		vg.Judge = res
		grade.Judged, grade.Judge = true, rec
	}
	settle(grade, inner, vg)
	return grade, nil
}

// settle decides Passed and the reason: every check, the judge's verdict
// when there is one, and the inner verifier when it ran.
func settle(grade *schemas.Grade, inner *schemas.Grade, vg *schemas.VisualGrade) {
	var failed []string
	for _, c := range vg.Checks {
		if !c.Passed {
			failed = append(failed, c.ID+" ("+c.Detail+")")
		}
	}
	reason := fmt.Sprintf("visual: %d of %d checks passed", vg.Passed, len(vg.Checks))
	passed := len(failed) == 0
	if len(failed) > 0 {
		reason += "; failed: " + strings.Join(failed, "; ")
	}
	if j := vg.Judge; j != nil {
		reason += "; judge " + j.Verdict
		switch j.Verdict {
		case visualVerdictPass:
		case visualVerdictFail:
			passed = false
		default:
			grade.InvalidEvaluator = true
		}
		if !j.Decides {
			reason += " (uncalibrated: " + j.Reason + ")"
		}
	}
	if len(vg.Checks) == 0 && vg.Judge == nil {
		grade.InvalidEvaluator = true
		reason = "visual: output.visual has no check and no rubric"
	}
	if inner != nil {
		reason = "verifier command: " + inner.Reason + "; " + reason
		passed = passed && inner.Passed
	}
	grade.Passed = passed && !grade.InvalidEvaluator
	grade.Reason = truncate(reason, 2000)
}

// capture takes the capture, writes it to the run folder and records it in
// vg. fail is the
// agent's shortfall (no artifact, an image that does not decode), invalid
// the bench's (a capture it could not take).
func (g *visualGrader) capture(ctx context.Context, rc *schemas.RunContext, spec *schemas.VisualCheck, vg *schemas.VisualGrade) (img *visual.Image, fail, invalid string, err error) {
	set, serr := visual.Settings(spec.Capture)
	if serr != nil {
		return nil, "", serr.Error(), nil
	}
	var shot *visual.Shot
	switch set.Kind {
	case schemas.VisualCaptureArtifact:
		var why string
		shot, why, invalid = g.captureArtifact(ctx, rc, set)
		if why != "" || invalid != "" {
			return nil, why, invalid, nil
		}
	case schemas.VisualCaptureBrowser, schemas.VisualCaptureDesktop:
		svc, ok := findService(rc, set)
		if !ok {
			return nil, "", fmt.Sprintf("the run has no started %s service %q", set.Kind, serviceID(set)), nil
		}
		var cerr error
		if set.Kind == schemas.VisualCaptureBrowser {
			shot, cerr = g.cfg.Capturer.CaptureBrowser(ctx, set, svc.Container)
		} else {
			mcpPath := ""
			if svc.Spec.MCP != nil {
				mcpPath = svc.Spec.MCP.Path
			}
			shot, cerr = g.cfg.Capturer.CaptureDesktop(ctx, svc.Container, svc.Spec.Port, mcpPath)
		}
		if cerr != nil {
			return nil, "", cerr.Error(), nil
		}
		shot.Source.Service = svc.Spec.ID
	default:
		return nil, "", fmt.Sprintf("capture kind %q is not browser, desktop or artifact", set.Kind), nil
	}
	img, derr := visual.Decode(shot.PNG)
	if derr != nil {
		if set.Kind == schemas.VisualCaptureArtifact && shot.Source.Renderer == "" {
			return nil, "artifact image: " + derr.Error(), "", nil
		}
		return nil, "", derr.Error(), nil
	}
	// The capture is kept re-encoded from its decoded pixels, so the file the
	// run page shows is one the bench wrote, never bytes the agent chose.
	data, err := img.EncodePNG()
	if err != nil {
		return nil, "", "", err
	}
	if err := writeRunFile(rc.Dir, VisualCaptureFile, data); err != nil {
		return nil, "", "", err
	}
	vg.Capture = imageRecord(VisualCaptureFile, data, img)
	vg.Source = shot.Source
	vg.Source.Format = img.Format
	return img, "", "", nil
}

// captureArtifact reads or renders the artifact file the capture names.
func (g *visualGrader) captureArtifact(ctx context.Context, rc *schemas.RunContext, set schemas.VisualCapture) (shot *visual.Shot, fail, invalid string) {
	spec, ok := artifactSpec(rc, set.Artifact)
	if !ok {
		return nil, "", fmt.Sprintf("output.artifacts has no artifact %q", set.Artifact)
	}
	name := set.File
	if name == "" {
		name = path.Base(spec.Path)
		if strings.ContainsAny(name, "*?[") {
			return nil, "", fmt.Sprintf("artifact %s is a glob: capture.file must name the file", spec.ID)
		}
	}
	dir := filepath.Join(rc.Dir, utils.ArtifactsDir, spec.ID)
	file := filepath.Join(dir, name)
	st, err := os.Lstat(file)
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Sprintf("artifact %s/%s was not produced", spec.ID, name), ""
	}
	src := schemas.VisualSource{Kind: schemas.VisualCaptureArtifact, Artifact: utils.ArtifactsDir + "/" + spec.ID + "/" + name}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		data, err := readCapped(file)
		if err != nil {
			return nil, "artifact image: " + err.Error(), ""
		}
		return &visual.Shot{PNG: data, Source: src}, "", ""
	case ".svg", ".html", ".htm":
		labels := utils.LabelArgs(rc.Run.ExpID, rc.Run.RunID)
		s, err := g.cfg.Capturer.RenderArtifact(ctx, set, dir, name, labels)
		if err != nil {
			return nil, "", err.Error()
		}
		s.Source.Artifact = src.Artifact
		return s, "", ""
	}
	return nil, "", fmt.Sprintf("artifact %s is not png, jpg, svg or html", name)
}

// reference reads the hidden reference image, if the spec names one, and
// keeps a copy beside the capture so the run page can show what the grade
// compared with.
func (g *visualGrader) reference(rc *schemas.RunContext, spec *schemas.VisualCheck, vg *schemas.VisualGrade) (*visual.Image, error) {
	if spec.Reference == "" {
		return nil, nil
	}
	p, err := visual.TaskPath(rc.Task.Dir, spec.Reference)
	if err != nil {
		return nil, err
	}
	data, err := readCapped(p)
	if err != nil {
		return nil, err
	}
	img, err := visual.Decode(data)
	if err != nil {
		return nil, err
	}
	if data, err = img.EncodePNG(); err != nil {
		return nil, err
	}
	if err := writeRunFile(rc.Dir, VisualReferenceFile, data); err != nil {
		return nil, err
	}
	rec := imageRecord(VisualReferenceFile, data, img)
	vg.Reference = &rec
	return img, nil
}

// heatmap writes the diff heatmap when capture and reference are the same
// size.
func (g *visualGrader) heatmap(rc *schemas.RunContext, spec *schemas.VisualCheck, img, ref *visual.Image, vg *schemas.VisualGrade) error {
	var rects []visual.Rect
	for _, r := range spec.Masks {
		rects = append(rects, visual.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H})
	}
	hm, _ := visual.Heatmap(img, ref, visual.NewMask(img.W, img.H, rects), spec.Tolerance)
	if hm == nil {
		return nil
	}
	data, err := hm.EncodePNG()
	if err != nil {
		return err
	}
	if err := writeRunFile(rc.Dir, VisualDiffFile, data); err != nil {
		return err
	}
	rec := imageRecord(VisualDiffFile, data, hm)
	vg.Diff = &rec
	return nil
}

func imageRecord(rel string, data []byte, img *visual.Image) schemas.VisualImage {
	return schemas.VisualImage{Path: rel, SHA256: utils.SHA256Bytes(data), Bytes: int64(len(data)), Width: img.W, Height: img.H}
}

func findService(rc *schemas.RunContext, set schemas.VisualCapture) (schemas.ServiceHandle, bool) {
	id := serviceID(set)
	for _, s := range rc.Services {
		if s.Spec.ID == id && s.Container != "" {
			return s, true
		}
	}
	return schemas.ServiceHandle{}, false
}

func serviceID(set schemas.VisualCapture) string {
	if set.Service != "" {
		return set.Service
	}
	return set.Kind
}

func artifactSpec(rc *schemas.RunContext, id string) (schemas.ArtifactSpec, bool) {
	for _, a := range utils.OutputArtifacts(rc.Suite, rc.Task) {
		if a.ID == id {
			return a, true
		}
	}
	return schemas.ArtifactSpec{}, false
}

// readCapped reads a regular file of at most visual.MaxImageBytes.
func readCapped(p string) ([]byte, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(p))
	}
	if st.Size() > visual.MaxImageBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d byte cap", filepath.Base(p), st.Size(), visual.MaxImageBytes)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, visual.MaxImageBytes+1))
}

func writeRunFile(dir, rel string, data []byte) error {
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
