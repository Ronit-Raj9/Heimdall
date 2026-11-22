// Package validate decides which tests of a candidate pull request are
// FAIL_TO_PASS and PASS_TO_PASS (audit/RESEARCH_ISSUE2ENV.md section 7.3),
// drops flaky and error to pass tests, and asks the org's model through the
// gateway for the quality checks (leak, clarity, interface note, near-miss
// wrong patches). The output is an issuebench.Validation.
//
// The test runs are deterministic and use no model: each candidate runs in
// three states (base, base plus the PR's test patch, and that plus the fix),
// every state three times in a fresh container, and the gold state once
// more with the test order reversed. A test whose status changes between
// runs is flaky; it is removed, and a flaky FAIL_TO_PASS test drops the
// task.
package validate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// State is the tree a run tests.
type State string

const (
	// StateBase is the base commit as built.
	StateBase State = "base"
	// StateTests is the base commit with the PR's test patch applied.
	StateTests State = "base+tests"
	// StateGold is StateTests with the PR's fix applied too.
	StateGold State = "gold"
)

// Stop reasons a Validation carries. "" means the task is valid.
const (
	StopPatchFailed      = "patch_failed"       // the test patch or the fix does not apply at base
	StopTimeout          = "timeout"            // a test run passed its time limit
	StopNoResults        = "no_results"         // the gold run produced no test results
	StopNoFailToPass     = "no_fail_to_pass"    // no test fails before the fix and passes after it
	StopErrorToPass      = "error_to_pass"      // the only new passes come from tests that could not run before the fix
	StopPassToFail       = "pass_to_fail"       // the fix breaks a test that passed before it
	StopFlakyFailToPass  = "flaky_fail_to_pass" // a FAIL_TO_PASS test changed status between runs
	StopLeak             = "leak"               // the issue text states the fix
	StopUnclear          = "unclear"            // the issue does not say what to build (clarity 3)
	StopModelBudget      = "model_budget"       // the model checks ran out of budget
	StopModelCheckFailed = "model_check_failed" // the model gave no valid answer for a check
	StopUnsupported      = "unsupported_language"
)

// ErrPatchApply is what a Runner returns (wrapped) when a patch does not
// apply. ErrTimeout is what it returns when the run passes its limit.
var (
	ErrPatchApply = errors.New("patch does not apply")
	ErrTimeout    = errors.New("test run timed out")
)

// Run is one test run's request.
type Run struct {
	State    State
	Round    int      // 0, 1, 2; the reversed run is round 3
	Reversed bool     // tests are listed in reverse order
	Tests    []string // test files or ids, in the order the command uses
	Command  string   // Language.TestCommand(Tests)
}

// Runner runs one test command in a fresh container of the candidate's
// environment, at the run's state, and returns the command's output for
// Language.ParseResults. A failing test is not an error; a patch that does
// not apply is ErrPatchApply and a run past its limit is ErrTimeout.
type Runner interface {
	Run(ctx context.Context, env *issuebench.EnvSpec, c *issuebench.Candidate, r Run) ([]byte, error)
}

// Options configures one validation.
type Options struct {
	// Rounds is how many times each state runs; 0 means 3.
	Rounds int
	// WiderTests are more test files run at every state (the test files of
	// the modules the fix touches, or the whole suite): they widen
	// PASS_TO_PASS so fewer wrong patches pass.
	WiderTests []string
	// KeepErrorToPass keeps tests that could not run before the fix (an
	// import or collection error) as FAIL_TO_PASS, with an interface note
	// naming what they need. Off by default, as SWE-Factory drops them.
	KeepErrorToPass bool
	// Checks runs the model checks; nil skips them (Checks.Clear stays false
	// so a person reads the issue).
	Checks *Checker
	// Lang overrides the language looked up by Candidate.Language.
	Lang issuebench.Language
}

// Validator validates candidates with one runner.
type Validator struct {
	Runner Runner
}

// LanguageFor returns the registered language with the given name.
func LanguageFor(name string) issuebench.Language {
	for _, l := range issuebench.Languages() {
		if l.Name() == name {
			return l
		}
	}
	return nil
}

