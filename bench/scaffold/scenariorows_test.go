package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

const scenarioJSONL = `{"id": "dup-charge", "opening_message": "Hi, I was charged twice for order B-3001. My email is mia.torres@example.com.", "persona_goal": "Get the grinder refunded; it arrived cracked.", "facts": ["Your email is mia.torres@example.com", "Order B-3001"], "expected_actions": [{"tool": "find_customer", "args": {"email": "mia.torres@example.com"}}, {"tool": "issue_refund", "args": {"order_id": "B-3001", "amount_pence": 4500, "reason": "arrived damaged"}}], "forbidden_actions": [{"tool": "apply_credit", "args": {"customer_id": "U-101", "amount_pence": 500, "reason": "goodwill"}}], "ordered": true, "reference_reply": "I have refunded 45.00 for order B-3001.", "category": "Billing", "escalation": "not_expected"}
{"opening_message": "Where is my order?", "persona_goal": "Find out about B-9001", "account_seed": {"orders": [{"id": "B-9001", "customer_id": "U-101", "status": "processing", "items": [], "total_pence": 100, "refunded_pence": 0, "placed_at": "2026-11-09", "delivered_at": null, "shipping_address": null}]}, "expected_actions": ["get_order"]}
{"opening_message": "hello"}
{"opening_message": "x", "persona_goal": "y", "reply_rubric": "Be kind"}
`

