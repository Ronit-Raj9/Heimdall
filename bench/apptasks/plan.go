package apptasks

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

// Action is one tool call of a scenario.
type Action struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// Reply is one scripted customer reply (Y14 scenario row replies).
type Reply struct {
	When []string `json:"when"`
	Say  string   `json:"say"`
	Stop bool     `json:"stop,omitempty"`
}

// Scenario is one drafted conversation, as the assist writes it.
type Scenario struct {
	RuleID        string           `json:"rule_id"`
	Title         string           `json:"title"`
	Opening       string           `json:"opening_message"`
	Name          string           `json:"persona_name,omitempty"`
	Goal          string           `json:"persona_goal"`
	Facts         []string         `json:"facts,omitempty"`
	Unknown       []string         `json:"unknown,omitempty"`
	Behaviour     string           `json:"behaviour,omitempty"`
	SatisfiedWhen string           `json:"satisfied_when,omitempty"`
	Replies       []Reply          `json:"replies,omitempty"`
	AccountSeed   map[string][]any `json:"account_seed,omitempty"`
	Expected      []Action         `json:"expected_actions"`
	Forbidden     []Action         `json:"forbidden_actions,omitempty"`
	Ordered       bool             `json:"ordered,omitempty"`
	Reply         string           `json:"reference_reply,omitempty"`
	ReplyRubric   string           `json:"reply_rubric,omitempty"`
	Escalation    string           `json:"escalation,omitempty"`
}

// Proof is what the in-process run of a plan showed.
type Proof struct {
	Reference string            `json:"reference"`         // "passes"
	Controls  map[string]string `json:"controls"`          // control -> the check that fails it
	Skipped   []string          `json:"skipped,omitempty"` // checks only readiness can grade (reply rubric)
}

// Plan is a scenario that passed every gate: its checks and its proof.
type Plan struct {
	Scenario Scenario `json:"scenario"`
	Checks   []byte   `json:"-"`
	NChecks  int      `json:"checks"`
	Summary  []string `json:"summary"`
	Proof    Proof    `json:"proof"`
}

// Prover checks scenarios of one draft.
type Prover struct {
	Mock   *scaffold.MockSpec
	Seed   State
	Rules  []Rule
	Judged bool   // the benchmark pins a judge, so reply rubrics are kept
	App    string // the mock's service id
	seen   map[string]bool
}