// Validate runs the candidate's tests at every state and returns what it
// found. A task problem (no FAIL_TO_PASS, a flaky one, a leak) is a Stop on
// the Validation; an error is returned only when validation itself could not
// run (cancelled, the runner broke, output that does not parse).
func (v *Validator) Validate(ctx context.Context, env *issuebench.EnvSpec, c issuebench.Candidate, opts Options) (*issuebench.Validation, error) {
	out := &issuebench.Validation{Candidate: c, FailToPass: []string{}, PassToPass: []string{}}
	if v.Runner == nil {
		return nil, errors.New("validate: no runner")
	}
	lang := opts.Lang
	if lang == nil {
		lang = LanguageFor(c.Language)
	}
	if lang == nil {
		out.Stop = StopUnsupported
		return out, nil
	}
	rounds := opts.Rounds
	if rounds <= 0 {
		rounds = 3
	}
	prTests := testFiles(c)
	created := createdFiles(c.TestPatch)
	var baseTests []string
	for _, t := range prTests {
		if !created[t] {
			baseTests = append(baseTests, t)
		}
	}
	baseTests = appendNew(baseTests, opts.WiderTests...)
	allTests := appendNew(slices.Clone(prTests), opts.WiderTests...)

	obs := map[State][]map[string]issuebench.TestStatus{}
	var reversed map[string]issuebench.TestStatus
	run := func(st State, round int, rev bool) (map[string]issuebench.TestStatus, string, error) {
		tests := allTests
		if st == StateBase {
			tests = baseTests
		}
		if len(tests) == 0 {
			return map[string]issuebench.TestStatus{}, "", nil
		}
		if rev {
			tests = slices.Clone(tests)
			slices.Reverse(tests)
		}
		r := Run{State: st, Round: round, Reversed: rev, Tests: tests, Command: lang.TestCommand(tests)}
		raw, err := v.Runner.Run(ctx, env, &c, r)
		switch {
		case err == nil:
		case ctx.Err() != nil:
			return nil, "", ctx.Err()
		case errors.Is(err, ErrPatchApply):
			return nil, StopPatchFailed, nil
		case errors.Is(err, ErrTimeout):
			return nil, StopTimeout, nil
		default:
			return nil, "", fmt.Errorf("run %s round %d: %w", st, round+1, err)
		}
		res, err := lang.ParseResults(raw)
		if err != nil {
			return nil, "", fmt.Errorf("parse %s round %d results: %w", st, round+1, err)
		}
		return res, "", nil
	}
	for round := 0; round < rounds; round++ {
		for _, st := range []State{StateBase, StateTests, StateGold} {
			res, stop, err := run(st, round, false)
			if err != nil {
				return nil, err
			}
			if stop != "" {
				out.Stop = stop
				return out, nil
			}
			obs[st] = append(obs[st], res)
		}
		if round == 0 {
			// One round is enough to see a task with nothing to validate;
			// the reruns are only spent on tasks that may be kept.
			if len(obs[StateGold][0]) == 0 {
				out.Stop = StopNoResults
				return out, nil
			}
			if !anyNewPass(obs[StateTests][0], obs[StateGold][0]) {
				out.Stop = StopNoFailToPass
				return out, nil
			}
		}
	}
	res, stop, err := run(StateGold, rounds, true)
	if err != nil {
		return nil, err
	}
	if stop != "" {
		out.Stop = stop
		return out, nil
	}
	reversed = res

	cls := classify(obs, reversed)
	out.FailToPass = cls.failToPass
	out.PassToPass = cls.passToPass
	out.Flaky = cls.flaky
	switch {
	case len(cls.flakyF2P) > 0:
		out.Stop = StopFlakyFailToPass
		return out, nil
	case len(cls.passToFail) > 0:
		out.Stop = StopPassToFail
		return out, nil
	}
	if opts.KeepErrorToPass {
		out.FailToPass = sortedUnion(out.FailToPass, cls.errorToPass)
	}
	if len(out.FailToPass) == 0 {
		out.Stop = StopNoFailToPass
		if len(cls.errorToPass) > 0 {
			out.Stop = StopErrorToPass
		}
		return out, nil
	}
	keptE2P := opts.KeepErrorToPass && len(cls.errorToPass) > 0
	if opts.Checks == nil {
		if keptE2P {
			out.Checks.InterfaceNote = interfaceNoteFallback(newSymbols(c.FixPatch, c.TestPatch))
		}
		if score, hits := leakScore(c.IssueTitle+"\n"+c.IssueBody, c.FixPatch); hits >= 2 && score >= 0.5 {
			out.Checks.Leak = true
			out.Stop = StopLeak
		}
		return out, nil
	}
	checks, stop, err := opts.Checks.Run(ctx, c, keptE2P)
	if err != nil {
		return nil, err
	}
	out.Checks = checks
	out.Stop = stop
	return out, nil
}

