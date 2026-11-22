// Package taskchecks is the declarative check file of a task (checks.yaml in
// the task's hidden verifier folder): what an agent must and must not have
// done, written as data instead of a Python verifier. One shipped grader
// (graders/checks.go) reads it after the run from three sources:
//
//   - the tool proxy's ledger (tools.jsonl): tool_called, tool_not_called
//     and call_order, with arguments matched exactly, as a subset, by
//     regular expression or by JSON path;
//   - the services' exported state (state/<service>/<export id>): state
//     checks by JSON path on a JSON export or by a read-only SQL query on a
//     SQLite export;
//   - the conversation (the simulated customer's transcript, else the
//     answer): reply_rubric, judged by the suite's pinned judge model with
//     the judge guard (fenced data, injection refusal, its own key).
//
// Every check has a weight. The grade's reward is the weighted share of
// checks passed; the task passes when that share reaches pass_threshold
// (default 1, every weighted check) and every required check passes. Each
// check is also its own reward dimension, so a report shows which one
// failed.
package taskchecks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the check file's name in a task's verifier folder.
const FileName = "checks.yaml"

// Limits of one check file.
const (
	MaxChecks      = 64
	MaxFileBytes   = 256 << 10
	MaxRubricRunes = 4000
	maxIDLen       = 64
)

// Check kinds, in the order Kind reports them.
const (
	KindToolCalled    = "tool_called"
	KindToolNotCalled = "tool_not_called"
	KindCallOrder     = "call_order"
	KindState         = "state"
	KindReplyRubric   = "reply_rubric"
)

// Argument match modes (CallMatch.Match).
const (
	MatchSubset = "subset" // every key given must be present with an equal value (nested objects too); others may be present
	MatchExact  = "exact"  // the arguments equal the given object exactly
)

// Spec is one checks.yaml.
type Spec struct {
	Version int `yaml:"version" json:"version"`
	// PassThreshold is the lowest weighted share of passed checks that
	// passes the task: 1 (the default) needs every weighted check.
	PassThreshold *float64 `yaml:"pass_threshold,omitempty" json:"pass_threshold,omitempty"`
	Checks        []Check  `yaml:"checks" json:"checks"`
}

// Check is one entry of checks.yaml. Exactly one of ToolCalled,
// ToolNotCalled, CallOrder, State and ReplyRubric is set.
type Check struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Weight is the check's share of the reward; default 1. 0 reports the
	// check without counting it (unless it is required).
	Weight *float64 `yaml:"weight,omitempty" json:"weight,omitempty"`
	// Required fails the task when this check fails, whatever the score.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`

	ToolCalled    *CallMatch  `yaml:"tool_called,omitempty" json:"tool_called,omitempty"`
	ToolNotCalled *CallMatch  `yaml:"tool_not_called,omitempty" json:"tool_not_called,omitempty"`
	CallOrder     []CallMatch `yaml:"call_order,omitempty" json:"call_order,omitempty"`
	State         *StateCheck `yaml:"state,omitempty" json:"state,omitempty"`
	ReplyRubric   string      `yaml:"reply_rubric,omitempty" json:"reply_rubric,omitempty"`
}

// CallMatch picks tool calls from the ledger. In call_order a bare string
// is a CallMatch with only Tool set.
type CallMatch struct {
	Tool   string `yaml:"tool" json:"tool"`                         // tool name; for a plain HTTP tool server "METHOD /path"
	Server string `yaml:"server,omitempty" json:"server,omitempty"` // service id; empty matches any
	// Args are the arguments the call must carry: a subset (default) or
	// the exact object (Match exact).
	Args  map[string]any `yaml:"args,omitempty" json:"args,omitempty"`
	Match string         `yaml:"match,omitempty" json:"match,omitempty"`
	// ArgsRegex maps an argument (a key, or a JSON path from $) to a
	// regular expression its value, as text, must match.
	ArgsRegex map[string]string `yaml:"args_regex,omitempty" json:"args_regex,omitempty"`
	// ArgsPath are value tests on JSON paths into the arguments.
	ArgsPath []PathTest `yaml:"args_path,omitempty" json:"args_path,omitempty"`
	// Succeeded keeps only calls that did (true) or did not (false) return
	// a tool error.
	Succeeded *bool `yaml:"succeeded,omitempty" json:"succeeded,omitempty"`
	// Times is how many matching calls tool_called needs: exactly Times,
	// or between MinTimes (default 1) and MaxTimes.
	Times    *int `yaml:"times,omitempty" json:"times,omitempty"`
	MinTimes *int `yaml:"min_times,omitempty" json:"min_times,omitempty"`
	MaxTimes *int `yaml:"max_times,omitempty" json:"max_times,omitempty"`
}

