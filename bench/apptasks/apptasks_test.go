package apptasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

const rulesReply = `{"rules": [
 {"id": "R1", "text": "Delivered orders may be refunded in full within 30 days of delivery: set status refunded and refund_amount to the order total.", "kind": "must", "operations": ["getOrder", "updateOrder"]},
 {"id": "R2", "text": "Never refund an order delivered more than 30 days ago.", "kind": "must_not", "operations": ["updateOrder"]},
 {"id": "R3", "text": "Orders that have not shipped may be cancelled.", "kind": "must", "operations": ["updateOrder"]},
 {"id": "R4", "text": "A safety issue gets a high priority ticket for the team.", "kind": "escalate", "operations": ["createTicket"]}
]}`

const seedReply = `{"collections": {
 "orders": [
  {"id": "O-1", "customer_id": "C-1", "status": "delivered", "total": 45, "refund_amount": 0, "delivered_at": "2026-11-10"},
  {"id": "O-2", "customer_id": "C-2", "status": "delivered", "total": 80, "refund_amount": 0, "delivered_at": "2026-09-01"},
  {"id": "O-3", "customer_id": "C-1", "status": "placed", "total": 20, "refund_amount": 0, "delivered_at": ""}
 ],
 "customers": [{"id": "C-1", "name": "Mia Torres"}, {"id": "C-2", "name": "Sam Lee"}],
 "tickets": []
}}`

var scenarioReplies = map[string]string{
	"R1": `{"scenarios": [
  {"rule_id": "R1", "title": "Refund a recent delivery", "opening_message": "Hi, order O-1 arrived broken, can I get a refund?", "persona_name": "Mia Torres",
   "persona_goal": "Get order O-1 refunded", "facts": ["Order O-1"], "replies": [{"when": ["refunded"], "say": "Thanks!", "stop": true}],
   "expected_actions": [{"tool": "getOrder", "args": {"order_id": "O-1"}}, {"tool": "updateOrder", "args": {"order_id": "O-1", "body": {"status": "refunded", "refund_amount": 45}}}],
   "reference_reply": "I have refunded 45 for order O-1.", "escalation": "not_expected"},
  {"rule_id": "R1", "title": "Refund a missing order", "opening_message": "Refund O-99 please", "persona_goal": "Refund",
   "expected_actions": [{"tool": "getOrder", "args": {"order_id": "O-99"}}]}
 ]}`,
	"R2": `{"scenarios": [
  {"rule_id": "R2", "title": "Refuse an old refund", "opening_message": "I want my money back for O-2.", "persona_goal": "Refund O-2",
   "expected_actions": [{"tool": "getOrder", "args": {"order_id": "O-2"}}],
   "forbidden_actions": [{"tool": "updateOrder", "args": {"order_id": "O-2", "body": {"status": "refunded", "refund_amount": 80}}}],
   "reply_rubric": "Says the order is past the 30 day refund window.", "escalation": "not_expected"}
 ]}`,
	"R3": `not json`,
	"R4": `{"scenarios": [
  {"rule_id": "R4", "title": "Kettle lid burns", "opening_message": "The kettle lid from O-1 got so hot it burned me.", "persona_goal": "Report a safety problem",
   "expected_actions": [{"tool": "createTicket", "args": {"body": {"customer_id": "C-1", "subject": "Kettle lid burn, order O-1", "priority": "high"}}}],
   "escalation": "expected"},
  {"rule_id": "R4", "title": "Same calls again", "opening_message": "Burned again", "persona_goal": "Report",
   "expected_actions": [{"tool": "createTicket", "args": {"body": {"customer_id": "C-1", "subject": "Kettle lid burn, order O-1", "priority": "high"}}}]},
  {"rule_id": "R4", "title": "Bad arg", "opening_message": "x", "persona_goal": "y",
   "expected_actions": [{"tool": "createTicket", "args": {"body": {"customer_id": "C-1", "subject": "s", "priority": "urgent"}}}]}
 ]}`,
}

// fakeChat answers each op from canned replies; R3's first answers are
// not JSON, to exercise the retries.
type fakeChat struct {
	mu    sync.Mutex
	calls []string
	r3    int
}

func (f *fakeChat) Chat(_ context.Context, _, user string, _ int) (string, *schemas.Tokens, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := &schemas.Tokens{Fresh: 1000, Out: 500}
	switch {
	case strings.HasPrefix(user, "Read the company's API"):
		f.calls = append(f.calls, OpRules)
		return rulesReply, tok, "test-model", nil
	case strings.HasPrefix(user, "Write the starting data"):
		f.calls = append(f.calls, OpSeed)
		return "```json\n" + seedReply + "\n```", tok, "test-model", nil
	}
	f.calls = append(f.calls, OpScenarios)
	for id, r := range scenarioReplies {
		if strings.Contains(user, `"id": "`+id+`"`) {
			if id == "R3" {
				f.r3++
				if f.r3 == 3 {
					return `{"scenarios": [{"rule_id": "R3", "title": "Cancel before shipping", "opening_message": "Please cancel O-3", "persona_goal": "Cancel O-3",
					 "expected_actions": [{"tool": "updateOrder", "args": {"order_id": "O-3", "status": "cancelled"}}]}]}`, tok, "test-model", nil
				}
			}
			return r, tok, "test-model", nil
		}
	}
	return `{}`, tok, "test-model", nil
}

