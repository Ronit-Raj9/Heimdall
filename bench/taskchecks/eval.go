package taskchecks

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Call is one tool call as the checks see it: the ledger row with its
// arguments decoded.
type Call struct {
	Seq     int
	Server  string
	Tool    string
	Args    any // decoded JSON; nil when the arguments were not JSON or were truncated
	Raw     string
	IsError bool
}

// CallsFrom keeps the ledger rows that are tool calls (not session or
// unparsed traffic rows) in ledger order and decodes their arguments.
func CallsFrom(rows []schemas.ToolCall) []Call {
	out := make([]Call, 0, len(rows))
	for _, r := range rows {
		if r.Kind != "" || r.Tool == "" || r.Blocked {
			continue
		}
		c := Call{Seq: r.Seq, Server: r.Server, Tool: r.Tool, Raw: r.Arguments, IsError: r.IsError || r.Fault != ""}
		if strings.TrimSpace(r.Arguments) != "" {
			var v any
			if json.Unmarshal([]byte(r.Arguments), &v) == nil {
				c.Args = v
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Inputs are what a grade reads. State maps <service>/<export id> to a
// reader of that export.
type Inputs struct {
	Calls []Call
	State StateReader
}

// StateReader reads an exported state file: JSON returns the decoded
// document, Query runs a read-only query on a SQLite export.
type StateReader interface {
	JSON(file string) (any, error)
	Query(file, sql string) (rows [][]any, err error)
}

// Result is the outcome of one check.
type Result struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	Passed   bool    `json:"passed"`
	Weight   float64 `json:"weight"`
	Required bool    `json:"required,omitempty"`
	Detail   string  `json:"detail"`
	// Invalid is set when the check could not be decided (a state export
	// missing or unreadable): the grade is then an evaluator problem.
	Invalid bool `json:"invalid,omitempty"`
	// Pending marks a reply_rubric check the judge has not decided yet.
	Pending bool `json:"pending,omitempty"`
}

// Evaluate runs every deterministic check. A reply_rubric check is
// returned Pending, for the grader to judge.
func Evaluate(s *Spec, in Inputs) []Result {
	out := make([]Result, 0, len(s.Checks))
	for i := range s.Checks {
		c := &s.Checks[i]
		r := Result{ID: c.ID, Kind: c.Kind(), Weight: c.WeightOf(), Required: c.Required}
		switch r.Kind {
		case KindToolCalled:
			r.Passed, r.Detail = toolCalled(c.ToolCalled, in.Calls)
		case KindToolNotCalled:
			n, first := countMatches(c.ToolNotCalled, in.Calls)
			r.Passed = n == 0
			r.Detail = "no matching call of " + c.ToolNotCalled.Tool
			if n > 0 {
				r.Detail = fmt.Sprintf("%s was called %s (first at call %d)", c.ToolNotCalled.Tool, times(n), first)
			}
		case KindCallOrder:
			r.Passed, r.Detail = callOrder(c.CallOrder, in.Calls)
		case KindState:
			r.Passed, r.Invalid, r.Detail = stateCheck(c.State, in.State)
		case KindReplyRubric:
			r.Pending, r.Detail = true, "not judged yet"
		}
		out = append(out, r)
	}
	return out
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return strconv.Itoa(n) + " times"
}

func toolCalled(m *CallMatch, calls []Call) (bool, string) {
	n, _ := countMatches(m, calls)
	lo, hi := 1, -1
	switch {
	case m.Times != nil:
		lo, hi = *m.Times, *m.Times
	default:
		if m.MinTimes != nil {
			lo = *m.MinTimes
		}
		if m.MaxTimes != nil {
			hi = *m.MaxTimes
		}
	}
	ok := n >= lo && (hi < 0 || n <= hi)
	want := fmt.Sprintf("at least %s", times(lo))
	switch {
	case hi == lo:
		want = "exactly " + times(lo)
	case hi >= 0:
		want = fmt.Sprintf("%d to %d times", lo, hi)
	}
	detail := fmt.Sprintf("%s matched %s; want %s", m.Tool, times(n), want)
	if n == 0 {
		detail = fmt.Sprintf("no call of %s matched", m.Tool)
		if near := nearMiss(m, calls); near != "" {
			detail += "; " + near
		}
	}
	return ok, detail
}

// nearMiss says why the calls of the named tool did not match, for the
// grade's detail.
func nearMiss(m *CallMatch, calls []Call) string {
	n := 0
	var why string
	for _, c := range calls {
		if c.Tool != m.Tool || (m.Server != "" && c.Server != m.Server) {
			continue
		}
		n++
		if why == "" {
			_, why = matchCall(m, c)
		}
	}
	if n == 0 {
		return m.Tool + " was never called"
	}
	return fmt.Sprintf("%s was called %s, but %s", m.Tool, times(n), why)
}

// countMatches counts the calls m matches and returns the first one's seq.
func countMatches(m *CallMatch, calls []Call) (int, int) {
	n, first := 0, 0
	for _, c := range calls {
		if ok, _ := matchCall(m, c); ok {
			if n == 0 {
				first = c.Seq
			}
			n++
		}
	}
	return n, first
}

// matchCall reports whether c is a call m picks, and why not.
func matchCall(m *CallMatch, c Call) (bool, string) {
	if c.Tool != m.Tool {
		return false, "another tool"
	}
	if m.Server != "" && c.Server != m.Server {
		return false, "server " + c.Server + ", want " + m.Server
	}
	if m.Succeeded != nil && *m.Succeeded == c.IsError {
		if c.IsError {
			return false, "the call returned an error"
		}
		return false, "the call succeeded"
	}
	needArgs := m.Args != nil || len(m.ArgsRegex) > 0 || len(m.ArgsPath) > 0
	if needArgs && c.Args == nil {
		return false, "its arguments are not readable JSON"
	}
	if m.Args != nil {
		obj, _ := c.Args.(map[string]any)
		if m.Match == MatchExact {
			if !Equal(obj, m.Args) {
				return false, "its arguments " + short(c.Raw) + " are not exactly " + short(mustJSON(m.Args))
			}
		} else if why := subset(m.Args, obj, ""); why != "" {
			return false, why
		}
	}
	for _, k := range sortedKeys(m.ArgsRegex) {
		re := regexp.MustCompile(m.ArgsRegex[k])
		vals := argValues(c.Args, k)
		hit := false
		for _, v := range vals {
			if re.MatchString(textOf(v)) {
				hit = true
				break
			}
		}
		if !hit {
			return false, fmt.Sprintf("argument %s does not match %s", k, m.ArgsRegex[k])
		}
	}
	for _, pt := range m.ArgsPath {
		p, _ := ParsePath(pt.Path)
		if ok, why := pt.ValueOps.test(p.Eval(c.Args), p.Definite()); !ok {
			return false, "argument " + pt.Path + " " + why
		}
	}
	return true, ""
}

// argValues reads a regex key: a JSON path from $, else a top-level key.
func argValues(args any, k string) []any {
	if strings.HasPrefix(k, "$") {
		p, err := ParsePath(k)
		if err != nil {
			return nil
		}
		return p.Eval(args)
	}
	if m, ok := args.(map[string]any); ok {
		if v, ok := m[k]; ok {
			return []any{v}
		}
	}
	return nil
}

// subset says why want is not a subset of got ("" when it is): every key of
// want is in got with an equal value, objects compared the same way.
func subset(want, got map[string]any, at string) string {
	for _, k := range sortedKeys(want) {
		g, ok := got[k]
		name := strings.TrimPrefix(at+"."+k, ".")
		if !ok {
			return "argument " + name + " is missing"
		}
		if wm, ok := want[k].(map[string]any); ok {
			gm, ok := g.(map[string]any)
			if !ok {
				return "argument " + name + " is not an object"
			}
			if why := subset(wm, gm, name); why != "" {
				return why
			}
			continue
		}
		if !Equal(g, want[k]) {
			return fmt.Sprintf("argument %s is %s, want %s", name, short(mustJSON(g)), short(mustJSON(want[k])))
		}
	}
	return ""
}

// callOrder passes when the calls hold the steps in this order (other calls
// may come between them).
func callOrder(steps []CallMatch, calls []Call) (bool, string) {
	i := 0
	var seqs []string
	for _, c := range calls {
		if i == len(steps) {
			break
		}
		if ok, _ := matchCall(&steps[i], c); ok {
			seqs = append(seqs, strconv.Itoa(c.Seq))
			i++
		}
	}
	names := make([]string, len(steps))
	for j, s := range steps {
		names[j] = s.Tool
	}
	if i == len(steps) {
		return true, "in order: " + strings.Join(names, ", ") + " (calls " + strings.Join(seqs, ", ") + ")"
	}
	if i == 0 {
		return false, "no call of " + steps[0].Tool + " matched, so the order " + strings.Join(names, ", ") + " never started"
	}
	return false, fmt.Sprintf("%s did not follow %s in order (want %s)", steps[i].Tool, steps[i-1].Tool, strings.Join(names, ", "))
}

// stateCheck reads one state test.
func stateCheck(s *StateCheck, r StateReader) (passed, invalid bool, detail string) {
	if r == nil {
		return false, true, "no exported state to read " + s.File + " from"
	}
	if s.SQL != "" {
		rows, err := r.Query(s.File, s.SQL)
		if err != nil {
			return false, true, s.File + ": " + err.Error()
		}
		var vals []any
		if len(rows) > 0 && len(rows[0]) > 0 {
			vals = []any{rows[0][0]}
		}
		ops := s.ValueOps
		if ops.Length != nil || ops.MaxLength != nil {
			count := ValueOps{Length: ops.Length, MaxLength: ops.MaxLength}
			list := make([]any, len(rows))
			for i := range rows {
				list[i] = rows[i]
			}
			if ok, why := count.test([]any{list}, true); !ok {
				return false, false, "sql rows: " + strings.Replace(why, "has length", "count", 1)
			}
			ops.Length, ops.MaxLength = nil, nil
			if ops.empty() {
				return true, false, fmt.Sprintf("sql returned %d rows", len(rows))
			}
		}
		ok, why := ops.test(vals, true)
		return ok, false, "sql value " + why
	}
	doc, err := r.JSON(s.File)
	if err != nil {
		return false, true, s.File + ": " + err.Error()
	}
	p, _ := ParsePath(s.Path)
	ok, why := s.ValueOps.test(p.Eval(doc), p.Definite())
	return ok, false, s.Path + " " + why
}

// test applies the ops to what a path reached. On a definite path vals
// holds at most one value; otherwise it is the list of matches.
func (v ValueOps) test(vals []any, definite bool) (bool, string) {
	if v.Exists != nil {
		got := len(vals) > 0
		if got != *v.Exists {
			if got {
				return false, "exists, want it absent"
			}
			return false, "is missing"
		}
	}
	rest := v
	rest.Exists = nil
	if rest.empty() {
		if len(vals) > 0 {
			return true, "exists"
		}
		return true, "is absent"
	}
	if !definite {
		if rest.Length != nil || rest.MaxLength != nil {
			n := len(vals)
			if rest.Length != nil && n != *rest.Length {
				return false, fmt.Sprintf("matched %d, want %d", n, *rest.Length)
			}
			if rest.MaxLength != nil && n > *rest.MaxLength {
				return false, fmt.Sprintf("matched %d, want at most %d", n, *rest.MaxLength)
			}
			rest.Length, rest.MaxLength = nil, nil
			if rest.empty() {
				return true, fmt.Sprintf("matched %d", n)
			}
		}
		if len(vals) == 0 {
			return false, "matched nothing"
		}
		for _, x := range vals {
			ok, why := rest.one(x)
			if rest.All && !ok {
				return false, "a match " + why
			}
			if !rest.All && ok {
				return true, why
			}
		}
		if rest.All {
			return true, fmt.Sprintf("all %d matches pass", len(vals))
		}
		return false, fmt.Sprintf("none of %d matches pass (first is %s)", len(vals), short(mustJSON(vals[0])))
	}
	if len(vals) == 0 {
		return false, "is missing"
	}
	return rest.one(vals[0])
}

// one applies every op to one value.
func (v ValueOps) one(x any) (bool, string) {
	got := short(mustJSON(x))
	if v.Equals != nil && !Equal(x, v.Equals) {
		return false, "is " + got + ", want " + short(mustJSON(v.Equals))
	}
	if v.NotEquals != nil && Equal(x, v.NotEquals) {
		return false, "is " + got + ", want anything else"
	}
	if v.In != nil {
		hit := false
		for _, w := range v.In {
			hit = hit || Equal(x, w)
		}
		if !hit {
			return false, "is " + got + ", want one of " + short(mustJSON(v.In))
		}
	}
	if v.Contains != nil && !containsValue(x, v.Contains) {
		return false, "is " + got + ", which does not contain " + short(mustJSON(v.Contains))
	}
	if v.NotContains != nil && containsValue(x, v.NotContains) {
		return false, "is " + got + ", which contains " + short(mustJSON(v.NotContains))
	}
	if v.Matches != "" {
		s, ok := x.(string)
		if !ok || !regexp.MustCompile(v.Matches).MatchString(s) {
			return false, "is " + got + ", which does not match " + v.Matches
		}
	}
	if v.Gte != nil || v.Lte != nil {
		f, ok := number(x)
		if !ok {
			return false, "is " + got + ", not a number"
		}
		if v.Gte != nil && f < *v.Gte {
			return false, fmt.Sprintf("is %s, want at least %v", got, *v.Gte)
		}
		if v.Lte != nil && f > *v.Lte {
			return false, fmt.Sprintf("is %s, want at most %v", got, *v.Lte)
		}
	}
	if v.Length != nil || v.MaxLength != nil {
		n, ok := lengthOf(x)
		if !ok {
			return false, "is " + got + ", which has no length"
		}
		if v.Length != nil && n != *v.Length {
			return false, fmt.Sprintf("has length %d, want %d", n, *v.Length)
		}
		if v.MaxLength != nil && n > *v.MaxLength {
			return false, fmt.Sprintf("has length %d, want at most %d", n, *v.MaxLength)
		}
	}
	return true, "is " + got
}

// Equal is JSON equality that keeps 1 and true apart; numbers compare by
// value whatever their Go type (a SQLite int64 equals a YAML 3).
func Equal(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	switch av := a.(type) {
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !Equal(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, x := range av {
			y, ok := bv[k]
			if !ok || !Equal(x, y) {
				return false
			}
		}
		return true
	case []byte:
		return Equal(string(av), b)
	}
	if bb, ok := b.([]byte); ok {
		return Equal(a, string(bb))
	}
	return reflect.DeepEqual(a, b)
}

// number reads a JSON or SQL number; bool is never a number.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n)
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func containsValue(x, want any) bool {
	switch t := x.(type) {
	case string:
		w, ok := want.(string)
		return ok && strings.Contains(t, w)
	case []any:
		for _, el := range t {
			if Equal(el, want) {
				return true
			}
		}
	case map[string]any:
		w, ok := want.(string)
		_, has := t[w]
		return ok && has
	}
	return false
}

func lengthOf(x any) (int, bool) {
	switch t := x.(type) {
	case string:
		return len([]rune(t)), true
	case []any:
		return len(t), true
	case map[string]any:
		return len(t), true
	}
	return 0, false
}

// textOf is a value as text: a string itself, anything else as JSON.
func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	}
	if f, ok := number(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return mustJSON(v)
}

func mustJSON(v any) string {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(out)
}

func short(s string) string {
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Score combines the results: the weighted share passed, and whether the
// task passes (share at the threshold and every required check passed).
// Invalid is set when any check could not be decided.
func Score(s *Spec, results []Result) (score float64, passed, invalid bool, reason string) {
	total, got := 0.0, 0.0
	var failed, required, undecided []string
	for _, r := range results {
		if r.Invalid || r.Pending {
			undecided = append(undecided, r.ID+" ("+r.Detail+")")
			continue
		}
		total += r.Weight
		if r.Passed {
			got += r.Weight
			continue
		}
		failed = append(failed, r.ID)
		if r.Required {
			required = append(required, r.ID)
		}
	}
	if len(undecided) > 0 {
		return 0, false, true, "checks not decided: " + strings.Join(undecided, "; ")
	}
	if total > 0 {
		score = got / total
	} else if len(failed) == 0 {
		score = 1
	}
	threshold := s.Threshold()
	passed = score >= threshold-1e-9 && len(required) == 0
	n := len(results)
	switch {
	case len(failed) == 0:
		reason = fmt.Sprintf("checks: all %d passed", n)
	default:
		reason = fmt.Sprintf("checks: %d of %d passed, score %.2f (threshold %.2f); failed: %s", n-len(failed), n, score, threshold, strings.Join(failed, ", "))
		if len(required) > 0 {
			reason += "; required check failed: " + strings.Join(required, ", ")
		}
	}
	return score, passed, false, reason
}