// UnmarshalYAML accepts a bare tool name as a CallMatch.
func (c *CallMatch) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		c.Tool = n.Value
		return nil
	}
	type plain CallMatch
	var p plain
	if err := decodeStrict(n, &p); err != nil {
		return err
	}
	*c = CallMatch(p)
	return nil
}

// StateCheck tests the exported state of a service. File is
// <service>/<export id>. A JSON export is read with Path (a JSON path such
// as $.orders[?(@.id=='B-3001')].status, or the slash form
// orders/id=B-3001/status); a SQLite export with SQL, a single read-only
// SELECT whose first column of the first row is the value (Length counts
// the rows).
type StateCheck struct {
	File     string `yaml:"file" json:"file"`
	Path     string `yaml:"path,omitempty" json:"path,omitempty"`
	SQL      string `yaml:"sql,omitempty" json:"sql,omitempty"`
	ValueOps `yaml:",inline" json:",inline"`
}

// PathTest is a value test at a JSON path.
type PathTest struct {
	Path     string `yaml:"path" json:"path"`
	ValueOps `yaml:",inline" json:",inline"`
}

// ValueOps are the tests a value must pass; every one given must hold. On
// a path with a wildcard or a filter the value is the list of matches:
// Exists means at least one, Length counts them, and the other tests pass
// when any match passes (every match with All).
type ValueOps struct {
	Equals      any      `yaml:"equals,omitempty" json:"equals,omitempty"`
	NotEquals   any      `yaml:"not_equals,omitempty" json:"not_equals,omitempty"`
	In          []any    `yaml:"in,omitempty" json:"in,omitempty"`
	Contains    any      `yaml:"contains,omitempty" json:"contains,omitempty"`
	NotContains any      `yaml:"not_contains,omitempty" json:"not_contains,omitempty"`
	Matches     string   `yaml:"matches,omitempty" json:"matches,omitempty"`
	Gte         *float64 `yaml:"gte,omitempty" json:"gte,omitempty"`
	Lte         *float64 `yaml:"lte,omitempty" json:"lte,omitempty"`
	Length      *int     `yaml:"length,omitempty" json:"length,omitempty"`
	MaxLength   *int     `yaml:"max_length,omitempty" json:"max_length,omitempty"`
	Exists      *bool    `yaml:"exists,omitempty" json:"exists,omitempty"`
	All         bool     `yaml:"all,omitempty" json:"all,omitempty"`
}

func (v ValueOps) empty() bool {
	return v.Equals == nil && v.NotEquals == nil && v.In == nil && v.Contains == nil && v.NotContains == nil &&
		v.Matches == "" && v.Gte == nil && v.Lte == nil && v.Length == nil && v.MaxLength == nil && v.Exists == nil
}

// Kind names the check's kind.
func (c *Check) Kind() string {
	switch {
	case c.ToolCalled != nil:
		return KindToolCalled
	case c.ToolNotCalled != nil:
		return KindToolNotCalled
	case len(c.CallOrder) > 0:
		return KindCallOrder
	case c.State != nil:
		return KindState
	case c.ReplyRubric != "":
		return KindReplyRubric
	}
	return ""
}

// WeightOf is the check's weight, 1 when unset.
func (c *Check) WeightOf() float64 {
	if c.Weight == nil {
		return 1
	}
	return *c.Weight
}

// Threshold is the spec's pass threshold, 1 when unset.
func (s *Spec) Threshold() float64 {
	if s.PassThreshold == nil {
		return 1
	}
	return *s.PassThreshold
}

// Rubric is the task's reply rubric check, nil when it has none.
func (s *Spec) Rubric() *Check {
	for i := range s.Checks {
		if s.Checks[i].ReplyRubric != "" {
			return &s.Checks[i]
		}
	}
	return nil
}

