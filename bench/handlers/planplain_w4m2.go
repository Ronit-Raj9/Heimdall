package handlers

import (
	"errors"
	"strings"
)

// plainEach says each planner error string in plain words, through
// PlainPlanError, keeping their order.
func plainEach(errs []string) []string {
	out := make([]string, 0, len(errs))
	for _, s := range errs {
		out = append(out, PlainPlanError(errors.New(s)))
	}
	return out
}

// planSkipReason is the reason an evaluation gives for a benchmark the
// planner refused: the planner's error in plain words.
func planSkipReason(err error) string {
	return PlainPlanError(err)
}

// estimateRefusedNote is the note of a benchmark the planner refused while
// the evaluation was being estimated.
func estimateRefusedNote(err error) string {
	return "Cannot estimate this benchmark: " + PlainPlanError(err)
}

// priceReason says why a cost is not known. An empty name stands for an
// agent that picks its own model and reports its own cost.
func priceReason(unpriced []string) string {
	names := make([]string, 0, len(unpriced))
	for _, n := range unpriced {
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "the agent reports its own cost"
	}
	return "no price for " + strings.Join(names, ", ")
}

// planSentence is s ending as a sentence: trimmed, with a final period when
// it has no final punctuation, so a sentence that follows never runs on.
func planSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") {
		return s
	}
	return s + "."
}

// planSentences is the planner errors in plain words, each ending as a
// sentence, joined with spaces.
func planSentences(errs []string) string {
	each := plainEach(errs)
	for i, s := range each {
		each[i] = planSentence(s)
	}
	return strings.Join(each, " ")
}