// classified is what the runs say about each test.
type classified struct {
	failToPass, passToPass, errorToPass []string
	flaky, flakyF2P, passToFail         []string
}

const missing issuebench.TestStatus = ""

// classify reads the runs. A test's status in a state is stable when every
// run of that state gave the same one (missing counts as a status); the
// reversed gold run must agree with the gold runs too.
func classify(obs map[State][]map[string]issuebench.TestStatus, reversed map[string]issuebench.TestStatus) classified {
	ids := map[string]bool{}
	for _, runs := range obs {
		for _, r := range runs {
			for id := range r {
				ids[id] = true
			}
		}
	}
	for id := range reversed {
		ids[id] = true
	}
	var c classified
	for _, id := range sortedKeys(ids) {
		b, bOK := stable(obs[StateBase], id)
		t, tOK := stable(obs[StateTests], id)
		g, gOK := stable(obs[StateGold], id)
		if gOK && reversed != nil && reversed[id] != g {
			gOK = false
		}
		if !bOK || !tOK || !gOK {
			c.flaky = append(c.flaky, id)
			// Would it count as FAIL_TO_PASS in any run? Then the task
			// cannot be trusted.
			if seen(obs[StateGold], id, issuebench.TestPass) && !always(obs[StateTests], id, issuebench.TestPass) {
				c.flakyF2P = append(c.flakyF2P, id)
			}
			continue
		}
		switch g {
		case issuebench.TestPass:
			switch t {
			case issuebench.TestFail:
				c.failToPass = append(c.failToPass, id)
			case issuebench.TestPass:
				c.passToPass = append(c.passToPass, id)
			default: // error, skip or missing: could not run before the fix
				c.errorToPass = append(c.errorToPass, id)
			}
		case issuebench.TestFail, issuebench.TestError:
			if t == issuebench.TestPass || b == issuebench.TestPass {
				c.passToFail = append(c.passToFail, id)
			}
		}
	}
	return c
}

// stable returns the status every run gave id, and false when runs differ.
func stable(runs []map[string]issuebench.TestStatus, id string) (issuebench.TestStatus, bool) {
	if len(runs) == 0 {
		return missing, true
	}
	first := runs[0][id]
	for _, r := range runs[1:] {
		if r[id] != first {
			return first, false
		}
	}
	return first, true
}

func seen(runs []map[string]issuebench.TestStatus, id string, s issuebench.TestStatus) bool {
	for _, r := range runs {
		if r[id] == s {
			return true
		}
	}
	return false
}

func always(runs []map[string]issuebench.TestStatus, id string, s issuebench.TestStatus) bool {
	for _, r := range runs {
		if r[id] != s {
			return false
		}
	}
	return len(runs) > 0
}

// anyNewPass reports whether some test passes at gold and did not pass at
// base plus tests.
func anyNewPass(tests, gold map[string]issuebench.TestStatus) bool {
	for id, s := range gold {
		if s == issuebench.TestPass && tests[id] != issuebench.TestPass {
			return true
		}
	}
	return false
}

// testFiles are the PR's test files: Candidate.TestFiles, or the test
// patch's files when the scan left the list empty. Files the test patch
// deletes are left out.
func testFiles(c issuebench.Candidate) []string {
	deleted := map[string]bool{}
	var fromPatch []string
	for _, f := range splitPatch(c.TestPatch) {
		if f.Deleted {
			deleted[f.Path] = true
			continue
		}
		fromPatch = append(fromPatch, f.Path)
	}
	src := c.TestFiles
	if len(src) == 0 {
		src = fromPatch
	}
	var out []string
	for _, f := range src {
		if !deleted[f] {
			out = appendNew(out, f)
		}
	}
	return out
}

func appendNew(list []string, items ...string) []string {
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it != "" && !slices.Contains(list, it) {
			list = append(list, it)
		}
	}
	return list
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedUnion(a, b []string) []string {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		m[s] = true
	}
	return sortedKeys(m)
}

// interfaceNoteFallback is the interface note without the model: the
// names and signatures the tests use that the fix adds.
func interfaceNoteFallback(symbols []string) string {
	if len(symbols) == 0 {
		return ""
	}
	return "The tests use these names, which do not exist yet: " + strings.Join(symbols, "; ") + "."
}