// Prove runs every gate on one scenario. A failed gate is the reason the
// scenario is dropped, in words a reviewer reads.
func (p *Prover) Prove(sc Scenario) (*Plan, error) {
	if p.seen == nil {
		p.seen = map[string]bool{}
	}
	ops := map[string]scaffold.MockOperation{}
	for _, op := range p.Mock.Operations {
		ops[op.Name] = op
	}
	known := false
	for _, r := range p.Rules {
		known = known || r.ID == sc.RuleID
	}
	switch {
	case !known:
		return nil, fmt.Errorf("rule %q is not an approved rule", sc.RuleID)
	case strings.TrimSpace(sc.Opening) == "":
		return nil, fmt.Errorf("no opening message")
	case strings.TrimSpace(sc.Goal) == "":
		return nil, fmt.Errorf("no customer goal")
	case len(sc.Expected) == 0:
		return nil, fmt.Errorf("no expected action: an agent that does nothing would pass")
	case len(sc.Expected) > 8:
		return nil, fmt.Errorf("%d expected actions, at most 8", len(sc.Expected))
	case len(sc.Forbidden) > 4:
		return nil, fmt.Errorf("%d forbidden actions, at most 4", len(sc.Forbidden))
	}
	if sc.Escalation != "" && sc.Escalation != "expected" && sc.Escalation != "not_expected" {
		return nil, fmt.Errorf("escalation %q must be expected, not_expected or empty", sc.Escalation)
	}
	if !p.Judged {
		sc.ReplyRubric = ""
	}
	for _, list := range [][]Action{sc.Expected, sc.Forbidden} {
		for _, a := range list {
			op, ok := ops[a.Tool]
			if !ok {
				return nil, fmt.Errorf("%s is not an operation of the API", a.Tool)
			}
			if err := checkArgs(op, a.Args); err != nil {
				return nil, fmt.Errorf("%s: %v", a.Tool, err)
			}
		}
	}
	// Path and query parameters are strings in the tools' input schemas.
	for _, list := range [][]Action{sc.Expected, sc.Forbidden} {
		for i := range list {
			list[i].Args = stringParams(ops[list[i].Tool], list[i].Args)
		}
	}
	for _, a := range sc.Forbidden {
		if len(a.Args) == 0 && len(ops[a.Tool].PathParams) > 0 {
			return nil, fmt.Errorf("forbidden %s has no args, so no control can prove the check", a.Tool)
		}
	}
	seed := p.Seed.Clone()
	if len(sc.AccountSeed) > 0 {
		add := State(sc.AccountSeed)
		merged, ok := scaffold.MergeSeed(map[string]any(stateAny(seed)), map[string]any(stateAny(add))).(map[string]any)
		if !ok {
			return nil, fmt.Errorf("account_seed does not merge into the seed")
		}
		seed = anyState(merged)
	}
	if err := ValidateSeed(seed, p.Mock); err != nil {
		return nil, fmt.Errorf("account_seed: %v", err)
	}
	// The reference plan on the seed: every step must succeed.
	sim := NewSim(p.Mock, seed)
	start := sim.State.Clone()
	var ledger []schemas.ToolCall
	for i, a := range sc.Expected {
		st, resp, err := sim.Call(a.Tool, a.Args)
		if err != nil {
			return nil, err
		}
		if st >= 300 {
			return nil, fmt.Errorf("reference step %d (%s) fails on the seed with %d: %s", i+1, a.Tool, st, short(resp))
		}
		ledger = append(ledger, ledgerRow(len(ledger)+1, p.App, a))
	}
	end := sim.State.Clone()
	spec := checksDoc{Version: 1}
	used := map[string]bool{}
	var summary []string
	for _, a := range sc.Expected {
		op := ops[a.Tool]
		spec.add(used, "calls-"+a.Tool, "the agent calls "+a.Tool+describeIDs(identifying(op, a.Args)), true,
			map[string]any{"tool_called": callMatch(p.App, a.Tool, identifying(op, a.Args))})
	}
	if sc.Ordered && len(sc.Expected) > 1 {
		var steps []any
		for _, a := range sc.Expected {
			steps = append(steps, callMatch(p.App, a.Tool, identifying(ops[a.Tool], a.Args)))
		}
		spec.add(used, "in-order", "the expected calls happen in this order", false, map[string]any{"call_order": steps})
	}
	diff := stateChecks(p.App, start, end, "")
	for _, c := range diff {
		spec.add(used, c.id, c.desc, true, map[string]any{"state": c.state})
	}
	summary = append(summary, "calls "+toolList(sc.Expected))
	if len(diff) > 0 {
		summary = append(summary, fmt.Sprintf("%d state change(s) checked", len(diff)))
	}
	for _, a := range sc.Forbidden {
		op := ops[a.Tool]
		args := identifying(op, a.Args)
		// The same call as an expected one with other body values: match
		// the forbidden values too, or the reference would break the check.
		for _, e := range sc.Expected {
			if e.Tool == a.Tool && reflect.DeepEqual(identifying(ops[e.Tool], e.Args), args) {
				args = a.Args
				break
			}
		}
		spec.add(used, "never-"+a.Tool, "the agent never calls "+a.Tool+describeIDs(identifying(op, a.Args))+" (policy "+sc.RuleID+")", true,
			map[string]any{"tool_not_called": callMatch(p.App, a.Tool, args)})
		after := NewSim(p.Mock, end)
		if st, _, err := after.Call(a.Tool, a.Args); err == nil && st < 300 {
			for _, c := range stateChecks(p.App, end, after.State, sc.RuleID) {
				spec.add(used, c.id, c.desc, true, map[string]any{"state": c.state})
			}
		}
	}
	if len(sc.Forbidden) > 0 {
		summary = append(summary, "never "+toolList(sc.Forbidden))
	}
	if sc.ReplyRubric != "" {
		spec.add(used, "reply", "what the customer was told (policy "+sc.RuleID+")", false, map[string]any{"reply_rubric": sc.ReplyRubric})
		summary = append(summary, "reply: "+sc.ReplyRubric)
	}
	if len(spec.Checks) > taskchecks.MaxChecks {
		return nil, fmt.Errorf("%d checks, at most %d", len(spec.Checks), taskchecks.MaxChecks)
	}
	sig := signature(sc)
	if p.seen[sig] {
		return nil, fmt.Errorf("the same calls as another scenario")
	}
	raw, err := yaml.Marshal(spec)
	if err != nil {
		return nil, err
	}
	parsed, err := taskchecks.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("checks.yaml: %v", err)
	}
	proof, err := p.prove(parsed, sc, seed, ledger, end)
	if err != nil {
		return nil, err
	}
	p.seen[sig] = true
	header := fmt.Sprintf("# Checks of a task drafted from the API's OpenAPI file and policy rule %s.\n"+
		"# The checks grader reads this file (docs/declarative-checks.md); edit it freely.\n", sc.RuleID)
	return &Plan{Scenario: sc, Checks: append([]byte(header), raw...), NChecks: len(spec.Checks), Summary: summary, Proof: *proof}, nil
}

