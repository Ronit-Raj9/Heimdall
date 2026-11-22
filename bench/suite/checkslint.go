package suite

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskchecks"
	"github.com/maximhq/bifrost/bench/utils"
)

// ChecksGrader is the grader that reads a task's checks.yaml.
const ChecksGrader = "checks"

// lintChecksTask checks a task's declarative checks.yaml: it parses and
// validates; it is read (the suite's grader is checks); every state file
// it names is an export of one of the task's services; a reply rubric has
// a pinned judge in a judged suite; tool checks have a tool service to
// record calls from. A checks-graded task with neither a checks.yaml nor
// a real verifier command is graded by nothing.
func lintChecksTask(s *schemas.Suite, t *schemas.Task) []Problem {
	root := s.Root
	var out []Problem
	file := taskchecks.TaskFile(t)
	taskYAML := rel(root, t.Dir+"/"+TaskFile)
	if file == "" {
		if s.Grader == ChecksGrader && noVerifierCommand(s) {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityError,
				Message: fmt.Sprintf("verifier: the suite grades with checks, but this task has no %s in %s and the suite's verifier command does nothing", taskchecks.FileName, t.Verifier.Tests)})
		}
		return out
	}
	path := rel(root, file)
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: path, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if s.Grader != ChecksGrader {
		add(SeverityWarning, "%s is read only by grader checks; this suite uses %s, so these checks never run (set grader: checks in suite.yaml)", taskchecks.FileName, s.Grader)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		add(SeverityError, "%v", err)
		return out
	}
	spec, err := taskchecks.Parse(b)
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			add(SeverityError, "%s", line)
		}
		return out
	}
	services := utils.TaskServices(s, t)
	exports := map[string]bool{}
	for _, sv := range services {
		for _, e := range sv.Export {
			exports[sv.ID+"/"+e.ID] = true
		}
	}
	for _, f := range spec.Files() {
		if !exports[f] {
			add(SeverityError, "state file %s: no service of this task exports it (exports: %s)", f, orNoneList(sortedSet(exports)))
		}
	}
	if spec.Rubric() != nil {
		switch {
		case utils.Grading(s) != schemas.GradingJudged:
			add(SeverityError, "reply_rubric is graded by a judge model, so the suite must declare grading: judged and pin a judge")
		case s.Judge == nil || s.Judge.Provider == "" || s.Judge.Model == "":
			add(SeverityError, "reply_rubric needs the suite's judge (provider and model) pinned")
		}
	}
	toolChecks := false
	for _, c := range spec.Checks {
		k := c.Kind()
		toolChecks = toolChecks || k == taskchecks.KindToolCalled || k == taskchecks.KindToolNotCalled || k == taskchecks.KindCallOrder
	}
	if toolChecks && !hasToolService(services) && len(utils.ExternalAPIs(s, t)) == 0 {
		add(SeverityWarning, "tool checks: the task has no tool or MCP service and no external API, so the tool proxy records no calls for them")
	}
	if t.Controls.Reference == "" {
		add(SeverityWarning, "controls.reference: empty; the readiness checks cannot prove these checks pass a correct run")
	}
	if len(t.Controls.Mutants) == 0 && t.Controls.Empty == "" {
		add(SeverityWarning, "controls: no mutant and no empty control; nothing proves these checks fail a wrong run")
	}
	return out
}

func hasToolService(services []schemas.ServiceSpec) bool {
	for _, sv := range services {
		if sv.Role == "tool" || sv.Role == "mcp" {
			return true
		}
	}
	return false
}

// noVerifierCommand reports a suite command that grades nothing (true, or
// nothing at all).
func noVerifierCommand(s *schemas.Suite) bool {
	c := s.Verifier.Command
	return len(c) == 0 || (len(c) == 1 && (c[0] == "true" || c[0] == "/bin/true"))
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func orNoneList(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

// referenceMissedChecks is why a passing reference control of a checks
// graded task still fails admission: a correct run must pass every check
// of checks.yaml, not only reach the pass threshold, or a check is wrong
// (or the threshold hides a broken one). "" when there is nothing to say.
func referenceMissedChecks(grader, control string, g *schemas.Grade) string {
	if grader != ChecksGrader || g == nil || !g.Passed || !(strings.HasPrefix(control, "reference_") || strings.HasPrefix(control, "honest_original_")) {
		return ""
	}
	var missed []string
	for k, v := range g.Dimensions {
		if id, ok := strings.CutPrefix(k, "check."); ok && v < 1 {
			missed = append(missed, id)
		}
	}
	if len(missed) == 0 {
		return ""
	}
	slices.Sort(missed)
	return "the reference reaches the pass threshold but fails check " + strings.Join(missed, ", ") + "; a correct run must pass every check (fix the check or the reference)"
}
