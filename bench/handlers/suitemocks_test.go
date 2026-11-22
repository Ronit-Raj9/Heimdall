package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mockOpenAPI = `openapi: 3.0.3
info: {title: Orders, version: "1"}
paths:
  /orders:
    get: {operationId: listOrders}
  /orders/{order_id}:
    get: {operationId: getOrder}
  /orders/{order_id}/refund:
    post: {operationId: refundOrder, requestBody: {content: {application/json: {schema: {type: object}}}}}
`

func TestSuiteMockFromOpenAPI(t *testing.T) {
	c := newBuildClient(t)
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"desk","template":"support"}`), nil); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	req := SuiteMockRequest{ID: "orders", Spec: mockOpenAPI, FileName: "orders.yaml", DryRun: true}
	code, body := c.do(t, "POST", "/suites/desk/mocks", jsonBody(t, req), nil)
	var res SuiteMockResult
	if code != 200 || json.Unmarshal(body, &res) != nil || len(res.Operations) != 3 || !strings.Contains(res.ServiceYAML, "id: orders") {
		t.Fatalf("dry run: %d %s", code, body)
	}
	svc := filepath.Join(c.root, "benchmarks", "desk", "services", "orders")
	if _, err := os.Stat(svc); err == nil {
		t.Fatal("dry run wrote the service")
	}
	req.DryRun = false
	if code, body = c.do(t, "POST", "/suites/desk/mocks", jsonBody(t, req), nil); code != 201 {
		t.Fatalf("write: %d %s", code, body)
	}
	for _, f := range []string{"server.py", "Dockerfile", "mock.json", "openapi.yaml"} {
		if _, err := os.Stat(filepath.Join(svc, f)); err != nil {
			t.Fatal(err)
		}
	}
	if code, body = c.do(t, "POST", "/suites/desk/mocks", jsonBody(t, req), nil); code != 409 || !strings.Contains(string(body), "replace") {
		t.Fatalf("existing: %d %s", code, body)
	}
	req.Replace = true
	req.Spec = strings.Replace(mockOpenAPI, "  /orders/{order_id}/refund:\n    post: {operationId: refundOrder, requestBody: {content: {application/json: {schema: {type: object}}}}}\n", "", 1)
	res = SuiteMockResult{}
	if code, body = c.do(t, "POST", "/suites/desk/mocks", jsonBody(t, req), nil); code != 201 || json.Unmarshal(body, &res) != nil || !res.Replaced || len(res.Operations) != 2 {
		t.Fatalf("replace: %d %s", code, body)
	}
	ents, _ := os.ReadDir(filepath.Dir(svc))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "orders.") || strings.HasPrefix(e.Name(), ".new-") {
			t.Fatalf("leftover folder %s", e.Name())
		}
	}
	for _, bad := range []string{`{"id":"Orders!","spec":"x"}`, `{"id":"orders","spec":"not: openapi"}`} {
		if code, body := c.do(t, "POST", "/suites/desk/mocks", []byte(bad), nil); code != 422 {
			t.Fatalf("%s: %d %s", bad, code, body)
		}
	}
	if code, _ := c.do(t, "POST", "/suites/desk/mocks", []byte(`{"id":"orders","spec":" "}`), nil); code != 400 {
		t.Fatalf("empty spec: %d", code)
	}
}