// prove grades the reference and the controls on the simulated ledger and
// state: the reference must pass and every control must fail.
func (p *Prover) prove(spec *taskchecks.Spec, sc Scenario, seed State, refLedger []schemas.ToolCall, refEnd State) (*Proof, error) {
	graded := &taskchecks.Spec{Version: spec.Version, PassThreshold: spec.PassThreshold}
	var skipped []string
	for _, c := range spec.Checks {
		if c.ReplyRubric != "" {
			skipped = append(skipped, c.ID)
			continue
		}
		graded.Checks = append(graded.Checks, c)
	}
	run := func(actions []Action) (bool, string) {
		sim := NewSim(p.Mock, seed)
		var rows []schemas.ToolCall
		for _, a := range actions {
			st, _, err := sim.Call(a.Tool, a.Args)
			row := ledgerRow(len(rows)+1, p.App, a)
			row.IsError = err != nil || st >= 400
			rows = append(rows, row)
		}
		in := taskchecks.Inputs{Calls: taskchecks.CallsFrom(rows), State: memState{p.App + "/state.json": stateAny(sim.State)}}
		res := taskchecks.Evaluate(graded, in)
		_, passed, invalid, reason := taskchecks.Score(graded, res)
		if invalid {
			return false, "invalid: " + reason
		}
		return passed, reason
	}
	pr := &Proof{Controls: map[string]string{}, Skipped: skipped}
	if ok, why := run(sc.Expected); !ok {
		return nil, fmt.Errorf("the reference plan fails its own checks: %s", why)
	}
	pr.Reference = "passes"
	controls := map[string][]Action{"empty": nil}
	key := sc.Expected[len(sc.Expected)-1]
	controls["skips "+key.Tool] = sc.Expected[:len(sc.Expected)-1]
	if len(sc.Forbidden) > 0 {
		controls["also "+sc.Forbidden[0].Tool] = append(append([]Action(nil), sc.Expected...), sc.Forbidden[0])
	}
	for _, name := range sortedKeys(controls) {
		ok, why := run(controls[name])
		if ok {
			return nil, fmt.Errorf("the %s control passes the checks, so they do not tell a wrong agent from a right one", name)
		}
		pr.Controls[name] = why
	}
	return pr, nil
}

// memState is a StateReader over in-memory JSON exports.
type memState map[string]any

