package graders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// VerifierCommandType is the part name suites use to select this grader.
const VerifierCommandType = "verifier_command"

const (
	verifierCommandVersion = "1.0.0"
	resultDir              = "/grader/out"
	defaultRewardPath      = resultDir + "/reward.json"
	defaultWorkdir         = "/workspace"
	maxRewardBytes         = 64 << 10
	maxDimensions          = 32
	defaultPassThreshold   = 1.0
	maxPassThreshold       = 1e9
	maxReasonQuote         = 240
)

var (
	resultNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	dimensionRe  = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	decimalRe    = regexp.MustCompile(`^[-+]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][-+]?[0-9]+)?$`)
	utf8BOM      = []byte("\xef\xbb\xbf")
	// reservedMounts are paths the verifier container already uses or that
	// must stay the image's own; the workspace cannot be mounted over them.
	reservedMounts = []string{"/grader", "/tmp", "/home/agent", "/proc", "/sys", "/dev"}
)

// verifierCommand grades a suite in any language: it runs the suite's
// verifier command on the suite base image under the same clean room as
// hidden_tests and reads the outcome in the suite's result format (JUnit
// XML, a reward file or the exit code).
type verifierCommand struct {
	cfg  HiddenTestsConfig
	room *cleanRoom
}

// newVerifierCommand builds the grader and fills config defaults.
func newVerifierCommand(cfg HiddenTestsConfig) *verifierCommand {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	return &verifierCommand{cfg: cfg, room: &cleanRoom{docker: cfg.Docker, logger: cfg.Logger}}
}

// GetName implements schemas.Part.
func (g *verifierCommand) GetName() string { return VerifierCommandType }

// GetVersion implements schemas.Part.
func (g *verifierCommand) GetVersion() string { return verifierCommandVersion }

// VerifierNetwork reports the Docker network of the verifier container, for
// the admission check that the verifier ran with no network.
func (g *verifierCommand) VerifierNetwork() string { return verifierNetwork }

// Grade prepares the workspace as hidden_tests does (pristine repo, diff,
// restore list) with the configured helper image, runs the suite's verifier
// command on the suite base image with the workspace read-only at the suite
// workdir, and scores the result format the suite names. A task with its own
// image is verified in its verifier_image or task image; in snapshot mode the
// verifier runs on the frozen agent container instead, with the run's own
// workspace (applyScenario). Exported service state and the answer are
// mounted at /grader/state and /grader/answer.txt when the run has them. A diff that does not
// apply, a timeout, or a missing, oversized or unparseable result file are
// fails: the reference solution passed this verifier at admission. Only a
// broken required-test list (junit) makes the evaluator invalid; any other
// trouble with docker itself is an error. Grade.ResultFormat is always set.
func (g *verifierCommand) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	in, spec, err := g.inputs(rc, diff)
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	grade := &schemas.Grade{
		ExitCode:      -1,
		SuiteVersion:  rc.Suite.Version,
		GraderVersion: verifierCommandVersion,
		DiffSHA256:    in.diffSHA,
		ResultFormat:  spec.format,
	}
	if in.snapshot != "" {
		grade.Mode = schemas.VerifierSnapshot
	}
	var out resultRead
	res, err := g.room.run(ctx, newGradeJob(expID, runID, in), func(ctx context.Context, container string) error {
		return out.collect(ctx, g.room, container, spec)
	})
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	switch {
	case !res.applied:
		grade.Reason = ReasonPatchDoesNotApply
	case res.timedOut:
		grade.ExitCode = res.exitCode
		grade.Reason = timedOutReason(in.timeout)
	default:
		scoreResult(grade, spec, res.exitCode, out, rc.Task.Verifier.Required)
	}
	g.cfg.Logger.Debug("graded", "run", runID, "format", spec.format, "passed", grade.Passed, "reason", grade.Reason)
	return grade, nil
}

// resultSpec is how a suite's verifier reports.
type resultSpec struct {
	format    string  // schemas.VerifierResult*
	path      string  // the result file inside the verifier container; empty for exit_code
	limit     int64   // largest result file read
	noun      string  // names the file in reasons
	threshold float64 // lowest passing reward
}

// resultRead is what collect found in the stopped verifier container.
type resultRead struct {
	data    []byte // nil when the file is missing or not a regular file
	problem string // why the file cannot be used, when it is too large
}

