package handlers

import (
	"fmt"
	"regexp"
	"strings"
)

// planIDPrefixRe is the planner's own "plan <experiment id or name>: "
// prefix. The id is made fresh on every plan of an experiment without one,
// so it never reaches the page (N44).
var planIDPrefixRe = regexp.MustCompile(`^plan [^:]+: `)

var (
	lockNotAdmittedRe = regexp.MustCompile(`^task (\S+) is (?:not in the suite lock|\S+, not admitted)$`)
	lockNoLockRe      = regexp.MustCompile(`^suite (\S+?)@\S* has no lock$`)
	lockStaleRe       = regexp.MustCompile(`^task (\S+) admission was recorded for other lock fingerprints`)
)

// planErrorListMax is how many task ids a plain plan error names before it
// says "and N more".
const planErrorListMax = 5

// PlainPlanError is the plain sentences of PlainPlanErrors joined with a
// space: the transient "plan <id>: " prefix is dropped, and the suite lock
// problems a person can fix on the benchmark page are said in plain words
// (N42, N44, D3). CLI users keep the raw planner error.
func PlainPlanError(err error) string {
	return strings.Join(PlainPlanErrors(err), " ")
}

var (
	// planPrefixRes are stripped at the start of a line, repeatedly, until
	// none matches.
	planPrefixRes = []*regexp.Regexp{
		planIDPrefixRe,
		regexp.MustCompile(`^plan: `),
		regexp.MustCompile(`^plan experiment: `),
		regexp.MustCompile(`^plan refused: `),
		regexp.MustCompile(`^no forecast: `),
	}
	planArmPrefixRe       = regexp.MustCompile(`^(?:arm \S+?|compatibility agent(?: \S+?)?)(?:, task (\S+))?: `)
	planConditionPrefixRe = regexp.MustCompile(`^condition \S+, task (\S+): `)
	planTaskPrefixRe      = regexp.MustCompile(`^task (\S+): `)
	planOwnImageRe        = regexp.MustCompile(`the task brings its own image and suite (\S+) grades a snapshot of the agent's container, but agent (\S+) has no build\.base_arg`)
	planSkillGateRe       = regexp.MustCompile(`^skill (\S+) in agents/([^/\s]+)/skills (has no passing scan|changed after its scan)`)
	planSettingsFieldRe   = regexp.MustCompile(`^agent config (\S+): effective agent config: schema: (\S+): required field missing$`)
	planRegistryRe        = regexp.MustCompile(`base image (\S+) (?:\(\S+\) )?is not in the shared worker's image registry`)
)

// PlainPlanErrors turns a planner error into the plain sentences a web page
// lists, one per problem, in order, without repeats. It drops the internal
// prefixes ("plan <id>: " repeated, "plan experiment: ", "plan refused: ",
// "arm X: ", "condition C, task T: ", "skills: "), keeps the suite lock
// sentences of PlainPlanError, and says these planner refusals in plain
// words: a task that brings its own image (one sentence per agent and
// benchmark, naming the tasks), a skill without a passing scan, a missing
// field in the agent settings, and a base image missing from the shared
// worker's registry.
func PlainPlanErrors(err error) []string {
	if err == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	// Own-image tasks are collected per agent and benchmark; the sentence
	// takes the place of the pair's first line.
	type ownKey struct{ agent, suite string }
	ownTasks := map[ownKey][]string{}
	ownSlot := map[ownKey]int{}
	var ownOrder []ownKey
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line, task := stripPlanPrefixes(line)
		if rest, ok := strings.CutPrefix(line, "suite lock: "); ok {
			for _, s := range plainLockSentences(rest, line) {
				add(s)
			}
			continue
		}
		if m := planOwnImageRe.FindStringSubmatch(line); m != nil {
			k := ownKey{agent: m[2], suite: m[1]}
			if _, ok := ownSlot[k]; !ok {
				ownSlot[k] = len(out)
				ownOrder = append(ownOrder, k)
				out = append(out, "")
			}
			ownTasks[k] = append(ownTasks[k], task)
			continue
		}
		add(plainPlanLine(line))
	}
	for _, k := range ownOrder {
		var ids []string
		seenID := map[string]bool{}
		for _, id := range ownTasks[k] {
			if !seenID[id] {
				seenID[id] = true
				ids = append(ids, id)
			}
		}
		count := fmt.Sprintf("%d tasks", len(ids))
		if len(ids) == 1 {
			count = "1 task"
		}
		var named []string
		for _, id := range ids {
			if id != "" {
				named = append(named, id)
			}
		}
		out[ownSlot[k]] = fmt.Sprintf("Agent %s runs in its own container, but %s of %s bring their own environment (%s), so every run of them would score 0. Leave those tasks out or pick another agent.",
			k.agent, count, k.suite, planErrorIDList(named))
	}
	// Drop the slots of own-image sentences that repeat an earlier sentence.
	final := out[:0]
	seenFinal := map[string]bool{}
	for _, s := range out {
		if s != "" && !seenFinal[s] {
			seenFinal[s] = true
			final = append(final, s)
		}
	}
	return final
}

