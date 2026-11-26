package evidence

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	digestLastSteps = 12 // the last steps shown in full
	digestAround    = 3  // steps shown in full on each side of the failing step
	truncated       = "(truncated)"
)

// digest is a run's digest.md: deterministic, no model. The task message,
// the last 12 steps and the failing step with 3 steps either side in full,
// every other step as one line, then the final diff. Over max bytes the
// oldest one-line steps go first, then the end is cut, with a marker.
func digest(r *run, max int) string {
	full := map[int]bool{}
	for i := len(r.steps) - digestLastSteps; i < len(r.steps); i++ {
		if i >= 0 {
			full[i] = true
		}
	}
	if r.failing >= 0 {
		for i := r.failing - digestAround; i <= r.failing+digestAround; i++ {
			if i >= 0 && i < len(r.steps) {
				full[i] = true
			}
		}
	}
	head := fmt.Sprintf("# Practice run %s\n\nOutcome: %s, success %s\n\n## Task message\n\n%s\n\n## Steps\n\n",
		r.label, r.item.Outcome, strconv.FormatFloat(r.item.Success, 'f', -1, 64), strings.TrimSpace(r.instr))
	lines := make([]string, len(r.steps))
	for i, s := range r.steps {
		if full[i] {
			lines[i] = fullStep(s, i == r.failing)
		} else {
			lines[i] = oneLine(s) + "\n"
		}
	}
	tail := "\n## Final diff\n\n"
	if r.diff == "" {
		tail += "(no changes)\n"
	} else {
		tail += clipLineCount(r.diff, finalLines)
	}

	join := func(dropped int) string {
		var b strings.Builder
		b.WriteString(head)
		if dropped > 0 {
			b.WriteString(truncated + "\n")
		}
		n := 0
		for i, l := range lines {
			if !full[i] && n < dropped {
				n++
				continue
			}
			b.WriteString(l)
		}
		b.WriteString(tail)
		return b.String()
	}
	oneLines := len(r.steps) - len(full)
	out := join(0)
	for d := 1; len(out) > max && d <= oneLines; d++ {
		out = join(d)
	}
	if len(out) > max {
		cut := max - len(truncated) - 2
		if cut < 0 {
			cut = 0
		}
		out = out[:cut] + "\n" + truncated + "\n"
	}
	return out
}

func oneLine(s step) string {
	return fmt.Sprintf("#%d %s %s%s", s.I, s.Tool, strconv.Quote(clip(oneLineText(s.Args), argsClip)), resultTail(s))
}

func resultTail(s step) string {
	var b strings.Builder
	if s.hasExit {
		fmt.Fprintf(&b, " exit=%d", s.Exit)
	}
	if s.S > 0 {
		fmt.Fprintf(&b, " %ss", strconv.FormatFloat(s.S, 'f', -1, 64))
	}
	if s.Error && !s.hasExit {
		b.WriteString(" error")
	}
	return b.String()
}

func fullStep(s step, failing bool) string {
	var b strings.Builder
	mark := ""
	if failing {
		mark = " (the failing step)"
	}
	fmt.Fprintf(&b, "### #%d %s%s%s\n\n", s.I, s.Tool, resultTail(s), mark)
	fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimRight(s.Args, "\n"))
	if s.Result != "" {
		fmt.Fprintf(&b, "Result:\n\n```\n%s\n```\n\n", strings.TrimRight(clip(s.Result, errorClip), "\n"))
	}
	return b.String()
}

func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
