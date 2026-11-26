package steprules

import (
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MaxRows is the most step rows a run is read with; a longer timeline is cut.
const MaxRows = 20000

// maxHits caps the hits of one result.
const maxHits = 20

// Hit is one place a rule matched. Seqs are StepRecord.Seq values in order.
type Hit struct {
	Seqs      []int  `json:"seqs"`
	Summary   string `json:"summary"`   // built from the steps, at most 200 chars, agent text quoted
	Witnessed bool   `json:"witnessed"` // every step in Seqs has Proof "verified"
}

// Status values.
const (
	StatusFired   = "fired"
	StatusClear   = "clear"
	StatusUnknown = "unknown" // no timeline, or the timeline is truncated and the rule needs the whole run
)

// Result is one rule's answer on one run.
type Result struct {
	RuleID      string `json:"rule_id"`
	RuleVersion int    `json:"rule_version"`
	Status      string `json:"status"`
	Hits        []Hit  `json:"hits,omitempty"`   // at most 20
	Reason      string `json:"reason,omitempty"` // for unknown
}

const (
	reasonNoTimeline = "this run has no step timeline"
	reasonTruncated  = "the step timeline was cut at 20000 rows"
)

// cut shortens s to at most n bytes on a rune boundary.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	for len(string(r)) > n-3 && len(r) > 0 {
		r = r[:len(r)-1]
	}
	return string(r) + "..."
}

// stepText is a short readable summary of one step, agent text quoted.
func stepText(s schemas.StepRecord) string {
	t := s.Summary
	if t == "" {
		switch {
		case s.Path != "":
			t = strings.TrimSpace(s.Op + " " + s.Path)
		case s.ToolName != "":
			t = s.ToolName
		default:
			t = s.Kind
		}
	}
	return strconv.Quote(cut(strings.TrimSpace(t), 120))
}

func makeHit(steps []schemas.StepRecord) Hit {
	h := Hit{Seqs: make([]int, 0, len(steps)), Witnessed: len(steps) > 0}
	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		h.Seqs = append(h.Seqs, s.Seq)
		h.Witnessed = h.Witnessed && s.Proof == schemas.EvidenceVerified
		parts = append(parts, "step "+strconv.Itoa(s.Seq)+": "+stepText(s))
	}
	h.Summary = cut(strings.Join(parts, "; "), 200)
	return h
}

func modelRow(s schemas.StepRecord) bool {
	return s.Kind == schemas.StepModelTurn || s.Kind == schemas.StepModelAttempt
}

// classSets computes each step's classes, keeping success_claim only on the
// last claim candidate.
func classSets(c Classes, steps []schemas.StepRecord) []map[string]bool {
	out := make([]map[string]bool, len(steps))
	last := -1
	for i, s := range steps {
		m := map[string]bool{}
		for _, k := range c.Of(s) {
			m[k] = true
		}
		if m[ClassClaim] {
			last = i
		}
		out[i] = m
	}
	for i := range out {
		if i != last {
			delete(out[i], ClassClaim)
		}
	}
	return out
}

// Eval runs the rule on one run's steps. steps must be in Seq order.
// truncated is RunSteps.Truncated: absent and sequence rules answer unknown
// on a truncated timeline; present, repeat and after_error may still fire.
func (c *Compiled) Eval(steps []schemas.StepRecord, truncated bool) Result {
	res := Result{RuleID: c.rule.ID, RuleVersion: c.rule.Version, Status: StatusClear}
	if len(steps) == 0 {
		res.Status, res.Reason = StatusUnknown, reasonNoTimeline
		return res
	}
	cls := classSets(c.classes, steps)
	m := func(sel *compiledSel, i int) bool { return sel.match(c.classes, steps[i], cls[i]) }
	var hits []Hit
	add := func(idx ...int) {
		if len(hits) >= maxHits {
			return
		}
		ss := make([]schemas.StepRecord, 0, len(idx))
		for _, i := range idx {
			ss = append(ss, steps[i])
		}
		hits = append(hits, makeHit(ss))
	}
	switch c.rule.Kind {
	case KindPresent:
		for i := range steps {
			if m(&c.a, i) {
				add(i)
			}
		}
	case KindAbsent:
		if truncated {
			res.Status, res.Reason = StatusUnknown, reasonTruncated
			return res
		}
		found := false
		for i := range steps {
			found = found || m(&c.a, i)
		}
		if !found {
			hits = append(hits, Hit{Seqs: []int{}, Summary: "no step matched"})
		}
	case KindSequence:
		if truncated {
			res.Status, res.Reason = StatusUnknown, reasonTruncated
			return res
		}
		pending := -1
		for i := range steps {
			switch {
			case m(&c.a, i):
				pending = i
			case pending >= 0 && m(c.b, i):
				pending = -1
			case pending >= 0 && c.reset != nil && m(c.reset, i):
				pending = -1
			}
		}
		if pending >= 0 {
			add(pending)
		}
	case KindBefore:
		for i := range steps {
			if m(c.b, i) {
				break
			}
			if m(&c.a, i) {
				add(i)
				break
			}
		}
	case KindRepeat:
		var run []int
		prev := ""
		flush := func() {
			if len(run) >= c.n {
				add(run...)
			}
			run = nil
		}
		for i, s := range steps {
			if modelRow(s) {
				continue
			}
			if !m(&c.a, i) {
				flush()
				prev = ""
				continue
			}
			k := keyWith(c.classes, s)
			if k != prev {
				flush()
			}
			run = append(run, i)
			prev = k
		}
		flush()
	case KindAfterError:
		for i, s := range steps {
			if !cls[i][ClassToolError] || !m(&c.a, i) {
				continue
			}
			handled := false
			seen := 0
			for j := i + 1; j < len(steps) && seen < c.window; j++ {
				if modelRow(steps[j]) {
					continue
				}
				seen++
				if c.relatedAfterError {
					if related(s, steps[j], cls[j]) {
						handled = true
					}
				} else if m(c.b, j) {
					handled = true
				}
				if handled {
					break
				}
			}
			if !handled {
				add(i)
			}
		}
	}
	if len(hits) > 0 {
		res.Status, res.Hits = StatusFired, hits
	}
	return res
}

// related: a shell, edit or read step touching the failed step's tool or path.
func related(failed, s schemas.StepRecord, cls map[string]bool) bool {
	if !cls[ClassShell] && !cls[ClassEdit] && !cls[ClassRead] {
		return false
	}
	if failed.Kind == schemas.StepCommand && failed.Path == "" {
		return cls[ClassShell]
	}
	if failed.Path != "" && s.Path == failed.Path {
		return true
	}
	return failed.ToolName != "" && strings.EqualFold(failed.ToolName, s.ToolName)
}
