package metrics

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// PatternLoop is the name of the repeated-tool-call pattern.
const PatternLoop = "pattern_loop"

// Loop thresholds.
const (
	loopInARow   = 3 // the same call this many times back to back
	loopNoChange = 4 // the same call this many times with no edit between
)

var _ schemas.Metric = (*patternLoopMetric)(nil)

// patternLoopMetric flags a run that repeats the same tool call. It is an
// observed pattern: a repeated read can be harmless and a loop can hide
// behind small argument changes (docs/triage.md).
type patternLoopMetric struct{}

// GetName implements schemas.Part.
func (m *patternLoopMetric) GetName() string { return PatternLoop }

// GetVersion implements schemas.Part.
func (m *patternLoopMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *patternLoopMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           PatternLoop,
		Version:        version,
		Label:          "Loop",
		Kind:           schemas.KindRate,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceGateway,
		Description: "Share of runs where the same tool call (same tool and arguments hash, or the same command text for shell tools) " +
			"repeats 3 or more times in a row, or 4 or more times with no edit tool call between, " +
			"over runs with gateway or agent_stream events; an observed pattern, not a root cause.",
		PS2: false,
	}
}

// Compute implements schemas.Metric. It reads the gateway's tool calls when
// complete, else the agent's stream (pickTrace). An incomplete gateway with
// no stream can still show a loop, but not prove there was none.
func (m *patternLoopMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	tr, ok := pickTrace(ev.Events)
	if !ok {
		return schemas.Unknown(reasonNoEvents, schemas.EvidenceGateway), nil
	}
	hits := findLoops(tr.calls)
	if len(hits) == 0 {
		if tr.incomplete {
			return schemas.Unknown(gatewayIncomplete+" and no agent_stream events", tr.level), nil
		}
		return patternValue(false, tr.level, nil, fmt.Sprintf("no repeated tool call in %d calls", len(tr.calls))), nil
	}
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.ids...)
	}
	first := hits[0]
	summary := fmt.Sprintf("%s repeated %d times %s", describeCall(first.call), len(first.ids), first.rule)
	if len(hits) > 1 {
		summary += fmt.Sprintf("; %d more repeated calls", len(hits)-1)
	}
	return patternValue(true, tr.level, ids, summary), nil
}

// loopHit is one repeated call: the event ids of its repeats and the rule
// that caught it.
type loopHit struct {
	key  string
	call schemas.Event
	ids  []string
	rule string
	at   int // index of its first repeat, for ordering
}

// findLoops returns every call repeated loopInARow times back to back or
// loopNoChange times between two edits, one hit per call key, in order of
// first appearance. An edit call resets the between-edits count; it can
// still loop by itself when repeated back to back.
func findLoops(calls []schemas.Event) []loopHit {
	byKey := map[string]*loopHit{}
	var order []string
	add := func(key string, idx []int, rule string) {
		h, ok := byKey[key]
		if !ok {
			h = &loopHit{key: key, call: calls[idx[0]], rule: rule, at: idx[0]}
			byKey[key] = h
			order = append(order, key)
		}
		for _, i := range idx {
			if !slices.Contains(h.ids, calls[i].EventID) {
				h.ids = append(h.ids, calls[i].EventID)
			}
		}
	}
	for start := 0; start < len(calls); {
		end := start + 1
		for end < len(calls) && callKey(calls[end]) == callKey(calls[start]) {
			end++
		}
		if end-start >= loopInARow {
			add(callKey(calls[start]), indexRange(start, end), "in a row")
		}
		start = end
	}
	since := map[string][]int{}
	for i, c := range calls {
		if isEdit(c) {
			since = map[string][]int{}
			continue
		}
		k := callKey(c)
		since[k] = append(since[k], i)
		if len(since[k]) >= loopNoChange {
			add(k, since[k], "with no edit between")
		}
	}
	out := make([]loopHit, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	slices.SortStableFunc(out, func(x, y loopHit) int { return cmp.Compare(x.at, y.at) })
	return out
}

// newPatternLoopMetric returns the pattern_loop metric.
func newPatternLoopMetric() *patternLoopMetric {
	return &patternLoopMetric{}
}

// indexRange returns the indexes from start up to end, exclusive.
func indexRange(start, end int) []int {
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}
