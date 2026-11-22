package taskchecks

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const sampleSpec = `
version: 1
checks:
  - id: found-customer
    tool_called: {tool: find_customer, args: {email: mia.torres@example.com}}
  - id: refund-exact
    required: true
    weight: 3
    tool_called:
      tool: issue_refund
      server: helpdesk
      args: {order_id: B-3001, amount_pence: 4500}
      args_regex: {reason: "(?i)damag|crack"}
      times: 1
  - id: no-credit
    tool_not_called: {tool: apply_credit}
  - id: verify-first
    call_order: [find_customer, {tool: issue_refund, args: {order_id: B-3001}}]
  - id: order-refunded
    state: {file: helpdesk/db.json, path: "$.orders[?(@.id=='B-3001')].status", equals: refunded}
  - id: one-refund
    state: {file: helpdesk/db.json, path: refunds, length: 1}
  - id: slash-path
    state: {file: helpdesk/db.json, path: orders/id=B-3001/refunded_pence, gte: 4500}
`

func ledger(rows ...schemas.ToolCall) []Call {
	for i := range rows {
		rows[i].Seq = i + 1
	}
	return CallsFrom(rows)
}

func row(server, tool, args string) schemas.ToolCall {
	return schemas.ToolCall{Server: server, Tool: tool, Arguments: args, Protocol: "mcp"}
}

type mapState map[string]any

func (m mapState) JSON(file string) (any, error) {
	v, ok := m[file]
	if !ok {
		return nil, os.ErrNotExist
	}
	return jsonForm(v), nil
}
func (m mapState) Query(string, string) ([][]any, error) { return nil, os.ErrInvalid }

func goodState() mapState {
	return mapState{"helpdesk/db.json": map[string]any{
		"orders":  []any{map[string]any{"id": "B-3001", "status": "refunded", "refunded_pence": 4500}, map[string]any{"id": "B-3002", "status": "delivered"}},
		"refunds": []any{map[string]any{"order_id": "B-3001", "amount_pence": 4500}},
	}}
}

func TestEvaluatePassAndFail(t *testing.T) {
	s, err := Parse([]byte(sampleSpec))
	if err != nil {
		t.Fatal(err)
	}
	good := ledger(
		row("customer", "respond_to_user", `{"message":"hi"}`),
		row("helpdesk", "find_customer", `{"email":"mia.torres@example.com"}`),
		row("helpdesk", "issue_refund", `{"order_id":"B-3001","amount_pence":4500,"reason":"arrived damaged"}`),
	)
	res := Evaluate(s, Inputs{Calls: good, State: goodState()})
	score, passed, invalid, reason := Score(s, res)
	if !passed || invalid || score != 1 {
		t.Fatalf("good run: score %v passed %v invalid %v: %s %+v", score, passed, invalid, reason, res)
	}

	// Refund before lookup, plus a credit: order and not-called fail, and
	// the required refund still passes, so the score is partial.
	bad := ledger(
		row("helpdesk", "issue_refund", `{"order_id":"B-3001","amount_pence":4500,"reason":"cracked"}`),
		row("helpdesk", "find_customer", `{"email":"mia.torres@example.com"}`),
		row("helpdesk", "apply_credit", `{"amount_pence":500}`),
	)
	res = Evaluate(s, Inputs{Calls: bad, State: goodState()})
	score, passed, _, reason = Score(s, res)
	if passed || score <= 0 || score >= 1 {
		t.Fatalf("bad run: score %v passed %v: %s", score, passed, reason)
	}
	if !strings.Contains(reason, "no-credit") || !strings.Contains(reason, "verify-first") {
		t.Fatalf("reason %q should name the failed checks", reason)
	}

	// Wrong amount: the required check fails even with a lowered threshold.
	th := 0.1
	s.PassThreshold = &th
	wrong := ledger(row("helpdesk", "find_customer", `{"email":"mia.torres@example.com"}`),
		row("helpdesk", "issue_refund", `{"order_id":"B-3001","amount_pence":450,"reason":"damaged"}`))
	res = Evaluate(s, Inputs{Calls: wrong, State: goodState()})
	if _, passed, _, reason = Score(s, res); passed || !strings.Contains(reason, "required check failed: refund-exact") {
		t.Fatalf("wrong amount passed or reason %q", reason)
	}
	for _, r := range res {
		if r.ID == "refund-exact" && !strings.Contains(r.Detail, "amount_pence is 450, want 4500") {
			t.Fatalf("near-miss detail %q", r.Detail)
		}
	}
}

