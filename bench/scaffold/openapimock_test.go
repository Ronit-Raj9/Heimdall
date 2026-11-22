package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskchecks"
)

const crmOpenAPI = `openapi: 3.0.3
info: {title: Acme CRM, version: "2.1"}
paths:
  /customers:
    get: {operationId: listCustomers, parameters: [{name: email, in: query}]}
    post:
      operationId: createCustomer
      requestBody:
        required: true
        content: {application/json: {schema: {$ref: "#/components/schemas/Customer"}}}
  /customers/{customer_id}:
    get: {operationId: getCustomer}
    patch: {summary: Update a customer, requestBody: {content: {application/json: {schema: {type: object}}}}}
  /customers/{customer_id}/tickets:
    get: {}
    post: {requestBody: {content: {application/json: {schema: {type: object}}}}}
  /tickets/{id}/escalate:
    post: {operationId: escalateTicket}
components:
  schemas:
    Customer: {type: object, properties: {name: {type: string}, email: {type: string}}}
`

func TestMockFromOpenAPI(t *testing.T) {
	m, warns, err := MockFromOpenAPI("crm", []byte(crmOpenAPI))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]MockOperation{}
	for _, op := range m.Operations {
		got[op.Name] = op
	}
	want := map[string]string{"listCustomers": MockList, "createCustomer": MockCreate, "getCustomer": MockGet,
		"update_customer": MockUpdate, "list_tickets": MockList, "create_ticket": MockCreate, "escalateTicket": MockAction}
	for name, kind := range want {
		if op, ok := got[name]; !ok || op.Kind != kind {
			t.Errorf("%s: %+v, want kind %s (all: %v)", name, op, kind, keys(got))
		}
	}
	if p := got["list_tickets"].Parent; p == nil || p.Param != "customer_id" || p.Field != "customer_id" {
		t.Errorf("nested parent %+v", p)
	}
	if s := got["createCustomer"].BodySchema; s == nil || s["properties"] == nil || !got["createCustomer"].BodyRequired {
		t.Errorf("body schema %v", s)
	}
	if len(warns) != 1 || strings.Join(m.Collections(), ",") != "customers,tickets" {
		t.Errorf("warnings %v collections %v", warns, m.Collections())
	}
	for _, bad := range []string{"swagger: '2.0'\npaths: {/a: {get: {}}}", "openapi: 3.0.0\npaths: {}", "not: [yaml"} {
		if _, _, err := MockFromOpenAPI("x", []byte(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFixturesFromLedger(t *testing.T) {
	m, _, _ := MockFromOpenAPI("crm", []byte(crmOpenAPI))
	rows := []schemas.ToolCall{
		{Server: "crm", Tool: "getCustomer", Arguments: `{"customer_id":"C-1"}`, Result: `{"id":"C-1","name":"Mia"}`},
		{Server: "crm", Tool: "escalateTicket", Arguments: `{"id":"T-9"}`, Result: `nope`, IsError: true},
		{Server: "other", Tool: "getCustomer", Arguments: `{}`},
		{Server: "crm", Tool: "POST /customers", Arguments: `{"name":"Leo"}`, Result: `{"id":"C-2"}`, Status: 201, Protocol: "http"},
	}
	fx := FixturesFromLedger(m, "crm", rows)
	if len(fx) != 3 || fx[0].Path != "/customers/C-1" || fx[1].Status != 400 || fx[1].Response != "nope" || fx[2].Method != "POST" || fx[2].Status != 201 {
		t.Fatalf("fixtures %+v", fx)
	}
}

// TestMockServerStateful runs the generated mock: REST and MCP share one
// state, the state and the call record are exported, fixtures answer first,
// and a checks.yaml state check reads the export.
func TestMockServerStateful(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	m, _, err := MockFromOpenAPI("crm", []byte(crmOpenAPI))
	if err != nil {
		t.Fatal(err)
	}
	m.Fixtures = []MockFixture{{Method: "POST", Path: "/tickets/{id}/escalate", Body: map[string]any{"reason": "legal"}, Status: 202, Response: map[string]any{"queued": true}}}
	dir := t.TempDir()
	files, err := MockServiceFiles(m, []byte(crmOpenAPI), "openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Path), f.Data, f.Mode); err != nil {
			t.Fatal(err)
		}
	}
	seed := `{"customers": [{"id": "C-1", "name": "Mia", "email": "mia@example.com"}], "tickets": []}`
	if err := os.WriteFile(filepath.Join(dir, "seed.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	port := scenarioPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "python3", "-I", filepath.Join(dir, "server.py"))
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), "MOCK_PATH="+filepath.Join(dir, "mock.json"),
		"SEED_PATH="+filepath.Join(dir, "seed.json"), "STATE_PATH="+filepath.Join(state, "crm", "state.json"), "RECORD_PATH="+filepath.Join(state, "crm", "calls.jsonl"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	waitUp(t, port)
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	do := func(method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := do("GET", "/customers?email=mia@example.com", ""); code != 200 || !strings.Contains(body, "C-1") {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, body := do("POST", "/customers/C-1/tickets", `{"subject":"broken"}`); code != 201 || !strings.Contains(body, `"customer_id": "C-1"`) {
		t.Fatalf("nested create: %d %s", code, body)
	}
	if code, _ := do("GET", "/customers/C-404", ""); code != 404 {
		t.Fatalf("missing item: %d", code)
	}
	if code, body := do("POST", "/tickets/TIC-1001/escalate", `{"reason":"legal"}`); code != 202 || !strings.Contains(body, "queued") {
		t.Fatalf("fixture: %d %s", code, body)
	}
	// MCP: the same state through tools.
	rpc := func(method string, params any) map[string]any {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		resp, err := http.Post(base+"/mcp", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	tools := rpc("tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(m.Operations) {
		t.Fatalf("tools %d", len(tools))
	}
	res := rpc("tools/call", map[string]any{"name": "update_customer", "arguments": map[string]any{"customer_id": "C-1", "body": map[string]any{"tier": "gold"}}})
	if r := res["result"].(map[string]any); r["isError"] != false {
		t.Fatalf("update: %v", res)
	}
	spec, err := taskchecks.Parse([]byte(`checks:
  - id: gold
    state: {file: crm/state.json, path: "$.customers[?(@.id=='C-1')].tier", equals: gold}
  - id: ticket
    state: {file: crm/state.json, path: "$.tickets[?(@.customer_id=='C-1')]", length: 1}
`))
	if err != nil {
		t.Fatal(err)
	}
	results := taskchecks.Evaluate(spec, taskchecks.Inputs{State: taskchecks.NewDirState(state)})
	if _, passed, _, reason := taskchecks.Score(spec, results); !passed {
		t.Fatalf("state checks: %s", reason)
	}
	rec, _ := os.ReadFile(filepath.Join(state, "crm", "calls.jsonl"))
	if n := strings.Count(string(rec), "\n"); n != 5 || !strings.Contains(string(rec), `"source": "fixture"`) {
		t.Fatalf("record %d lines:\n%s", n, rec)
	}
}