func supportShape(t *testing.T) *ScenarioShape {
	t.Helper()
	files, err := embeddedSample(TemplateSupport, "desk")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := ScenarioShapeFrom("refund-in-policy", files)
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

func TestScenarioRowsImport(t *testing.T) {
	rows, problems, err := ParseScenarioRows(RowFormatJSONL, []byte(scenarioJSONL))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || len(problems) != 1 || !strings.Contains(problems[0].Message, "persona_goal") {
		t.Fatalf("rows %d problems %+v", len(rows), problems)
	}
	sh := supportShape(t)
	if sh.Desk != "helpdesk" || sh.Customer != "customer" || sh.SeedRel != "seed/db.json" || sh.UserRel != "user.json" {
		t.Fatalf("shape %+v", sh)
	}
	tasks, more := ImportScenarioRows(rows, ScenarioOptions{Shape: sh, Source: "tickets.jsonl", Taken: func(id string) bool { return id == "refund-in-policy" }})
	if len(tasks) != 2 || len(more) != 1 || !strings.Contains(more[0].Message, "judge") {
		t.Fatalf("tasks %d problems %+v", len(tasks), more)
	}
	byPath := map[string][]byte{}
	for _, f := range tasks[0].Files {
		byPath[f.Path] = f.Data
	}
	spec, err := taskchecks.Parse(byPath["verifier/checks.yaml"])
	if err != nil {
		t.Fatalf("checks.yaml: %v\n%s", err, byPath["verifier/checks.yaml"])
	}
	if len(spec.Checks) != 4 || spec.Checks[3].Kind() != taskchecks.KindCallOrder || spec.Checks[0].ToolCalled.Server != "helpdesk" {
		t.Fatalf("checks %+v", spec.Checks)
	}
	var task schemas.Task
	dec := yaml.NewDecoder(bytes.NewReader(byPath["task.yaml"]))
	dec.KnownFields(true)
	if err := dec.Decode(&task); err != nil {
		t.Fatalf("task.yaml: %v\n%s", err, byPath["task.yaml"])
	}
	if task.ID != "dup-charge" || task.Escalation != "not_expected" || len(task.Controls.Mutants) != 2 || len(task.Services) != 2 || task.Tags[0] != "billing" {
		t.Fatalf("task %+v", task)
	}
	var user map[string]any
	if err := json.Unmarshal(byPath["user.json"], &user); err != nil || user["persona"].(map[string]any)["goal"] == "" {
		t.Fatalf("user.json %s", byPath["user.json"])
	}
	// The second row's account is merged into the seed by id.
	var seed map[string]any
	for _, f := range tasks[1].Files {
		if f.Path == "seed/db.json" {
			_ = json.Unmarshal(f.Data, &seed)
		}
	}
	orders := seed["orders"].([]any)
	if last := orders[len(orders)-1].(map[string]any); last["id"] != "B-9001" || len(orders) != 7 {
		t.Fatalf("merged orders %v", orders)
	}
	if tasks[1].ID != "where-is-my-order" {
		t.Fatalf("slug id %s", tasks[1].ID)
	}
}

func TestMergeSeed(t *testing.T) {
	base := map[string]any{"a": []any{map[string]any{"id": "1", "x": 1.0}}, "b": 1.0, "c": map[string]any{"d": 1.0}}
	add := map[string]any{"a": []any{map[string]any{"id": "1", "y": 2.0}, map[string]any{"id": "2"}}, "b": 2.0, "c": map[string]any{"e": 3.0}}
	got := MergeSeed(base, add).(map[string]any)
	a := got["a"].([]any)
	if len(a) != 2 || a[0].(map[string]any)["x"] != 1.0 || a[0].(map[string]any)["y"] != 2.0 || got["b"] != 2.0 || len(got["c"].(map[string]any)) != 2 {
		t.Fatalf("merge %v", got)
	}
}

// ledgerProxy forwards MCP calls to a service and records tools/call rows
// as the per-run tool proxy does.
type ledgerProxy struct {
	mu   sync.Mutex
	rows []schemas.ToolCall
}

func (l *ledgerProxy) handler(server, target string) http.Handler {
	u, _ := url.Parse(target)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			Method string `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		req, _ := http.NewRequest(r.Method, u.String(), bytes.NewReader(body))
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		if msg.Method == "tools/call" {
			l.mu.Lock()
			l.rows = append(l.rows, schemas.ToolCall{Seq: len(l.rows) + 1, Server: server, Tool: msg.Params.Name, Arguments: string(msg.Params.Arguments),
				Protocol: "mcp", IsError: bytes.Contains(out, []byte(`"isError":true`)) || bytes.Contains(out, []byte(`"isError": true`))})
			l.mu.Unlock()
		}
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(out)
	})
}

func scenarioPort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitUp(t *testing.T, port int) {
	for i := 0; i < 100; i++ {
		if r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port)); err == nil {
			r.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("service on %d never came up", port)
}

// TestScenarioControlsEndToEnd runs the generated reference, mutants and
// empty control against the real helpdesk server and user simulator and
// grades them with the checks the row wrote: the reference passes every
// check and every other control fails.
func TestScenarioControlsEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	rows, _, err := ParseScenarioRows(RowFormatJSONL, []byte(strings.SplitN(scenarioJSONL, "\n", 2)[0]))
	if err != nil {
		t.Fatal(err)
	}
	tasks, problems := ImportScenarioRows(rows, ScenarioOptions{Shape: supportShape(t)})
	if len(tasks) != 1 {
		t.Fatalf("problems %+v", problems)
	}
	dir := t.TempDir()
	for _, f := range tasks[0].Files {
		p := filepath.Join(dir, filepath.FromSlash(f.Path))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, f.Data, f.Mode); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := filepath.Abs(".")
	helpdeskPy := filepath.Join(root, "templates", "suites", "support", "services", "helpdesk", "server.py")
	simuserPy := filepath.Join(root, "..", "simuser", "simuser.py")
	spec, err := taskchecks.Parse(mustRead(t, filepath.Join(dir, "verifier", "checks.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	run := func(control string) (bool, string) {
		state := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		dp, cp := scenarioPort(t), scenarioPort(t)
		desk := exec.CommandContext(ctx, "python3", "-I", helpdeskPy)
		desk.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", dp), "SEED_PATH="+filepath.Join(dir, "seed", "db.json"), "STATE_PATH="+filepath.Join(state, "helpdesk", "db.json"))
		cust := exec.CommandContext(ctx, "python3", "-I", simuserPy, "serve", "--config", filepath.Join(dir, "user.json"), "--state", filepath.Join(state, "customer"), "--port", fmt.Sprint(cp))
		for _, c := range []*exec.Cmd{desk, cust} {
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			defer func(c *exec.Cmd) { cancel(); _ = c.Wait() }(c)
		}
		waitUp(t, dp)
		waitUp(t, cp)
		lp := &ledgerProxy{}
		ds := httptest.NewServer(lp.handler("helpdesk", fmt.Sprintf("http://127.0.0.1:%d/mcp", dp)))
		defer ds.Close()
		cs := httptest.NewServer(lp.handler("customer", fmt.Sprintf("http://127.0.0.1:%d/mcp", cp)))
		defer cs.Close()
		servers, _ := json.Marshal(map[string]any{"helpdesk": map[string]string{"url": ds.URL}, "customer": map[string]string{"url": cs.URL}})
		cmd := exec.Command("sh", filepath.Join(dir, control))
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "BENCH_MCP_SERVERS="+string(servers))
		out, err := cmd.CombinedOutput()
		if err != nil && control == "solution/reference.sh" {
			t.Fatalf("reference failed: %v\n%s", err, out)
		}
		res := taskchecks.Evaluate(spec, taskchecks.Inputs{Calls: taskchecks.CallsFrom(lp.rows), State: taskchecks.NewDirState(state)})
		_, passed, invalid, reason := taskchecks.Score(spec, res)
		if invalid {
			t.Fatalf("%s: invalid: %s", control, reason)
		}
		return passed, reason
	}
	if ok, why := run("solution/reference.sh"); !ok {
		t.Fatalf("reference fails: %s", why)
	}
	for _, c := range []string{"controls/empty.sh", "controls/mutant-skips-issue-refund.sh", "controls/mutant-also-apply-credit.sh"} {
		if ok, why := run(c); ok {
			t.Errorf("%s passes: %s", c, why)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