func TestMissingStateIsInvalid(t *testing.T) {
	s, err := Parse([]byte(sampleSpec))
	if err != nil {
		t.Fatal(err)
	}
	res := Evaluate(s, Inputs{State: mapState{}})
	if _, passed, invalid, reason := Score(s, res); passed || !invalid || !strings.Contains(reason, "order-refunded") {
		t.Fatalf("missing export: passed %v invalid %v %q", passed, invalid, reason)
	}
}

func TestArgsMatching(t *testing.T) {
	calls := ledger(row("svc", "create", `{"a":1,"b":{"c":"x","d":[1,2]},"e":true}`))
	cases := []struct {
		name string
		m    CallMatch
		want bool
	}{
		{"subset", CallMatch{Tool: "create", Args: map[string]any{"a": 1.0}}, true},
		{"nested subset", CallMatch{Tool: "create", Args: map[string]any{"b": map[string]any{"c": "x"}}}, true},
		{"bool is not 1", CallMatch{Tool: "create", Args: map[string]any{"e": 1.0}}, false},
		{"exact miss", CallMatch{Tool: "create", Match: MatchExact, Args: map[string]any{"a": 1.0}}, false},
		{"exact hit", CallMatch{Tool: "create", Match: MatchExact, Args: map[string]any{"a": 1.0, "b": map[string]any{"c": "x", "d": []any{1.0, 2.0}}, "e": true}}, true},
		{"regex key", CallMatch{Tool: "create", ArgsRegex: map[string]string{"$.b.c": "^x$"}}, true},
		{"regex miss", CallMatch{Tool: "create", ArgsRegex: map[string]string{"a": "^2$"}}, false},
		{"path", CallMatch{Tool: "create", ArgsPath: []PathTest{{Path: "$.b.d", ValueOps: ValueOps{Length: ptr(2)}}}}, true},
		{"path wildcard any", CallMatch{Tool: "create", ArgsPath: []PathTest{{Path: "$.b.d[*]", ValueOps: ValueOps{Equals: 2.0}}}}, true},
		{"path wildcard all", CallMatch{Tool: "create", ArgsPath: []PathTest{{Path: "$.b.d[*]", ValueOps: ValueOps{Equals: 2.0, All: true}}}}, false},
		{"server", CallMatch{Tool: "create", Server: "other"}, false},
		{"succeeded", CallMatch{Tool: "create", Succeeded: ptr(false)}, false},
	}
	for _, c := range cases {
		n, _ := countMatches(&c.m, calls)
		if (n == 1) != c.want {
			t.Errorf("%s: matched %d, want %v", c.name, n, c.want)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestPaths(t *testing.T) {
	doc := jsonForm(map[string]any{"a": []any{map[string]any{"k": "x", "n": 1, "o": map[string]any{"id": 7}}, map[string]any{"k": "y", "n": 2}}, "b c": 3})
	cases := []struct {
		path string
		n    int
		def  bool
	}{
		{"$.a[0].k", 1, true},
		{"$.a[-1].n", 1, true},
		{"$.a[*].k", 2, false},
		{"$.a[?(@.k=='y')].n", 1, false},
		{"$.a[?(@.n != 1)]", 1, false},
		{"$.a[?(@.o.id == 7)].k", 1, false},
		{"$['b c']", 1, true},
		{"$.*", 2, false},
		{"a/k=y/n", 1, true},
		{"a/1/k", 1, true},
		{"$.missing", 0, true},
	}
	for _, c := range cases {
		p, err := ParsePath(c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if got := p.Eval(doc); len(got) != c.n || p.Definite() != c.def {
			t.Errorf("%s: %d values (definite %v), want %d (%v)", c.path, len(got), p.Definite(), c.n, c.def)
		}
	}
	for _, bad := range []string{"$..a", "$.a[", "$.a[?(@k==1)]", "$.a[x]", "$a"} {
		if _, err := ParsePath(bad); err == nil {
			t.Errorf("%s: parsed", bad)
		}
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	_, err := Parse([]byte(`
checks:
  - id: Bad Id
    tool_called: {tool: x}
  - id: two
    tool_called: {tool: x}
    state: {file: a/b, path: x, equals: 1}
  - id: two
    call_order: [only_one]
  - id: st
    state: {file: nofile, sql: "DELETE FROM t"}
  - id: r1
    reply_rubric: be kind
  - id: r2
    reply_rubric: be brief
  - id: rx
    tool_called: {tool: y, args_regex: {a: "("}, times: 0}
`))
	if err == nil {
		t.Fatal("bad spec parsed")
	}
	for _, want := range []string{"Bad Id", "exactly one of", `id "two" is used`, "at least two calls", "<service>/<export id>", "one SELECT", "2 reply_rubric", "args_regex[a]", "times 0", "give a test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	if _, err := Parse([]byte("checks:\n  - id: a\n    tool_caled: {tool: x}\n")); err == nil || !strings.Contains(err.Error(), "tool_caled") {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := Parse([]byte("")); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file: %v", err)
	}
}

func TestSQLiteState(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "crm"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "crm", "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE TABLE refunds (order_id TEXT, amount INTEGER)", "INSERT INTO refunds VALUES ('B-1', 4500), ('B-2', 100)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := os.WriteFile(filepath.Join(dir, "crm", "db.json"), []byte(`{"x": [1,2]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Parse([]byte(`
checks:
  - id: sum
    state: {file: crm/state.db, sql: "SELECT sum(amount) FROM refunds WHERE order_id = 'B-1'", equals: 4500}
  - id: rows
    state: {file: crm/state.db, sql: "SELECT * FROM refunds", length: 2}
  - id: json
    state: {file: crm/db.json, path: $.x, length: 2}
`))
	if err != nil {
		t.Fatal(err)
	}
	res := Evaluate(s, Inputs{State: NewDirState(dir)})
	if _, passed, invalid, reason := Score(s, res); !passed || invalid {
		t.Fatalf("sqlite: %s %+v", reason, res)
	}
	// The export is never changed by a query.
	st := NewDirState(dir)
	if _, err := st.Query("crm/state.db", "SELECT 1; DELETE FROM refunds"); err == nil {
		t.Fatal("two statements ran")
	}
	if _, err := st.Query("crm/db.json", "SELECT 1"); err == nil || !strings.Contains(err.Error(), "not a SQLite") {
		t.Fatalf("json as sqlite: %v", err)
	}
	if _, err := st.JSON("../etc/passwd"); err == nil {
		t.Fatal("path left the state folder")
	}
	if err := checkSQL("SELECT 'delete me' FROM t"); err != nil {
		t.Fatalf("word inside a string: %v", err)
	}
}

func TestReadLedger(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tools.jsonl")
	if rows, err := ReadLedger(p); err != nil || rows != nil {
		t.Fatalf("missing ledger: %v %v", rows, err)
	}
	data := `{"seq":2,"server":"s","tool":"b","protocol":"mcp","args_sha256":"","result_sha256":"","is_error":false,"started":"2026-11-22T00:00:00Z","latency_ms":1}
{"seq":1,"server":"s","tool":"a","protocol":"mcp","args_sha256":"","result_sha256":"","is_error":true,"started":"2026-11-22T00:00:00Z","latency_ms":1}
{"seq":3,"kind":"session","server":"s","args_sha256":"","result_sha256":"","is_error":false,"started":"2026-11-22T00:00:00Z","latency_ms":1}
`
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadLedger(p)
	if err != nil {
		t.Fatal(err)
	}
	calls := CallsFrom(rows)
	if len(calls) != 2 || calls[0].Tool != "a" || !calls[0].IsError {
		t.Fatalf("calls %+v", calls)
	}
}

func TestScoreWeights(t *testing.T) {
	s := &Spec{Checks: []Check{{ID: "a"}, {ID: "b", Weight: ptr(3.0)}, {ID: "c", Weight: ptr(0.0)}}}
	res := []Result{{ID: "a", Weight: 1, Passed: false}, {ID: "b", Weight: 3, Passed: true}, {ID: "c", Weight: 0, Passed: false}}
	s.PassThreshold = ptr(0.75)
	score, passed, _, _ := Score(s, res)
	if score != 0.75 || !passed {
		t.Fatalf("score %v passed %v", score, passed)
	}
	res[2].Required = true
	if _, passed, _, _ = Score(s, res); passed {
		t.Fatal("a failed required check passed")
	}
	res = append(res, Result{ID: "r", Pending: true, Detail: "not judged"})
	if _, _, invalid, _ := Score(s, res); !invalid {
		t.Fatal("pending rubric was not undecided")
	}
}