func (m memState) JSON(file string) (any, error) {
	v, ok := m[file]
	if !ok {
		return nil, fmt.Errorf("no export %s", file)
	}
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func (m memState) Query(file, _ string) ([][]any, error) {
	return nil, fmt.Errorf("%s is not a SQLite export", file)
}

func ledgerRow(seq int, server string, a Action) schemas.ToolCall {
	b, _ := json.Marshal(a.Args)
	if a.Args == nil {
		b = []byte("{}")
	}
	return schemas.ToolCall{Seq: seq, Server: server, Tool: a.Tool, Arguments: string(b)}
}

// checkArgs refuses args the operation does not take.
func checkArgs(op scaffold.MockOperation, args map[string]any) error {
	known := map[string]bool{}
	for _, p := range op.PathParams {
		known[p] = true
	}
	for _, p := range op.QueryParams {
		known[p] = true
	}
	var bodyProps map[string]any
	if op.BodySchema != nil {
		bodyProps, _ = op.BodySchema["properties"].(map[string]any)
	}
	for k := range args {
		switch {
		case known[k]:
		case k == "body" && op.Body:
			if _, ok := args[k].(map[string]any); !ok {
				return fmt.Errorf("body must be a JSON object")
			}
		case op.Body && (bodyProps == nil || bodyProps[k] != nil):
			if ps, ok := bodyProps[k].(map[string]any); ok {
				if err := typeCheck(ps, args[k]); err != nil {
					return fmt.Errorf("body field %s %v", k, err)
				}
			}
		default:
			return fmt.Errorf("takes no argument %q", k)
		}
	}
	if b, ok := args["body"].(map[string]any); ok {
		for k, v := range b {
			if ps, ok := bodyProps[k].(map[string]any); ok {
				if err := typeCheck(ps, v); err != nil {
					return fmt.Errorf("body field %s %v", k, err)
				}
			}
		}
	}
	for _, p := range op.PathParams {
		if v, ok := args[p]; !ok || text(v) == "" {
			return fmt.Errorf("path parameter %s is missing", p)
		}
	}
	if op.BodyRequired && args["body"] == nil {
		flat := false
		for k := range args {
			flat = flat || !known[k]
		}
		if !flat {
			return fmt.Errorf("needs a body")
		}
	}
	return nil
}

// identifying keeps the args that pick what a call acts on (path and query
// parameters): a check on them holds whether the agent passes the body
// nested or flat, and the state checks grade what the body did.
func identifying(op scaffold.MockOperation, args map[string]any) map[string]any {
	out := map[string]any{}
	for _, p := range append(append([]string(nil), op.PathParams...), op.QueryParams...) {
		if v, ok := args[p]; ok {
			out[p] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringParams copies args with path and query parameter values as text.
func stringParams(op scaffold.MockOperation, args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	for _, p := range append(append([]string(nil), op.PathParams...), op.QueryParams...) {
		if v, ok := out[p]; ok {
			out[p] = text(v)
		}
	}
	return out
}

var (
	idSlugRE   = regexp.MustCompile(`[^a-z0-9]+`)
	plainKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func describeIDs(ids map[string]any) string {
	if len(ids) == 0 {
		return ""
	}
	var parts []string
	for _, k := range sortedKeys(ids) {
		parts = append(parts, fmt.Sprintf("%s %v", k, ids[k]))
	}
	return " for " + strings.Join(parts, ", ")
}

func callMatch(server, tool string, args map[string]any) map[string]any {
	m := map[string]any{"tool": tool, "server": server}
	if len(args) > 0 {
		m["args"] = args
	}
	return m
}

func toolList(as []Action) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Tool)
	}
	return strings.Join(out, ", ")
}

func signature(sc Scenario) string {
	b, _ := json.Marshal([]any{sc.Expected, sc.Forbidden, sc.AccountSeed})
	return string(b)
}

// checksDoc is checks.yaml as written.
type checksDoc struct {
	Version int              `yaml:"version"`
	Checks  []map[string]any `yaml:"checks"`
}

func (d *checksDoc) add(used map[string]bool, id, desc string, required bool, body map[string]any) {
	id = checkID(id, used)
	c := map[string]any{"id": id, "description": desc}
	if required {
		c["required"] = true
	}
	for k, v := range body {
		c[k] = v
	}
	d.Checks = append(d.Checks, c)
}

func checkID(base string, used map[string]bool) string {
	b := strings.Trim(idSlugRE.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if len(b) > 56 {
		b = b[:56]
	}
	id := b
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s-%d", b, n)
	}
	used[id] = true
	return id
}

// stateCheck is one derived state assertion.
type stateCheck struct {
	id, desc string
	state    map[string]any
}

// stateChecks turns the difference between two states into checks on the
// service's exported state.json. With rule set, the difference is what a
// forbidden call would do, and the checks assert it did not happen.
func stateChecks(app string, before, after State, rule string) []stateCheck {
	file := app + "/state.json"
	var out []stateCheck
	for _, coll := range sortedKeys(after) {
		if coll == "_actions" {
			continue
		}
		old := byID(before[coll])
		now := byID(after[coll])
		for _, id := range sortedKeys(now) {
			item := now[id]
			was, existed := old[id]
			if !existed {
				if rule != "" {
					out = append(out, stateCheck{id: "never-creates-" + coll, desc: fmt.Sprintf("no %s %s was created (policy %s)", coll, id, rule),
						state: map[string]any{"file": file, "path": filterPath(coll, "id", item["id"], ""), "length": 0}})
					continue
				}
				out = append(out, createdChecks(file, coll, item, before[coll])...)
				continue
			}
			for _, f := range sortedKeys(item) {
				if reflect.DeepEqual(was[f], item[f]) {
					continue
				}
				if v, ok := item[f].(string); ok && freeText(v) {
					continue // a note in the agent's own words
				}
				p := filterPath(coll, "id", item["id"], f)
				if p == "" {
					continue
				}
				if rule != "" {
					out = append(out, stateCheck{id: "not-" + coll + "-" + f, desc: fmt.Sprintf("%s %s %s is not %s (policy %s)", coll, id, f, short(item[f]), rule),
						state: map[string]any{"file": file, "path": p, "not_equals": item[f]}})
					continue
				}
				out = append(out, stateCheck{id: coll + "-" + f, desc: fmt.Sprintf("%s %s %s is %s", coll, id, f, short(item[f])),
					state: map[string]any{"file": file, "path": p, "equals": item[f]}})
			}
		}
		for _, id := range sortedKeys(old) {
			if _, ok := now[id]; ok {
				continue
			}
			p := filterPath(coll, "id", old[id]["id"], "")
			if p == "" {
				continue
			}
			if rule != "" {
				out = append(out, stateCheck{id: "keeps-" + coll, desc: fmt.Sprintf("%s %s is not deleted (policy %s)", coll, id, rule),
					state: map[string]any{"file": file, "path": p, "length": 1}})
				continue
			}
			out = append(out, stateCheck{id: "deletes-" + coll, desc: fmt.Sprintf("%s %s is deleted", coll, id),
				state: map[string]any{"file": file, "path": p, "length": 0}})
		}
	}
	return out
}

// createdChecks finds a field that tells the new item apart from every
// record that was there (the mock makes ids by count, so an agent's id may
// differ) and checks one item has it, plus the item's other plain fields.
func createdChecks(file, coll string, item map[string]any, before []any) []stateCheck {
	for _, f := range sortedKeys(item) {
		v, ok := item[f].(string)
		if !ok || f == "id" || v == "" || freeText(v) {
			continue
		}
		clash := false
		for _, b := range before {
			if m, ok := b.(map[string]any); ok && m[f] == v {
				clash = true
			}
		}
		p := filterPath(coll, f, v, "")
		if clash || p == "" {
			continue
		}
		out := []stateCheck{{id: "creates-" + coll, desc: fmt.Sprintf("one new item in %s has %s %q", coll, f, v),
			state: map[string]any{"file": file, "path": p, "length": 1}}}
		for _, g := range sortedKeys(item) {
			if g == f || g == "id" {
				continue
			}
			switch x := item[g].(type) {
			case string:
				if freeText(x) {
					continue
				}
			case float64, bool:
			default:
				continue
			}
			out = append(out, stateCheck{id: "created-" + coll + "-" + g, desc: fmt.Sprintf("the new item in %s has %s %s", coll, g, short(item[g])),
				state: map[string]any{"file": file, "path": filterPath(coll, f, v, g), "equals": item[g]}})
			if len(out) >= 6 {
				break
			}
		}
		return out
	}
	return nil
}

// filterPath is $.coll[?(@.field == value)] (and .sub): "" when a value
// could not be written safely in a filter.
func filterPath(coll, field string, value any, sub string) string {
	if !plainKey(coll) || !plainKey(field) || (sub != "" && !plainKey(sub)) {
		return ""
	}
	b, err := json.Marshal(value)
	if err != nil || strings.Contains(string(b), ")]") {
		return ""
	}
	switch value.(type) {
	case string, float64:
	default:
		return ""
	}
	p := fmt.Sprintf("$.%s[?(@.%s == %s)]", coll, field, b)
	if sub != "" {
		p += "." + sub
	}
	return p
}

// freeText is a string an agent writes in its own words (a subject, a
// note): it is not checked for equality.
func freeText(s string) bool { return strings.ContainsAny(s, " \t\n") || len(s) > 40 }

func plainKey(s string) bool { return s != "" && plainKeyRE.MatchString(s) }

func byID(items []any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if id := text(m["id"]); id != "" {
				out[id] = m
			}
		}
	}
	return out
}

func stateAny(s State) map[string]any {
	out := map[string]any{}
	for k, v := range s {
		out[k] = v
	}
	return out
}

func anyState(m map[string]any) State {
	out := State{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if l, ok := m[k].([]any); ok {
			out[k] = l
		}
	}
	return out
}