// inputs validates the run context, the diff and the suite's result
// settings. The configured image only prepares the workspace; the verifier
// always runs on the suite base image.
func (g *verifierCommand) inputs(rc *schemas.RunContext, diff *schemas.Diff) (*gradeInputs, resultSpec, error) {
	in, err := gatherInputs(rc, diff, g.cfg.Image)
	if err != nil {
		return nil, resultSpec{}, err
	}
	if in.image = rc.Suite.BaseImage.Tag; in.image == "" {
		return nil, resultSpec{}, errors.New("suite needs a base image to run its verifier")
	}
	if in.workdir, err = verifierWorkdir(rc.Suite.Workdir); err != nil {
		return nil, resultSpec{}, err
	}
	if err := applyScenario(rc, in); err != nil {
		return nil, resultSpec{}, err
	}
	spec, err := resultSettings(rc.Suite.Verifier)
	if err != nil {
		return nil, resultSpec{}, err
	}
	return in, spec, nil
}

// collect reads the result file, unless the format needs none. An oversized
// file is recorded as a problem, not an error: the verifier ran on
// agent-influenced code, so its output is untrusted input.
func (r *resultRead) collect(ctx context.Context, room *cleanRoom, container string, spec resultSpec) error {
	if spec.path == "" {
		return nil
	}
	data, err := room.readFile(ctx, container, spec.path, spec.limit, "result", spec.noun)
	var tl *tooLargeError
	if errors.As(err, &tl) {
		r.problem = fmt.Sprintf("%s is %v", spec.noun, tl)
		return nil
	}
	r.data = data
	return err
}

// resultSettings resolves a suite's result format, path and threshold and
// refuses settings that do not belong together. A result path must name a
// file directly in /grader/out, the folder the grader creates empty for each
// grade, so no file the agent wrote can stand in for the verifier's result.
// It is pure.
func resultSettings(v schemas.SuiteVerifier) (resultSpec, error) {
	spec := resultSpec{format: v.Result, path: v.ResultPath, threshold: v.PassThreshold}
	if spec.format == "" {
		spec.format = schemas.VerifierResultJUnit
	}
	switch spec.format {
	case schemas.VerifierResultJUnit:
		spec.limit, spec.noun = maxJUnitBytes, "junit report"
		if spec.path == "" {
			spec.path = junitPath
		}
	case schemas.VerifierResultReward:
		spec.limit, spec.noun = maxRewardBytes, "reward file"
		if spec.path == "" {
			spec.path = defaultRewardPath
		}
		if spec.threshold == 0 {
			spec.threshold = defaultPassThreshold
		}
		if math.IsNaN(spec.threshold) || spec.threshold < 0 || spec.threshold > maxPassThreshold {
			return resultSpec{}, fmt.Errorf("pass_threshold %v must be between 0 and %g", spec.threshold, maxPassThreshold)
		}
	case schemas.VerifierResultExitCode:
		if spec.path != "" {
			return resultSpec{}, errors.New("result_path does not apply to result exit_code")
		}
	default:
		return resultSpec{}, fmt.Errorf("verifier result %q is not junit, reward or exit_code", spec.format)
	}
	if spec.format != schemas.VerifierResultReward && spec.threshold != 0 {
		return resultSpec{}, errors.New("pass_threshold applies only to result reward")
	}
	if spec.path != "" && (path.Clean(spec.path) != spec.path || path.Dir(spec.path) != resultDir || !resultNameRe.MatchString(path.Base(spec.path))) {
		return resultSpec{}, fmt.Errorf("result_path %q must name a file directly in %s", spec.path, resultDir)
	}
	return spec, nil
}

// verifierWorkdir is where the verifier sees the workspace: the suite workdir,
// where the agent worked, so paths in the verifier match the agent's view.
// It is pure.
func verifierWorkdir(wd string) (string, error) {
	if wd == "" {
		return defaultWorkdir, nil
	}
	if !path.IsAbs(wd) || path.Clean(wd) != wd || wd == "/" {
		return "", fmt.Errorf("suite workdir %q must be a clean absolute path", wd)
	}
	for _, r := range reservedMounts {
		if wd == r || strings.HasPrefix(wd, r+"/") {
			return "", fmt.Errorf("suite workdir %q is inside %s, which the verifier container reserves", wd, r)
		}
	}
	return wd, nil
}

