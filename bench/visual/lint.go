package visual

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// PromptVisualV1 is the one shipped vision judge prompt version.
const PromptVisualV1 = "visual-v1"

// GraderName is the grader that reads output.visual.
const GraderName = "visual"

// Lint severities, as the suite linter names them.
const (
	LintError   = "error"
	LintWarning = "warning"
)

// LintProblem is one finding of Lint.
type LintProblem struct {
	Severity string
	Message  string
}

// CheckJudge validates a vision judge spec: provider, model and a shipped
// prompt version pinned, the aggregate known, votes from 1 to 9 (odd for
// majority) and pass_score from 0 to 1.
func CheckJudge(j *schemas.VisualJudgeSpec) error {
	if j == nil {
		return errors.New("a rubric needs judge: {provider, model, prompt_version}")
	}
	votes := max(j.Votes, 1)
	agg := j.Aggregate
	if agg == "" {
		agg = "majority"
	}
	switch {
	case j.Provider == "" || j.Model == "" || j.PromptVersion == "":
		return errors.New("vision judge needs provider, model and prompt_version")
	case j.Provider == "pin-me" || j.Model == "pin-me":
		return errors.New("vision judge provider and model are still pin-me: pin them")
	case j.PromptVersion != PromptVisualV1:
		return fmt.Errorf("vision prompt_version %q is not shipped (want %s)", j.PromptVersion, PromptVisualV1)
	case agg != "majority" && agg != "mean":
		return fmt.Errorf("vision judge aggregate %q is not majority or mean", j.Aggregate)
	case j.Votes < 0 || votes > 9 || (agg == "majority" && votes%2 == 0):
		return fmt.Errorf("vision judge votes %d: want 1 to 9, odd for majority", j.Votes)
	case j.PassScore < 0 || j.PassScore > 1:
		return fmt.Errorf("vision judge pass_score %v is outside 0 to 1", j.PassScore)
	case j.MaxTokens < 0:
		return fmt.Errorf("vision judge max_tokens %d is negative", j.MaxTokens)
	}
	return nil
}

// thresholdRange is the range a method's threshold must fall in.
func thresholdRange(method string) (float64, float64) {
	switch method {
	case schemas.VisualMethodPHash:
		return 0, 64
	case schemas.VisualMethodColors:
		return 0, 442
	case schemas.VisualMethodDimensions:
		return 0, 1
	}
	return 0, 1
}

func knownMethod(m string) bool {
	switch m {
	case schemas.VisualMethodDimensions, schemas.VisualMethodPixel, schemas.VisualMethodSSIM, schemas.VisualMethodPHash,
		schemas.VisualMethodColors, schemas.VisualMethodRegion:
		return true
	}
	return false
}

