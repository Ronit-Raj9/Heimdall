package reportcard

// Metadata slices (lane Y13): tasks carry metadata from the customer's data
// (tier, language, intent, any key), frozen in each child's manifest
// (Manifest.TaskMetadata). The report card shows, per key and value, each
// arm's clean score with the same task-clustered interval as a benchmark
// row, pooled over every benchmark of the evaluation, and marks each arm's
// worst slice. A value with fewer than MinSliceTasks tasks is listed but
// marked small and never named worst: its interval is too wide to rank.
// Options.Where narrows the whole report to the tasks of one slice, and a
// gate rule can hold a slice to a minimum score or a largest drop.

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MinSliceTasks is the fewest tasks a slice value needs to be ranked.
const MinSliceTasks = 3

// maxSliceKeys and maxSliceValues cap the slices section.
const (
	maxSliceKeys   = 12
	maxSliceValues = 30
)

// SliceValue is one value of a metadata key and each arm's clean score on
// the tasks that have it.
type SliceValue struct {
	Value string             `json:"value"`
	Tasks int                `json:"tasks"`
	Small bool               `json:"small,omitempty"` // fewer than MinSliceTasks tasks: shown, never ranked
	Arms  map[string]Metrics `json:"arms"`
	// Delta is the second arm's score minus the first's in an A/B
	// evaluation; nil otherwise or when either arm has no runs here.
	Delta *float64 `json:"delta,omitempty"`
}

// Slice is one metadata key.
type Slice struct {
	Key    string       `json:"key"`
	Values []SliceValue `json:"values"`
	// Worst names, per arm label, the ranked value with the lowest score
	// (ties: the lower interval, then the value name).
	Worst map[string]string `json:"worst"`
	// Other counts the values past the cap that are not listed.
	Other int `json:"other,omitempty"`
}

var sliceKeyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// ValidSliceKey reports whether k can be a metadata key.
func ValidSliceKey(k string) bool { return sliceKeyRE.MatchString(k) }