// Files are the state files the checks read, in order, without repeats.
func (s *Spec) Files() []string {
	var out []string
	for _, c := range s.Checks {
		if c.State != nil && !contains(out, c.State.File) {
			out = append(out, c.State.File)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// stateFileRE is <service>/<export id>.
var stateFileRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

// Parse reads and validates a check file. Every problem is reported, one
// per line of the error.
func Parse(data []byte) (*Spec, error) {
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("%s is %d bytes; at most %d", FileName, len(data), MaxFileBytes)
	}
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s is empty", FileName)
		}
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	normalizeSpec(&s)
	if probs := Validate(&s); len(probs) > 0 {
		return &s, errors.New(strings.Join(probs, "\n"))
	}
	return &s, nil
}

// decodeStrict decodes n refusing unknown keys.
func decodeStrict(n *yaml.Node, out any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// Validate lists what is wrong with a decoded spec.
func Validate(s *Spec) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if s.Version != 0 && s.Version != 1 {
		add("version: %d; this bench reads version 1", s.Version)
	}
	if t := s.PassThreshold; t != nil && (*t <= 0 || *t > 1 || math.IsNaN(*t)) {
		add("pass_threshold: %v; want a share above 0 and at most 1", *t)
	}
	switch {
	case len(s.Checks) == 0:
		add("checks: empty; write at least one check")
	case len(s.Checks) > MaxChecks:
		add("checks: %d; at most %d", len(s.Checks), MaxChecks)
	}
	ids := map[string]bool{}
	rubrics, weighted := 0, 0.0
	for i, c := range s.Checks {
		at := fmt.Sprintf("checks[%d]", i)
		if c.ID != "" {
			at += " (" + c.ID + ")"
		}
		switch {
		case c.ID == "":
			add("%s: id is missing", at)
		case len(c.ID) > maxIDLen || !idRE.MatchString(c.ID):
			add("%s: id %q: lowercase letters, digits, '_' and '-', at most %d", at, c.ID, maxIDLen)
		case ids[c.ID]:
			add("%s: id %q is used by an earlier check", at, c.ID)
		}
		ids[c.ID] = true
		n := 0
		for _, set := range []bool{c.ToolCalled != nil, c.ToolNotCalled != nil, len(c.CallOrder) > 0, c.State != nil, c.ReplyRubric != ""} {
			if set {
				n++
			}
		}
		if n != 1 {
			add("%s: give exactly one of tool_called, tool_not_called, call_order, state, reply_rubric (found %d)", at, n)
			continue
		}
		if w := c.Weight; w != nil && (*w < 0 || *w > 1000 || math.IsNaN(*w)) {
			add("%s: weight %v; want 0 to 1000", at, *w)
		}
		weighted += c.WeightOf()
		switch c.Kind() {
		case KindToolCalled:
			out = append(out, validateCall(at+".tool_called", c.ToolCalled, true)...)
		case KindToolNotCalled:
			out = append(out, validateCall(at+".tool_not_called", c.ToolNotCalled, false)...)
			if c.ToolNotCalled.Times != nil || c.ToolNotCalled.MinTimes != nil || c.ToolNotCalled.MaxTimes != nil {
				add("%s.tool_not_called: times, min_times and max_times belong to tool_called", at)
			}
		case KindCallOrder:
			if len(c.CallOrder) < 2 {
				add("%s.call_order: name at least two calls, in the order they must happen", at)
			}
			for j := range c.CallOrder {
				cm := &c.CallOrder[j]
				out = append(out, validateCall(fmt.Sprintf("%s.call_order[%d]", at, j), cm, false)...)
				if cm.Times != nil || cm.MinTimes != nil || cm.MaxTimes != nil {
					add("%s.call_order[%d]: times, min_times and max_times belong to tool_called", at, j)
				}
			}
		case KindState:
			out = append(out, validateState(at+".state", c.State)...)
		case KindReplyRubric:
			rubrics++
			if r := []rune(strings.TrimSpace(c.ReplyRubric)); len(r) > MaxRubricRunes {
				add("%s.reply_rubric: %d characters; at most %d", at, len(r), MaxRubricRunes)
			}
		}
	}
	if rubrics > 1 {
		add("checks: %d reply_rubric checks; write one rubric with every criterion of the reply (the judge grades it as one list)", rubrics)
	}
	if weighted == 0 && len(s.Checks) > 0 {
		hasRequired := false
		for _, c := range s.Checks {
			hasRequired = hasRequired || c.Required
		}
		if !hasRequired {
			add("checks: every weight is 0 and none is required; nothing would decide the grade")
		}
	}
	return out
}

func validateCall(at string, c *CallMatch, called bool) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if strings.TrimSpace(c.Tool) == "" {
		add("%s: tool is missing", at)
	}
	switch c.Match {
	case "", MatchSubset:
	case MatchExact:
		if c.Args == nil {
			add("%s: match exact needs args (use args: {} for a call without arguments)", at)
		}
	default:
		add("%s: match %q; want subset or exact", at, c.Match)
	}
	for k, re := range c.ArgsRegex {
		if _, err := regexp.Compile(re); err != nil {
			add("%s.args_regex[%s]: %v", at, k, err)
		}
		if strings.HasPrefix(k, "$") {
			if _, err := ParsePath(k); err != nil {
				add("%s.args_regex[%s]: %v", at, k, err)
			}
		}
	}
	for i, pt := range c.ArgsPath {
		pat := fmt.Sprintf("%s.args_path[%d]", at, i)
		if _, err := ParsePath(pt.Path); err != nil {
			add("%s: %v", pat, err)
		}
		out = append(out, validateOps(pat, pt.ValueOps)...)
	}
	if called {
		for name, v := range map[string]*int{"times": c.Times, "min_times": c.MinTimes, "max_times": c.MaxTimes} {
			if v != nil && *v < 0 {
				add("%s: %s %d is negative", at, name, *v)
			}
		}
		if c.Times != nil && (c.MinTimes != nil || c.MaxTimes != nil) {
			add("%s: give times, or min_times and max_times, not both", at)
		}
		if c.Times != nil && *c.Times == 0 {
			add("%s: times 0 means the tool must not be called; write tool_not_called", at)
		}
		if c.MinTimes != nil && c.MaxTimes != nil && *c.MinTimes > *c.MaxTimes {
			add("%s: min_times %d is above max_times %d", at, *c.MinTimes, *c.MaxTimes)
		}
	}
	return out
}