// Lint checks a task's output.visual against the suite and the task folder:
// the grader that reads it, the capture (kind, service or artifact,
// settings), the reference present and decodable, masks, areas and crops
// inside the image, thresholds in range, the rubric present with a pinned
// judge, and the hidden files kept out of what the agent sees.
func Lint(s *schemas.Suite, t *schemas.Task, v *schemas.VisualCheck) []LintProblem {
	var out []LintProblem
	add := func(sev, format string, args ...any) {
		out = append(out, LintProblem{Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if s.Grader != GraderName {
		add(LintError, "output.visual: read only by grader visual; the suite uses %s", s.Grader)
	}
	set, err := Settings(v.Capture)
	if err != nil {
		add(LintError, "output.visual.capture: %v", err)
	}
	size := lintCapture(s, t, set, add)
	var ref *Image
	if v.Reference != "" {
		ref = lintImageFile(t, "output.visual.reference", v.Reference, add)
		if ref != nil {
			size = &Rect{0, 0, ref.W, ref.H}
		}
	}
	for i, m := range v.Masks {
		lintRect(fmt.Sprintf("output.visual.masks[%d]", i), m, size, add)
	}
	checks := Checks(v)
	if len(checks) == 0 && v.Rubric == "" {
		add(LintError, "output.visual: no method, no checks and no rubric; nothing would be graded")
	}
	if v.Method == "" && (v.Threshold != 0 || v.Tolerance != 0) {
		add(LintError, "output.visual: threshold and tolerance belong to method, which is not set")
	}
	ids := map[string]bool{}
	for i, c := range checks {
		field := "output.visual.checks[" + fmt.Sprint(i-btoi(v.Method != "")) + "]"
		if v.Method != "" && i == 0 {
			field = "output.visual.method"
		}
		if ids[c.ID] {
			add(LintError, "%s.id: %q is used by an earlier check", field, c.ID)
		}
		ids[c.ID] = true
		lintCheck(field, c, ref, size, t, add)
	}
	if v.Rubric != "" {
		if p, err := TaskPath(t.Dir, v.Rubric); err != nil {
			add(LintError, "output.visual.rubric: %v", err)
		} else if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
			add(LintError, "output.visual.rubric: %s is not a file of the task folder", v.Rubric)
		}
		if err := CheckJudge(v.Judge); err != nil {
			add(LintError, "output.visual.judge: %v", err)
		}
		lintHidden(t, "output.visual.rubric", v.Rubric, add)
		if utils.Grading(s) != schemas.GradingJudged {
			add(LintError, "output.visual.rubric: the vision judge then decides outcomes, so suite.yaml must declare grading: judged")
		}
	} else if v.Judge != nil {
		add(LintWarning, "output.visual.judge: set, but there is no rubric; the judge is never asked")
	}
	return out
}

// lintCapture checks the capture against the task's services and artifacts
// and returns the capture's size when it is known before the run.
func lintCapture(s *schemas.Suite, t *schemas.Task, set schemas.VisualCapture, add func(string, string, ...any)) *Rect {
	switch set.Kind {
	case schemas.VisualCaptureArtifact:
		if utils.OutputKind(s, t) != schemas.OutputArtifact {
			add(LintError, "output.visual.capture: kind artifact needs output kind artifact")
		}
		var spec *schemas.ArtifactSpec
		for _, a := range utils.OutputArtifacts(s, t) {
			if a.ID == set.Artifact {
				spec = &a
			}
		}
		if spec == nil {
			add(LintError, "output.visual.capture.artifact: %q is not an artifact of output.artifacts", set.Artifact)
			return nil
		}
		name := set.File
		if name == "" {
			name = path.Base(spec.Path)
			if strings.ContainsAny(name, "*?[") {
				add(LintError, "output.visual.capture.file: artifact %s is a glob; name the file to capture", spec.ID)
				return nil
			}
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".png", ".jpg", ".jpeg":
			return nil
		case ".svg":
			return &Rect{0, 0, set.Width * set.Scale, set.Height * set.Scale}
		case ".html", ".htm":
			if set.FullPage {
				return nil
			}
			return &Rect{0, 0, set.Width * set.Scale, set.Height * set.Scale}
		default:
			add(LintError, "output.visual.capture: %s is not png, jpg, svg or html", name)
		}
	case schemas.VisualCaptureBrowser, schemas.VisualCaptureDesktop:
		id := set.Service
		if id == "" {
			id = set.Kind
		}
		var svc *schemas.ServiceSpec
		for _, sv := range utils.TaskServices(s, t) {
			if sv.ID == id {
				svc = &sv
			}
		}
		if svc == nil {
			add(LintError, "output.visual.capture: no service %q in the suite or the task to capture from", id)
		} else if set.Kind == schemas.VisualCaptureDesktop && svc.Port == 0 {
			add(LintError, "output.visual.capture: desktop service %s has no port for its screenshot tool", id)
		}
		if set.Kind == schemas.VisualCaptureBrowser {
			if !strings.HasPrefix(set.URL, "http://") && !strings.HasPrefix(set.URL, "https://") {
				add(LintError, "output.visual.capture.url: %q must be the http url the browser opens, e.g. http://webapp:8000/", set.URL)
			}
			if !set.FullPage {
				return &Rect{0, 0, set.Width * set.Scale, set.Height * set.Scale}
			}
		}
	default:
		add(LintError, "output.visual.capture.kind: %q is not browser, desktop or artifact", set.Kind)
	}
	return nil
}

func lintCheck(field string, c schemas.VisualAssert, ref *Image, size *Rect, t *schemas.Task, add func(string, string, ...any)) {
	if !knownMethod(c.Method) {
		add(LintError, "%s: method %q is not dimensions, pixel, ssim, phash, colors or region", field, c.Method)
		return
	}
	if lo, hi := thresholdRange(c.Method); c.Threshold < lo || c.Threshold > hi {
		add(LintError, "%s: threshold %v for %s is outside %v to %v", field, c.Threshold, c.Method, lo, hi)
	}
	if c.Tolerance < 0 || c.Tolerance > 255 {
		add(LintError, "%s: tolerance %d is outside 0 to 255", field, c.Tolerance)
	}
	if c.Tolerance != 0 && c.Method != schemas.VisualMethodPixel {
		add(LintWarning, "%s: tolerance applies to method pixel only", field)
	}
	needRef := c.Method == schemas.VisualMethodPixel || c.Method == schemas.VisualMethodSSIM || c.Method == schemas.VisualMethodPHash ||
		(c.Method == schemas.VisualMethodRegion && c.CropFile == "") || (c.Method == schemas.VisualMethodDimensions && (c.Width == 0 || c.Height == 0))
	if needRef && ref == nil {
		add(LintError, "%s: method %s needs output.visual.reference", field, c.Method)
	}
	if c.Area != nil {
		lintRect(field+".area", *c.Area, size, add)
	}
	switch c.Method {
	case schemas.VisualMethodColors:
		if len(c.Colors) == 0 {
			add(LintError, "%s: colors needs at least one #rrggbb colour", field)
		}
		for _, col := range c.Colors {
			if _, err := ParseHex(col); err != nil {
				add(LintError, "%s: %v", field, err)
			}
		}
		if c.MinShare < 0 || c.MinShare > 1 {
			add(LintError, "%s: min_share %v is outside 0 to 1", field, c.MinShare)
		}
	case schemas.VisualMethodRegion:
		switch {
		case c.Crop == nil && c.CropFile == "":
			add(LintError, "%s: region needs crop (a rectangle of the reference) or crop_file", field)
		case c.Crop != nil && c.CropFile != "":
			add(LintError, "%s: region takes crop or crop_file, not both", field)
		case c.Crop != nil && ref != nil:
			lintRect(field+".crop", *c.Crop, &Rect{0, 0, ref.W, ref.H}, add)
		case c.CropFile != "":
			lintImageFile(t, field+".crop_file", c.CropFile, add)
		}
		if c.At != nil && size != nil && (c.At.X >= size.W || c.At.Y >= size.H) {
			add(LintError, "%s.at: (%d,%d) is outside the %dx%d image", field, c.At.X, c.At.Y, size.W, size.H)
		}
		if c.MaxOffset < 0 || c.MaxOffset > 512 {
			add(LintError, "%s: max_offset %d is outside 0 to 512", field, c.MaxOffset)
		}
	}
}

// lintRect checks that r is non-empty and, when the image size is known,
// inside it.
func lintRect(field string, r schemas.VisualRect, size *Rect, add func(string, string, ...any)) {
	rr := Rect{r.X, r.Y, r.W, r.H}
	switch {
	case rr.Empty() || r.X < 0 || r.Y < 0:
		add(LintError, "%s: %s is empty or negative", field, rectText(rr))
	case size != nil && !rr.Inside(size.W, size.H):
		add(LintError, "%s: %s is not inside the %dx%d image", field, rectText(rr), size.W, size.H)
	}
}

// lintImageFile checks that rel is an image file of the task folder that
// decodes, kept out of the agent's view, and returns it.
func lintImageFile(t *schemas.Task, field, rel string, add func(string, string, ...any)) *Image {
	p, err := TaskPath(t.Dir, rel)
	if err != nil {
		add(LintError, "%s: %v", field, err)
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		add(LintError, "%s: %s is not a file of the task folder", field, rel)
		return nil
	}
	img, err := Decode(data)
	if err != nil {
		add(LintError, "%s: %s: %v", field, rel, err)
		return nil
	}
	lintHidden(t, field, rel, add)
	return img
}

// lintHidden warns when a hidden grading file sits in the folder copied
// into the agent's workspace.
func lintHidden(t *schemas.Task, field, rel string, add func(string, string, ...any)) {
	if t.RepoDir == "" {
		return
	}
	r := filepath.Clean(t.RepoDir) + string(filepath.Separator)
	if strings.HasPrefix(filepath.Clean(rel)+string(filepath.Separator), r) {
		add(LintError, "%s: %s is inside repo_dir %s, which the agent sees; keep it under verifier/", field, rel, t.RepoDir)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
