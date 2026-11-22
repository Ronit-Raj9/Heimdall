package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenarioRowsImport(t *testing.T) {
	c := newBuildClient(t)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"desk","template":"support"}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	rows := `{"id":"dup-charge","opening_message":"I was charged twice for B-3001, email mia.torres@example.com","persona_goal":"Get a refund","expected_actions":[{"tool":"issue_refund","args":{"order_id":"B-3001","amount_pence":4500,"reason":"damaged"}}],"forbidden_actions":["apply_credit"]}
{"opening_message":"Be nice","persona_goal":"chat","reply_rubric":"Polite"}
`
	req := ScenarioRowsRequest{Format: "jsonl", Data: rows, Source: "tickets.jsonl"}
	code, body := c.do(t, "POST", "/suites/desk/scenario-rows", jsonBody(t, req), nil)
	if code != 409 || !strings.Contains(string(body), "switch_grader") {
		t.Fatalf("other grader: %d %s", code, body)
	}
	req.SwitchGrader, req.DryRun = true, true
	code, body = c.do(t, "POST", "/suites/desk/scenario-rows", jsonBody(t, req), nil)
	var res ScenarioRowsResult
	if code != 200 || json.Unmarshal(body, &res) != nil || len(res.Tasks) != 1 || len(res.Problems) != 1 || res.Shape != "refund-in-policy" {
		t.Fatalf("dry run: %d %s", code, body)
	}
	if raw, _ := os.ReadFile(filepath.Join(c.root, "benchmarks", "desk", "suite.yaml")); strings.Contains(string(raw), "grader: checks") {
		t.Fatal("dry run switched the grader")
	}
	req.DryRun = false
	code, body = c.do(t, "POST", "/suites/desk/scenario-rows", jsonBody(t, req), nil)
	res = ScenarioRowsResult{}
	if code != 201 || json.Unmarshal(body, &res) != nil || !res.GraderChanged || !strings.Contains(res.Next, "earlier tasks") {
		t.Fatalf("import: %d %s", code, body)
	}
	if errs := lintErrors(t, c, "desk"); len(errs) > 0 {
		t.Fatalf("imported tasks do not lint clean:\n%s", strings.Join(errs, "\n"))
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "desk", "tasks", "dup-charge", "verifier", "checks.yaml")); err != nil {
		t.Fatal(err)
	}
	// A judge pinned with the switch admits the rubric row.
	req.Data = strings.SplitN(rows, "\n", 2)[1]
	code, body = c.do(t, "POST", "/suites/desk/scenario-rows", []byte(`{"format":"jsonl","data":`+jsonString(req.Data)+`,"switch_grader":true,"judge":{"provider":"openai","model":"gpt-x"}}`), nil)
	if code != 201 || !strings.Contains(string(body), `"be-nice"`) {
		t.Fatalf("judged import: %d %s", code, body)
	}
	if errs := lintErrors(t, c, "desk"); len(errs) > 0 {
		t.Fatalf("judged suite does not lint clean:\n%s", strings.Join(errs, "\n"))
	}
	// A suite without a simulated customer has no shape to copy.
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"faq","template":"qa"}`), nil); code != 201 {
		t.Fatalf("create qa: %d %s", code, body)
	}
	if code, body := c.do(t, "POST", "/suites/faq/scenario-rows", []byte(`{"format":"jsonl","data":"{}","switch_grader":true}`), nil); code != 422 || !strings.Contains(string(body), "support benchmark") {
		t.Fatalf("qa suite: %d %s", code, body)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