func validateState(at string, s *StateCheck) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	if !stateFileRE.MatchString(s.File) {
		add("%s: file %q: want <service>/<export id>, such as helpdesk/db.json", at, s.File)
	}
	switch {
	case s.Path == "" && s.SQL == "":
		add("%s: give path (a JSON export) or sql (a SQLite export)", at)
	case s.Path != "" && s.SQL != "":
		add("%s: give path or sql, not both", at)
	case s.Path != "":
		if _, err := ParsePath(s.Path); err != nil {
			add("%s.path: %v", at, err)
		}
	default:
		if err := checkSQL(s.SQL); err != nil {
			add("%s.sql: %v", at, err)
		}
	}
	out = append(out, validateOps(at, s.ValueOps)...)
	return out
}

func validateOps(at string, v ValueOps) []string {
	var out []string
	if v.empty() {
		out = append(out, at+": give a test: equals, not_equals, in, contains, not_contains, matches, gte, lte, length, max_length or exists")
	}
	if v.Matches != "" {
		if _, err := regexp.Compile(v.Matches); err != nil {
			out = append(out, fmt.Sprintf("%s.matches: %v", at, err))
		}
	}
	return out
}

// normalizeSpec turns every YAML value a test compares into its JSON form
// (numbers become float64), so a comparison sees what a JSON export holds.
func normalizeSpec(s *Spec) {
	for i := range s.Checks {
		c := &s.Checks[i]
		for _, cm := range []*CallMatch{c.ToolCalled, c.ToolNotCalled} {
			if cm != nil {
				normalizeCall(cm)
			}
		}
		for j := range c.CallOrder {
			normalizeCall(&c.CallOrder[j])
		}
		if c.State != nil {
			normalizeOps(&c.State.ValueOps)
		}
	}
}

func normalizeCall(c *CallMatch) {
	if c.Args != nil {
		if m, ok := jsonForm(c.Args).(map[string]any); ok {
			c.Args = m
		}
	}
	for i := range c.ArgsPath {
		normalizeOps(&c.ArgsPath[i].ValueOps)
	}
}

func normalizeOps(v *ValueOps) {
	v.Equals, v.NotEquals, v.Contains, v.NotContains = jsonForm(v.Equals), jsonForm(v.NotEquals), jsonForm(v.Contains), jsonForm(v.NotContains)
	if v.In != nil {
		if l, ok := jsonForm(v.In).([]any); ok {
			v.In = l
		}
	}
}

// jsonForm round-trips v through JSON. nil stays nil.
func jsonForm(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return v
	}
	return out
}