// scoreResult fills grade from the verifier's exit code and result file in
// the suite's format. It is pure.
func scoreResult(grade *schemas.Grade, spec resultSpec, exitCode int, out resultRead, required []string) {
	grade.ExitCode = exitCode
	if out.problem != "" {
		grade.Reason = fmt.Sprintf("%s: %s (exit %d)", ReasonTestRunBroken, out.problem, exitCode)
		return
	}
	switch spec.format {
	case schemas.VerifierResultJUnit:
		scoreGrade(grade, exitCode, out.data, required)
	case schemas.VerifierResultReward:
		scoreReward(grade, exitCode, out.data, spec.threshold)
	default:
		scoreExitCode(grade, exitCode)
	}
}

// scoreReward passes when the reward the verifier wrote reaches threshold.
// The exit code is recorded but not required to be 0: a Harbor-style
// test.sh may exit non-zero after writing a partial reward. A missing, empty
// or unparseable file is a broken test run, never unknown, for the same
// reason as a missing JUnit report. It is pure.
func scoreReward(grade *schemas.Grade, exitCode int, data []byte, threshold float64) {
	grade.ExitCode = exitCode
	if data == nil {
		grade.Reason = fmt.Sprintf("%s: no reward file (exit %d)", ReasonTestRunBroken, exitCode)
		return
	}
	reward, dims, err := parseReward(data)
	if err != nil {
		grade.Reason = fmt.Sprintf("%s: %s (exit %d)", ReasonTestRunBroken, truncate(err.Error(), maxReasonQuote), exitCode)
		return
	}
	grade.Reward = &reward
	grade.Dimensions = dims
	grade.Passed = reward >= threshold
	if grade.Passed {
		grade.Reason = fmt.Sprintf("passed (reward %s)", formatNumber(reward))
		return
	}
	grade.Reason = fmt.Sprintf("reward %s below pass threshold %s", formatNumber(reward), formatNumber(threshold))
}

// scoreExitCode passes on exit 0. It is pure.
func scoreExitCode(grade *schemas.Grade, exitCode int) {
	grade.ExitCode = exitCode
	grade.Passed = exitCode == 0
	if grade.Passed {
		grade.Reason = "passed (exit 0)"
		return
	}
	grade.Reason = fmt.Sprintf("exit %d", exitCode)
}

// parseReward reads a reward file: a bare decimal number (Harbor's
// reward.txt) or the JSON object {"reward": n, "dimensions": {"name": n}}.
// Unknown keys, null values, non-finite numbers, more than 32 dimensions and
// dimension names outside [a-z0-9_]{1,40} are refused, so a typo or a
// half-written file never passes for a score. It is pure.
func parseReward(data []byte) (float64, map[string]float64, error) {
	s := bytes.TrimSpace(bytes.TrimPrefix(data, utf8BOM))
	if len(s) == 0 {
		return 0, nil, errors.New("empty reward file")
	}
	if s[0] != '{' {
		r, err := parseRewardNumber(string(s))
		return r, nil, err
	}
	var doc struct {
		Reward     *float64            `json:"reward"`
		Dimensions map[string]*float64 `json:"dimensions"`
	}
	dec := json.NewDecoder(bytes.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return 0, nil, fmt.Errorf("parse reward json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return 0, nil, errors.New("parse reward json: data after the object")
	}
	if doc.Reward == nil {
		return 0, nil, errors.New("reward json has no reward")
	}
	if len(doc.Dimensions) > maxDimensions {
		return 0, nil, fmt.Errorf("reward json has %d dimensions, over the limit of %d", len(doc.Dimensions), maxDimensions)
	}
	var dims map[string]float64
	for name, v := range doc.Dimensions {
		if !dimensionRe.MatchString(name) {
			return 0, nil, fmt.Errorf("dimension name %q is not [a-z0-9_]{1,40}", truncate(name, 48))
		}
		if v == nil {
			return 0, nil, fmt.Errorf("dimension %s is null", name)
		}
		if dims == nil {
			dims = make(map[string]float64, len(doc.Dimensions))
		}
		dims[name] = *v
	}
	return *doc.Reward, dims, nil
}

// parseRewardNumber reads one decimal number. NaN, infinities, hex floats
// and values out of float64 range are refused.
func parseRewardNumber(s string) (float64, error) {
	if !decimalRe.MatchString(s) {
		return 0, fmt.Errorf("reward %q is not a decimal number", truncate(s, 40))
	}
	r, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(r, 0) || math.IsNaN(r) {
		return 0, fmt.Errorf("reward %q is out of range", truncate(s, 40))
	}
	return r, nil
}

// formatNumber prints a reward or threshold in its shortest exact form.
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// truncate shortens untrusted text quoted in a reason to n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}
