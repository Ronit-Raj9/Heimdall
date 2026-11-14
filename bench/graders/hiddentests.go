// Package graders holds the bench's Grader implementations. A grader decides
// pass or fail from the exported diff alone, in fresh containers, never from
// anything the agent's own container reports.
package graders

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// HiddenTestsType is the part name suites use to select this grader.
const HiddenTestsType = "hidden_tests"

const (
	hiddenTestsVersion = "1.0.0"
	maxJUnitBytes      = 16 << 20
	junitPath          = "/grader/out/junit.xml"
)

// HiddenTestsConfig configures the clean-room graders, hidden_tests and
// verifier_command. Image runs the trusted preparation step (it needs git and
// sh); hidden_tests also runs its verifier on it. When empty the suite base
// image is used. verifier_command always runs its verifier on the suite base
// image, so a suite in any language keeps its own toolchain.
type HiddenTestsConfig struct {
	Image  string
	Logger schemas.Logger
	Docker utils.Docker
}

// hiddenTests reproduces benchmarks/coding-core/tools/checktask.sh clean
// grading in Go.
type hiddenTests struct {
	cfg  HiddenTestsConfig
	room *cleanRoom
}

// newHiddenTests builds the grader and fills config defaults.
func newHiddenTests(cfg HiddenTestsConfig) *hiddenTests {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	return &hiddenTests{cfg: cfg, room: &cleanRoom{docker: cfg.Docker, logger: cfg.Logger}}
}

// GetName implements schemas.Part.
func (g *hiddenTests) GetName() string { return HiddenTestsType }

// GetVersion implements schemas.Part.
func (g *hiddenTests) GetVersion() string { return hiddenTestsVersion }

// VerifierNetwork reports the Docker network of the verifier container, for
// the admission check that the verifier ran with no network.
func (g *hiddenTests) VerifierNetwork() string { return verifierNetwork }

// Grade copies the pristine task repo into a fresh volume, applies the diff,
// restores the suite's restore paths from the pristine repo, and runs the
// suite's verifier command with no network, the workspace read-only and the
// hidden tests mounted read-only. A diff that does not apply is a fail. No
// JUnit report, zero tests or a verifier timeout are fails too, with reason
// ReasonTestRunBroken. Every container and volume it creates is removed
// before it returns.
func (g *hiddenTests) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	in, err := g.inputs(rc, diff)
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	grade := &schemas.Grade{
		ExitCode:      -1,
		SuiteVersion:  rc.Suite.Version,
		GraderVersion: hiddenTestsVersion,
		DiffSHA256:    in.diffSHA,
	}
	var report []byte
	res, err := g.room.run(ctx, newGradeJob(expID, runID, in), func(ctx context.Context, container string) error {
		var rerr error
		report, rerr = g.room.readFile(ctx, container, junitPath, maxJUnitBytes, "junit", "junit report")
		return rerr
	})
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	if !res.applied {
		grade.Reason = ReasonPatchDoesNotApply
		return grade, nil
	}
	if res.timedOut {
		grade.ExitCode = res.exitCode
		grade.Reason = timedOutReason(in.timeout)
		return grade, nil
	}
	scoreGrade(grade, res.exitCode, report, rc.Task.Verifier.Required)
	g.cfg.Logger.Debug("graded", "run", runID, "passed", grade.Passed, "reason", grade.Reason)
	return grade, nil
}

// inputs validates the run context and the diff for this grader: the
// configured image, or else the suite base image, prepares and verifies.
func (g *hiddenTests) inputs(rc *schemas.RunContext, diff *schemas.Diff) (*gradeInputs, error) {
	return gatherInputs(rc, diff, g.cfg.Image)
}

// scoreGrade fills grade from the verifier exit code and its JUnit report,
// following checktask.sh: pass only with exit 0, more than zero tests and no
// failures, errors or skips, plus every required test run and passed.
//
// The reference passed this verifier at admission, so when a cleanly applied
// patch leaves no report, an unreadable report or zero tests, the patch broke
// the test run and the grade is a fail. Otherwise an agent could turn a 0 into
// an unknown by crashing pytest. Only a broken required-test list, a task
// configuration problem, is an invalid evaluator. It is pure.
func scoreGrade(grade *schemas.Grade, exitCode int, junitXML []byte, required []string) {
	grade.ExitCode = exitCode
	if len(bytes.TrimSpace(junitXML)) == 0 {
		grade.Reason = fmt.Sprintf("%s: no junit report (exit %d)", ReasonTestRunBroken, exitCode)
		return
	}
	r, err := parseJUnit(junitXML)
	if err != nil {
		grade.Reason = fmt.Sprintf("%s: %v", ReasonTestRunBroken, err)
		return
	}
	grade.TestsRan, grade.Failures, grade.Errors, grade.Skipped = r.Tests, r.Failures, r.Errors, r.Skipped
	for _, c := range r.Cases {
		if c.Failed || c.Errored {
			grade.FailedTests = append(grade.FailedTests, c.ID())
		}
	}
	if r.Tests == 0 {
		grade.Reason = fmt.Sprintf("%s: no tests ran (exit %d)", ReasonTestRunBroken, exitCode)
		return
	}
	req, err := checkRequired(r, required)
	if err != nil {
		grade.InvalidEvaluator = true
		grade.Reason = err.Error()
		return
	}
	grade.MissingRequired = req.Missing
	var why []string
	if exitCode != 0 {
		why = append(why, fmt.Sprintf("exit %d", exitCode))
	}
	for _, c := range []struct {
		n    int
		name string
	}{{r.Failures, "failures"}, {r.Errors, "errors"}, {r.Skipped, "skipped"}} {
		if c.n > 0 {
			why = append(why, fmt.Sprintf("%d %s", c.n, c.name))
		}
	}
	if len(req.Missing) > 0 {
		why = append(why, "required tests did not run: "+strings.Join(req.Missing, ", "))
	}
	if len(req.Failed) > 0 {
		why = append(why, "required tests failed: "+strings.Join(req.Failed, ", "))
	}
	grade.Passed = len(why) == 0
	if grade.Passed {
		grade.Reason = fmt.Sprintf("passed (%d tests)", r.Tests)
		return
	}
	grade.Reason = strings.Join(why, "; ")
}