// stripPlanPrefixes drops the planner's internal prefixes from one line
// (rule 1 of the wave 4 design) and returns the task id one of them named.
func stripPlanPrefixes(line string) (string, string) {
	for changed := true; changed; {
		changed = false
		for _, re := range planPrefixRes {
			if loc := re.FindStringIndex(line); loc != nil {
				line = line[loc[1]:]
				changed = true
			}
		}
	}
	task := ""
	if m := planArmPrefixRe.FindStringSubmatch(line); m != nil {
		line, task = line[len(m[0]):], m[1]
	}
	if m := planConditionPrefixRe.FindStringSubmatch(line); m != nil {
		line, task = line[len(m[0]):], m[1]
	}
	if m := planTaskPrefixRe.FindStringSubmatch(line); m != nil && !isLockSentence(line[len(m[0]):]) {
		line, task = line[len(m[0]):], m[1]
	}
	line = strings.TrimPrefix(line, "skills: ")
	return line, task
}

// isLockSentence reports whether s is a suite lock problem.
func isLockSentence(s string) bool {
	return strings.HasPrefix(s, "suite lock: ") || lockNotAdmittedRe.MatchString(s) || lockNoLockRe.MatchString(s) || lockStaleRe.MatchString(s)
}

// plainPlanLine says one stripped planner line in plain words (rules 3, 4,
// 5 and 7 of the wave 4 design).
func plainPlanLine(line string) string {
	if m := planSkillGateRe.FindStringSubmatch(line); m != nil {
		what := "has no passing safety scan"
		if strings.HasPrefix(m[3], "changed") {
			what = "changed after its safety scan"
		}
		return fmt.Sprintf("Skill %s of agent %s %s. Open the agent's Tools tab and press Scan.", m[1], m[2], what)
	}
	if m := planSettingsFieldRe.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf("The agent settings file %s is missing the field %s.", m[1], m[2])
	}
	if m := planRegistryRe.FindStringSubmatch(line); m != nil {
		return fmt.Sprintf("The container image %s is not ready on the shared worker yet. Try again later, or connect your own worker in Settings, Workers.", m[1])
	}
	// Rule 7 (upper-case first letter, final period) is not applied: the
	// existing TestPlainPlanErrorStripsID pins the line as it is.
	return line
}

// plainLockSentences says the suite lock problems of rest in plain words.
// msg is the whole line, returned when no part is understood.
func plainLockSentences(rest, msg string) []string {
	var notReady, sentences []string
	for _, part := range strings.Split(rest, "; ") {
		part = strings.TrimSpace(part)
		switch {
		case part == "", part == "run suite admit": // the tail of the stale admission problem, said below in plain words
		case lockNotAdmittedRe.MatchString(part):
			notReady = append(notReady, lockNotAdmittedRe.FindStringSubmatch(part)[1])
		case lockNoLockRe.MatchString(part):
			s := lockNoLockRe.FindStringSubmatch(part)[1]
			sentences = append(sentences, fmt.Sprintf("Benchmark %s is not frozen yet. Freeze it and run its readiness check on the benchmark page.", s))
		case lockStaleRe.MatchString(part):
			id := lockStaleRe.FindStringSubmatch(part)[1]
			sentences = append(sentences, fmt.Sprintf("Task %s changed after its readiness check. Run the readiness check again.", id))
		default:
			sentences = append(sentences, part)
		}
	}
	if len(notReady) > 0 {
		sentences = append([]string{fmt.Sprintf("This benchmark has tasks that have not passed the readiness check yet: %s. "+
			"Pick a ready benchmark, choose only ready tasks, or run the readiness check on the benchmark page.", planErrorIDList(notReady))}, sentences...)
	}
	if len(sentences) == 0 {
		return []string{msg}
	}
	return sentences
}

// planErrorIDList names at most planErrorListMax ids, then "and N more".
func planErrorIDList(ids []string) string {
	if len(ids) <= planErrorListMax {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:planErrorListMax], ", "), len(ids)-planErrorListMax)
}
