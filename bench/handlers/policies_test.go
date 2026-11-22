package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

const testPolicyYAML = `policy_version: 1
name: tiny
checks:
  - id: clean-no-regression
    metric: verified_success
    condition: clean
    rule: no_regression
`

func newPoliciesHandler(t *testing.T) (policiesHandler, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return policiesHandler{mode: schemas.ModeLive, store: st}, root
}

func callPolicy(h fasthttp.RequestHandler, method, name string, body any) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	if name != "" {
		ctx.SetUserValue("name", name)
	}
	if body != nil {
		b, _ := json.Marshal(body)
		ctx.Request.SetBody(b)
	}
	h(ctx)
	return ctx
}

func TestPoliciesSaveListGet(t *testing.T) {
	h, root := newPoliciesHandler(t)

	ctx := callPolicy(h.put, "PUT", "tiny.yaml", PolicySaveRequest{YAML: testPolicyYAML})
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("put yaml: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if b, err := os.ReadFile(filepath.Join(root, "policies", "tiny.yaml")); err != nil || string(b) != testPolicyYAML {
		t.Fatalf("file on disk: %q %v", b, err)
	}

	// The form path: a policy object rendered to YAML that parses again.
	max := 0.05
	form := &schemas.Policy{Name: "from-form", Checks: []schemas.PolicyCheck{{ID: "fc", Metric: "false_completion", Condition: "all", Max: &max, MinN: 10}}}
	ctx = callPolicy(h.put, "PUT", "form.yaml", PolicySaveRequest{Policy: form})
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("put form: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var doc PolicyDocument
	if err := json.Unmarshal(ctx.Response.Body(), &doc); err != nil || !doc.Valid || doc.Policy.Name != "from-form" || strings.Contains(doc.YAML, "on_unknown") {
		t.Fatalf("form doc: %+v %v", doc, err)
	}

	ctx = callPolicy(h.list, "GET", "", nil)
	var list PolicyList
	if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil || len(list.Policies) != 2 || !list.Policies[1].Valid || list.Policies[1].Checks != 1 {
		t.Fatalf("list: %s %v", ctx.Response.Body(), err)
	}

	// A stale if_match is refused rather than overwriting.
	ctx = callPolicy(h.put, "PUT", "tiny.yaml", PolicySaveRequest{YAML: testPolicyYAML, IfMatch: strings.Repeat("0", 64)})
	if ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("stale if_match: %d", ctx.Response.StatusCode())
	}

	ctx = callPolicy(h.del, "DELETE", "form.yaml", nil)
	if ctx.Response.StatusCode() != fasthttp.StatusNoContent {
		t.Fatalf("delete: %d", ctx.Response.StatusCode())
	}
	if ctx = callPolicy(h.get, "GET", "form.yaml", nil); ctx.Response.StatusCode() != 404 {
		t.Fatalf("get deleted: %d", ctx.Response.StatusCode())
	}
}

func TestPoliciesRejects(t *testing.T) {
	h, _ := newPoliciesHandler(t)
	for _, name := range []string{"../x.yaml", "a/b.yaml", "X.yaml", "x.txt", ".hidden.yaml"} {
		if ctx := callPolicy(h.get, "GET", name, nil); ctx.Response.StatusCode() != 400 {
			t.Errorf("name %q: %d", name, ctx.Response.StatusCode())
		}
	}
	ctx := callPolicy(h.put, "PUT", "bad.yaml", PolicySaveRequest{YAML: "policy_version: 1\nname: bad\nchecks: []\nbogus: 1\n"})
	if ctx.Response.StatusCode() != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("invalid policy saved: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = callPolicy(h.validate, "POST", "", PolicySaveRequest{YAML: testPolicyYAML})
	var v PolicyValidation
	if err := json.Unmarshal(ctx.Response.Body(), &v); err != nil || !v.Valid {
		t.Fatalf("validate: %s", ctx.Response.Body())
	}
	none := policiesHandler{mode: schemas.ModeLive}
	if ctx := callPolicy(none.list, "GET", "", nil); ctx.Response.StatusCode() != fasthttp.StatusServiceUnavailable {
		t.Fatalf("nil store: %d", ctx.Response.StatusCode())
	}
}

// Saving a new rule set never overwrites an existing file: create_only
// answers 409 and leaves the file as it was (N6).
func TestPutPolicyCreateOnlyRefusesExisting(t *testing.T) {
	h, root := newPoliciesHandler(t)
	path := filepath.Join(root, "policies", "my-release-policy.yaml")
	if err := os.WriteFile(path, []byte(testPolicyYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	other := strings.Replace(testPolicyYAML, "name: tiny", "name: other", 1)
	ctx := callPolicy(h.put, "PUT", "my-release-policy.yaml", PolicySaveRequest{YAML: other, CreateOnly: true})
	if ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("create_only on an existing file: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if !strings.Contains(string(ctx.Response.Body()), "A rule set named my-release-policy already exists. Pick another file name.") {
		t.Fatalf("body %s", ctx.Response.Body())
	}
	if b, _ := os.ReadFile(path); string(b) != testPolicyYAML {
		t.Fatalf("file changed: %q", b)
	}
	ctx = callPolicy(h.put, "PUT", "my-release-policy-2.yaml", PolicySaveRequest{YAML: other, CreateOnly: true})
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("create_only on a new file: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}