// ParseWhere reads "key:value" filters (the report's where query); a key
// given twice keeps the last value.
func ParseWhere(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, w := range in {
		k, v, ok := strings.Cut(w, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !ValidSliceKey(k) || v == "" || len(v) > 200 {
			return nil, fmt.Errorf("where %q: want key:value, the key letters, digits, '_', '.' or '-'", w)
		}
		out[k] = v
	}
	if len(out) > 4 {
		return nil, fmt.Errorf("at most 4 where filters")
	}
	return out, nil
}

// matchesWhere reports whether a task's metadata has every key and value
// of where.
func matchesWhere(meta map[string]string, where map[string]string) bool {
	for k, v := range where {
		if meta[k] != v {
			return false
		}
	}
	return true
}

// sliceSections computes the slices of the clean runs of children.
func sliceSections(children []childRuns, labels []string) []Slice {
	type key struct{ k, v string }
	runs := map[key]map[string][]graded{} // slice value -> arm -> runs, benchmark-prefixed task ids
	tasks := map[key]map[string]bool{}
	keys := map[string]map[string]bool{}
	for _, cr := range children {
		if len(cr.meta) == 0 {
			continue
		}
		for _, l := range labels {
			for _, g := range filter(cr.arms[l], schemas.ConditionClean) {
				meta := cr.meta[g.task]
				for k, v := range meta {
					kv := key{k, v}
					if runs[kv] == nil {
						runs[kv] = map[string][]graded{}
						tasks[kv] = map[string]bool{}
					}
					pg := g
					pg.task = cr.child.Benchmark + "\x00" + g.task
					runs[kv][l] = append(runs[kv][l], pg)
					tasks[kv][pg.task] = true
					if keys[k] == nil {
						keys[k] = map[string]bool{}
					}
					keys[k][v] = true
				}
			}
		}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	slices.Sort(names)
	if len(names) > maxSliceKeys {
		names = names[:maxSliceKeys]
	}
	out := []Slice{}
	for _, k := range names {
		if len(keys[k]) < 2 {
			continue // one value is the whole evaluation, not a slice
		}
		s := Slice{Key: k, Values: []SliceValue{}, Worst: map[string]string{}}
		vals := make([]string, 0, len(keys[k]))
		for v := range keys[k] {
			vals = append(vals, v)
		}
		// Most tasks first, then by name.
		slices.SortFunc(vals, func(a, b string) int {
			return cmp.Or(cmp.Compare(len(tasks[key{k, b}]), len(tasks[key{k, a}])), cmp.Compare(a, b))
		})
		if len(vals) > maxSliceValues {
			s.Other = len(vals) - maxSliceValues
			vals = vals[:maxSliceValues]
		}
		for _, v := range vals {
			kv := key{k, v}
			sv := SliceValue{Value: v, Tasks: len(tasks[kv]), Arms: map[string]Metrics{}}
			sv.Small = sv.Tasks < MinSliceTasks
			for _, l := range labels {
				sv.Arms[l] = metricsOf(runs[kv][l])
			}
			if len(labels) == 2 {
				a, b := sv.Arms[labels[0]], sv.Arms[labels[1]]
				if a.Runs > 0 && b.Runs > 0 {
					d := round(b.Score - a.Score)
					sv.Delta = &d
				}
			}
			s.Values = append(s.Values, sv)
		}
		for _, l := range labels {
			best, bestScore, bestLo := "", math.Inf(1), math.Inf(1)
			for _, sv := range s.Values {
				m := sv.Arms[l]
				if sv.Small || m.Runs == 0 {
					continue
				}
				if m.Score < bestScore || (m.Score == bestScore && m.CILow < bestLo) {
					best, bestScore, bestLo = sv.Value, m.Score, m.CILow
				}
			}
			if best != "" {
				s.Worst[l] = best
			}
		}
		out = append(out, s)
	}
	return out
}

// sliceScore is an arm's score on one slice value of r and whether it has
// runs there.
func sliceScore(r *Report, label, key, value string) (Metrics, bool) {
	if r == nil {
		return Metrics{}, false
	}
	for _, s := range r.Slices {
		if s.Key != key {
			continue
		}
		for _, v := range s.Values {
			if v.Value == value {
				m, ok := v.Arms[label]
				return m, ok && m.Runs > 0
			}
		}
	}
	return Metrics{}, false
}

// sliceChecks applies the gate's slice rules. A slice the candidate never
// ran fails a min_score rule (a gate must not pass on missing data); a
// max_regression rule needs both sides, else it is skipped and counted in
// noBaseline.
func sliceChecks(in GateInput) (checks []schemas.GateCheck, failed int, noBaseline bool) {
	for _, rule := range in.Gate.Slices {
		name := rule.Key + "=" + rule.Value
		got, ok := sliceScore(in.Report, in.Candidate, rule.Key, rule.Value)
		if rule.MinScore != nil {
			min := *rule.MinScore
			chk := schemas.GateCheck{Capability: name, Rule: RuleMinScore, Threshold: min, Observed: got.Score, Pass: ok && got.Score+1e-9 >= min}
			switch {
			case !ok:
				chk.Summary = fmt.Sprintf("no runs on tasks with %s to score", name)
			case chk.Pass:
				chk.Summary = fmt.Sprintf("%s score %s, at least %s needed", name, pts(got.Score), pts(min))
			default:
				chk.Summary = fmt.Sprintf("%s score %s is below the %s needed", name, pts(got.Score), pts(min))
			}
			if !chk.Pass {
				failed++
			}
			checks = append(checks, chk)
		}
		if rule.MaxRegression > 0 {
			was, b := sliceScore(in.BaselineReport, in.BaselineArm, rule.Key, rule.Value)
			if in.Baseline == nil || !ok || !b {
				noBaseline = true
				continue
			}
			w := was.Score
			drop := was.Score - got.Score
			chk := schemas.GateCheck{Capability: name, Rule: RuleMaxRegression, Threshold: rule.MaxRegression, Observed: got.Score, Baseline: &w,
				Pass: drop <= rule.MaxRegression+1e-9}
			if chk.Pass {
				chk.Summary = fmt.Sprintf("%s %s, was %s (at most %s drop allowed)", name, pts(got.Score), pts(was.Score), pts(rule.MaxRegression))
			} else {
				chk.Summary = fmt.Sprintf("%s fell from %s to %s, more than the %s drop allowed", name, pts(was.Score), pts(got.Score), pts(rule.MaxRegression))
				failed++
			}
			checks = append(checks, chk)
		}
	}
	return checks, failed, noBaseline
}