func price(_ string, t schemas.Tokens) (float64, bool) {
	return float64(t.Fresh)*1e-6 + float64(t.Out)*5e-6, true
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newDraft(t *testing.T) (Store, *Draft, *scaffold.MockSpec) {
	t.Helper()
	st := Store{Dir: t.TempDir()}
	d, m, err := st.Create(Upload{Suite: "acme-support", App: "shop", Spec: readTestdata(t, "shop.yaml"), SpecName: "shop.yaml",
		Policy: readTestdata(t, "policy.md"), By: "ana@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return st, d, m
}

func TestGenerateEndToEnd(t *testing.T) {
	st, d, m := newDraft(t)
	if d.Title != "Acme Shop API" || len(m.Operations) != 5 {
		t.Fatalf("draft %+v ops %+v", d, m.Operations)
	}
	fc := &fakeChat{}
	a := &Assistant{Chat: fc, Model: Pinned{Provider: "openai", Model: "test-model"}, Budget: &Budget{MaxCalls: 50, MaxUSD: 1, Price: price, RequirePrice: true}}
	_, policy, err := st.Files(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := GenerateTasks(context.Background(), a, d, m, policy, GenerateOptions{}); err == nil {
		t.Fatal("tasks before the rules are approved")
	}
	if err := DraftRules(context.Background(), a, d, m, policy); err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusRulesDrafted || len(d.Rules) != 4 {
		t.Fatalf("rules %+v", d)
	}
	bad := append([]Rule(nil), d.Rules...)
	bad[0].Operations = []string{"refundEverything"}
	if err := ApproveRules(d, m, bad, "ana"); err == nil || !strings.Contains(err.Error(), "refundEverything") {
		t.Fatalf("bad rule approved: %v", err)
	}
	if err := ApproveRules(d, m, d.Rules, "ana"); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTasks(context.Background(), a, d, m, policy, GenerateOptions{Judged: true}); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, p := range d.Plans {
		titles = append(titles, p.Scenario.Title)
	}
	want := "Refund a recent delivery|Refuse an old refund|Cancel before shipping|Kettle lid burns"
	if strings.Join(titles, "|") != want {
		t.Fatalf("plans %q, dropped %+v", titles, d.Dropped)
	}
	reasons := map[string]string{}
	for _, dr := range d.Dropped {
		reasons[dr.Title] = dr.Reason
	}
	for title, want := range map[string]string{"Refund a missing order": "fails on the seed with 404", "Same calls again": "same calls", "Bad arg": "not one of the spec's values"} {
		if !strings.Contains(reasons[title], want) {
			t.Errorf("%s: dropped for %q, want %q (all: %+v)", title, reasons[title], want, d.Dropped)
		}
	}
	// R3 needed two retries; every call is recorded with its prompt hash.
	if n := strings.Count(strings.Join(fc.calls, ","), OpScenarios); n != 6 {
		t.Errorf("scenario calls %d (%v)", n, fc.calls)
	}
	for _, c := range d.Calls {
		if c.PromptSHA == "" || c.Model != "openai/test-model" {
			t.Errorf("call %+v", c)
		}
	}
	calls, usd := a.Budget.Spent()
	if calls != len(fc.calls) || usd <= 0 || d.SpentUSD <= 0 {
		t.Errorf("spent %d $%f draft $%f", calls, usd, d.SpentUSD)
	}

	refund := d.Plans[0]
	if refund.Proof.Reference != "passes" || len(refund.Proof.Controls) != 2 {
		t.Fatalf("proof %+v", refund.Proof)
	}
	checks := string(refund.Checks)
	for _, want := range []string{`path: $.orders[?(@.id == "O-1")].status`, "equals: refunded", "refund_amount", "tool_called:", "order_id: O-1"} {
		if !strings.Contains(checks, want) {
			t.Errorf("refund checks miss %q:\n%s", want, checks)
		}
	}
	old := string(d.Plans[1].Checks)
	for _, want := range []string{"tool_not_called:", "not_equals: refunded", "reply_rubric:", "policy R2"} {
		if !strings.Contains(old, want) {
			t.Errorf("old refund checks miss %q:\n%s", want, old)
		}
	}
	if _, ok := d.Plans[1].Proof.Controls["also updateOrder"]; !ok {
		t.Errorf("forbidden control not proved: %+v", d.Plans[1].Proof)
	}
	ticket := string(d.Plans[3].Checks)
	if !strings.Contains(ticket, `$.tickets[?(@.customer_id == "C-1")]`) || !strings.Contains(ticket, "length: 1") ||
		!strings.Contains(ticket, "equals: high") || strings.Contains(ticket, "equals: Kettle") {
		t.Errorf("created item checks:\n%s", ticket)
	}

	// The draft survives a reload, checks included.
	if err := st.Save(d); err != nil {
		t.Fatal(err)
	}
	back, err := st.Load(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Plans) != 4 || string(back.Plans[0].Checks) != checks {
		t.Fatalf("reloaded plans %d", len(back.Plans))
	}
	list, _ := st.List()
	if len(list) != 1 {
		t.Fatalf("list %d", len(list))
	}

	// Written as a benchmark: it loads and lints clean, and each task is
	// pending review with the plan's checks.
	root := t.TempDir()
	written, err := WriteBenchmark(filepath.Join(root, "acme-support"), st, back, &scaffold.JudgeConfig{Provider: "openai", Model: "test-model"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 || written[0].Review != ReviewPending {
		t.Fatalf("written %+v", written)
	}
	s, err := suite.Builtin(root)[0].Load(context.Background(), "acme-support")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint: %s: %s", p.Path, p.Message)
		}
	}
	if s.Grader != "checks" || len(s.Tasks) != 4 {
		t.Fatalf("suite grader %s tasks %d", s.Grader, len(s.Tasks))
	}
	for _, tk := range s.Tasks {
		if tk.Provenance == nil || tk.Provenance.InstructionReviewed == nil || *tk.Provenance.InstructionReviewed || tk.Provenance.CreatedBy != CreatedBy {
			t.Errorf("task %s provenance %+v", tk.ID, tk.Provenance)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "acme-support", "tasks", written[0].ID, "verifier", "checks.yaml"))
	if err != nil || string(b) != checks {
		t.Fatalf("checks.yaml on disk differs: %v", err)
	}
	instr, _ := os.ReadFile(filepath.Join(root, "acme-support", "tasks", written[0].ID, "instruction.md"))
	if !strings.Contains(string(instr), "R2. Never refund an order delivered more than 30 days ago.") {
		t.Errorf("instruction has no policy:\n%s", instr)
	}
	if _, err := os.Stat(filepath.Join(root, "acme-support", "services", "shop", "simuser.py")); err != nil {
		t.Error(err)
	}

	// A second write adds only new tasks; review accept and reject.
	if _, err := WriteBenchmark(filepath.Join(root, "acme-support"), st, back, nil, false); err == nil {
		t.Fatal("the same draft written twice")
	}
	if err := Review(filepath.Join(root, "acme-support"), back, written[0].ID, ReviewAccepted, "ana"); err != nil {
		t.Fatal(err)
	}
	ty, _ := os.ReadFile(filepath.Join(root, "acme-support", "tasks", written[0].ID, "task.yaml"))
	if !strings.Contains(string(ty), "instruction_reviewed: true") {
		t.Errorf("accepted task.yaml:\n%s", ty)
	}
	if err := Review(filepath.Join(root, "acme-support"), back, written[1].ID, ReviewRejected, "ana"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "acme-support", "tasks", written[1].ID)); !os.IsNotExist(err) {
		t.Errorf("rejected task still there: %v", err)
	}
	sy, _ := os.ReadFile(filepath.Join(root, "acme-support", "suite.yaml"))
	if ids, _ := SuiteTasks(sy); len(ids) != 3 {
		t.Errorf("suite tasks after reject %v", ids)
	}
}

func TestBudgetStops(t *testing.T) {
	st, d, m := newDraft(t)
	_, policy, _ := st.Files(d)
	a := &Assistant{Chat: &fakeChat{}, Model: Pinned{Provider: "openai", Model: "test-model"}, Budget: &Budget{MaxCalls: 2, Price: price}}
	if err := DraftRules(context.Background(), a, d, m, policy); err != nil {
		t.Fatal(err)
	}
	_ = ApproveRules(d, m, d.Rules, "ana")
	err := GenerateTasks(context.Background(), a, d, m, policy, GenerateOptions{})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("want the budget to stop it, got %v", err)
	}
	if n, _ := a.Budget.Spent(); n != 2 {
		t.Fatalf("calls %d", n)
	}
	// The USD cap holds before a call is sent.
	a = &Assistant{Chat: &fakeChat{}, Model: Pinned{Provider: "openai", Model: "test-model"}, Budget: &Budget{MaxUSD: 0.00001, Price: price}}
	if err := DraftRules(context.Background(), a, d, m, policy); !errors.Is(err, ErrBudget) {
		t.Fatalf("USD cap: %v", err)
	}
	// Hosted: an unpriced model is refused.
	a = &Assistant{Chat: &fakeChat{}, Model: Pinned{Provider: "openai", Model: "test-model"}, Budget: &Budget{MaxUSD: 1, RequirePrice: true}}
	if err := DraftRules(context.Background(), a, d, m, policy); err == nil || !strings.Contains(err.Error(), "price snapshot") {
		t.Fatalf("unpriced: %v", err)
	}
}

func TestUploadChecks(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	spec := readTestdata(t, "shop.yaml")
	for name, u := range map[string]Upload{
		"pdf policy":   {Suite: "a", App: "shop", Spec: spec, Policy: []byte("%PDF-1.7 ...")},
		"empty policy": {Suite: "a", App: "shop", Spec: spec, Policy: []byte("  ")},
		"swagger":      {Suite: "a", App: "shop", Spec: []byte("swagger: '2.0'\npaths: {}\n"), Policy: []byte("x")},
		"app customer": {Suite: "a", App: "customer", Spec: spec, Policy: []byte("x")},
		"bad suite":    {Suite: "A B", App: "shop", Spec: spec, Policy: []byte("x")},
	} {
		if _, _, err := st.Create(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := st.Load("ag_000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing draft: %v", err)
	}
	if _, err := st.Load("../x"); err == nil {
		t.Error("bad id loaded")
	}
}

func TestSeedValidation(t *testing.T) {
	m, _, err := scaffold.MockFromOpenAPI("shop", readTestdata(t, "shop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, seed := range map[string]State{
		"unknown collection": {"widgets": {map[string]any{"id": "W1"}}},
		"no id":              {"orders": {map[string]any{"status": "placed"}}},
		"dup id":             {"orders": {map[string]any{"id": "O1"}, map[string]any{"id": "O1"}}},
		"bad enum":           {"orders": {map[string]any{"id": "O1", "status": "lost"}}},
		"bad type":           {"orders": {map[string]any{"id": "O1", "total": "ten"}}},
		"missing required":   {"tickets": {map[string]any{"id": "T1", "subject": "x"}}},
	} {
		if err := ValidateSeed(seed, m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := State{"orders": {map[string]any{"id": "O1", "status": "placed", "total": 3.5}}, "tickets": {map[string]any{"id": "T1", "subject": "x", "customer_id": "C1"}}}
	if err := ValidateSeed(ok, m); err != nil {
		t.Fatal(err)
	}
}

func TestStrictJSON(t *testing.T) {
	var out struct {
		Rules []Rule `json:"rules"`
	}
	for _, bad := range []string{`hello`, `{"rules": [], "extra": 1}`, `{"rules": []} {"rules": []}`, `[1]`} {
		if decodeStrict(bad, &out) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := decodeStrict("```json\n{\"rules\": []}\n```", &out); err != nil {
		t.Fatal(err)
	}
	if !sameModel("claude-sonnet-4-5-20250929", "claude-sonnet-4-5") || !sameModel("openai/gpt-5", "gpt-5") || sameModel("gpt-5-mini", "gpt-4o") {
		t.Error("sameModel")
	}
}

// TestSimMatchesChecks: the simulated end state is what the checks read.
func TestSimMatchesChecks(t *testing.T) {
	m, _, _ := scaffold.MockFromOpenAPI("shop", readTestdata(t, "shop.yaml"))
	sim := NewSim(m, State{"orders": {map[string]any{"id": "O1", "status": "placed"}}})
	if st, _, _ := sim.Call("updateOrder", map[string]any{"order_id": "O1", "status": "cancelled"}); st != 200 {
		t.Fatalf("flat body %d", st)
	}
	if st, _, _ := sim.Call("getOrder", map[string]any{"order_id": "O2"}); st != 404 {
		t.Fatalf("missing %d", st)
	}
	st, resp, _ := sim.Call("createTicket", map[string]any{"body": map[string]any{"subject": "s", "customer_id": "C1"}})
	if st != 201 || resp.(map[string]any)["id"] != "TIC-1001" {
		t.Fatalf("create %d %v", st, resp)
	}
	spec, err := taskchecks.Parse([]byte("version: 1\nchecks:\n - id: s\n   state: {file: shop/state.json, path: '$.orders[?(@.id == \"O1\")].status', equals: cancelled}\n"))
	if err != nil {
		t.Fatal(err)
	}
	res := taskchecks.Evaluate(spec, taskchecks.Inputs{State: memState{"shop/state.json": stateAny(sim.State)}})
	if _, passed, _, why := taskchecks.Score(spec, res); !passed {
		t.Fatal(why)
	}
}
