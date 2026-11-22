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

// PlainPlanError turns a planner error into the plain sentences the web
// pages show (N42, N44, D3): the transient "plan <id>: " prefix is dropped,
// and the suite lock problems a person can fix on the benchmark page are
// said in plain words. Anything else is the planner's text without the id
// prefix. CLI users keep the raw planner error.
func PlainPlanError(err error) string {
	if err == nil {
		return ""
	}
	msg := planIDPrefixRe.ReplaceAllString(strings.TrimSpace(err.Error()), "")
	rest, ok := strings.CutPrefix(msg, "suite lock: ")
	if !ok {
		return msg
	}
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
		return msg
	}
	return strings.Join(sentences, " ")
}

// planErrorIDList names at most planErrorListMax ids, then "and N more".
func planErrorIDList(ids []string) string {
	if len(ids) <= planErrorListMax {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ids[:planErrorListMax], ", "), len(ids)-planErrorListMax)
}
