package steprules

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Category of the first meaningful difference.
const (
	DivCommand    = "command"     // different command text
	DivFileEdit   = "file_edit"   // different path or op
	DivToolCall   = "tool_call"   // different tool
	DivToolResult = "tool_result" // same call, one errored
	DivRetry      = "retry"       // one side retried the model call
	DivTestRun    = "test_run"    // one side ran tests here, the other did not
	DivEnded      = "ended"       // one side stopped, the other went on
)

// DivergeOptions tunes FirstDivergence. Use DefaultDivergeOptions for the
// defaults: the zero value keeps read steps.
type DivergeOptions struct {
	IgnoreReads bool // default true: read-only steps (ClassRead) are skipped before aligning
	Classes     Classes
	CostOf      func(runSide int, requestID string) (usd float64, known bool) // nil: cost unknown
}

// DefaultDivergeOptions skips reads and uses the default classes.
func DefaultDivergeOptions() DivergeOptions {
	return DivergeOptions{IgnoreReads: true, Classes: DefaultClasses()}
}

// SideAfter counts what one run did from the divergence point to its end.
type SideAfter struct {
	Steps    int      `json:"steps"`
	Turns    int      `json:"turns"`
	Retries  int      `json:"retries"`
	TestRuns int      `json:"test_runs"`
	Edits    int      `json:"edits"`
	Errors   int      `json:"errors"`
	CostUSD  *float64 `json:"cost_usd,omitempty"` // nil when any model_turn after the point has no known cost
}

// Divergence is the first point where two runs did something different.
type Divergence struct {
	Found    bool         `json:"found"`
	Category string       `json:"category,omitempty"`
	SeqA     int          `json:"seq_a,omitempty"` // 0 when that side has no step there
	SeqB     int          `json:"seq_b,omitempty"`
	TextA    string       `json:"text_a,omitempty"` // the step summaries, at most 200 chars each
	TextB    string       `json:"text_b,omitempty"`
	After    [2]SideAfter `json:"after"` // counted from the point (inclusive) to the end, per side
	Note     string       `json:"note"`  // always DivergenceNote
}

// DivergenceNote is the label every divergence carries.
const DivergenceNote = "first point where the two runs did something different: an observation, not a proven cause"

// compared returns the indexes of the steps that take part in the walk:
// not model turns, not witness copies of another row, and not reads when
// IgnoreReads is set.
func compared(steps []schemas.StepRecord, opt DivergeOptions) []int {
	var out []int
	for i, s := range steps {
		if s.Kind == schemas.StepModelTurn || s.WitnessOf != 0 {
			continue
		}
		if opt.IgnoreReads {
			read := false
			for _, k := range opt.Classes.Of(s) {
				read = read || k == ClassRead
			}
			if read {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

func summaryText(s schemas.StepRecord) string {
	t := strings.TrimSpace(s.Summary)
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
	return cut(t, 200)
}

// FirstDivergence aligns the two step lists after filtering (model_turn rows
// are kept for cost counting but never compared), walks them in order, and
// returns the first index where Key differs. Equal lists return Found false.
func FirstDivergence(a, b []schemas.StepRecord, opt DivergeOptions) Divergence {
	if opt.Classes.empty() {
		opt.Classes = DefaultClasses()
	}
	d := Divergence{Note: DivergenceNote}
	ia, ib := compared(a, opt), compared(b, opt)
	i := 0
	for ; i < len(ia) && i < len(ib); i++ {
		if keyWith(opt.Classes, a[ia[i]]) != keyWith(opt.Classes, b[ib[i]]) {
			break
		}
	}
	if i == len(ia) && i == len(ib) {
		return d
	}
	d.Found = true
	sides := [2][]schemas.StepRecord{a, b}
	idx := [2][]int{ia, ib}
	var at [2]*schemas.StepRecord
	var start [2]int
	for k := 0; k < 2; k++ {
		if i < len(idx[k]) {
			s := sides[k][idx[k][i]]
			at[k] = &s
			start[k] = idx[k][i]
		} else if i > 0 {
			start[k] = idx[k][i-1] + 1
		}
		d.After[k] = after(sides[k][start[k]:], k, opt)
	}
	if at[0] != nil {
		d.SeqA, d.TextA = at[0].Seq, summaryText(*at[0])
	}
	if at[1] != nil {
		d.SeqB, d.TextB = at[1].Seq, summaryText(*at[1])
	}
	d.Category = category(at[0], at[1], opt.Classes)
	return d
}

func category(x, y *schemas.StepRecord, c Classes) string {
	if x == nil || y == nil {
		return DivEnded
	}
	cx, cy := c.commandText(*x), c.commandText(*y)
	if cx != "" && cy != "" {
		return DivCommand
	}
	if x.Kind == y.Kind && x.ToolName == y.ToolName && x.Path == y.Path && x.Op == y.Op && cx == cy && isErrorStep(*x) != isErrorStep(*y) {
		return DivToolResult
	}
	if (x.Kind == schemas.StepModelAttempt) != (y.Kind == schemas.StepModelAttempt) {
		return DivRetry
	}
	has := func(s schemas.StepRecord, k string) bool {
		for _, v := range c.Of(s) {
			if v == k {
				return true
			}
		}
		return false
	}
	if has(*x, ClassTestRun) != has(*y, ClassTestRun) {
		return DivTestRun
	}
	if x.Kind == schemas.StepFileChange || y.Kind == schemas.StepFileChange {
		return DivFileEdit
	}
	return DivToolCall
}

func after(steps []schemas.StepRecord, side int, opt DivergeOptions) SideAfter {
	var out SideAfter
	total, known := 0.0, opt.CostOf != nil
	for _, s := range steps {
		if s.WitnessOf != 0 {
			continue
		}
		out.Steps++
		cls := map[string]bool{}
		for _, k := range opt.Classes.Of(s) {
			cls[k] = true
		}
		switch s.Kind {
		case schemas.StepModelTurn:
			out.Turns++
			if known {
				v, ok := opt.CostOf(side, s.RequestID)
				if !ok || s.RequestID == "" {
					known = false
				}
				total += v
			}
		case schemas.StepModelAttempt:
			out.Retries++
		}
		if cls[ClassTestRun] {
			out.TestRuns++
		}
		if cls[ClassEdit] {
			out.Edits++
		}
		if cls[ClassToolError] {
			out.Errors++
		}
	}
	if known {
		out.CostUSD = &total
	}
	return out
}
