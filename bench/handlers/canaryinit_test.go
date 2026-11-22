package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCanaryCreateListsEveryProblem: an empty drift monitor form gets one 422
// that names every missing field at once in plain words, each under its
// field, with the joined text in error.message for older clients; the edit
// route answers the same way.
func TestCanaryCreateListsEveryProblem(t *testing.T) {
	h, c, _ := fw17Canary(t)
	check := func(label string, status int, body []byte, want map[string]string) {
		t.Helper()
		if status != 422 {
			t.Fatalf("%s: status %d %s", label, status, body)
		}
		var got struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
			Problems []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"problems"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("%s: %v %s", label, err, body)
		}
		byField := map[string]string{}
		for _, p := range got.Problems {
			byField[p.Field] = p.Message
		}
		for f, msg := range want {
			if byField[f] != msg {
				t.Errorf("%s: field %s = %q, want %q (all: %s)", label, f, byField[f], msg, body)
			}
			if !strings.Contains(got.Error.Message, msg) {
				t.Errorf("%s: error.message %q misses %q", label, got.Error.Message, msg)
			}
		}
		if got.Error.Type == "" {
			t.Errorf("%s: no error type", label)
		}
		if strings.Contains(got.Error.Message, "per_run_usd") || strings.Contains(got.Error.Message, "tasks is empty") {
			t.Errorf("%s: YAML words in %q", label, got.Error.Message)
		}
	}
	empty := `{"name":"","agent":"","suite":"","tasks":[],"trials":1,"targets":[],"budget":{"per_run_usd":0,"per_day_usd":0}}`
	all := map[string]string{
		"name":    "Use lowercase letters, digits, - or _ for the name, at most 48 characters.",
		"agent":   "Pick an agent.",
		"suite":   "Pick a benchmark.",
		"tasks":   "Pick the tasks to check, or choose all tasks.",
		"targets": "Add at least one model to watch.",
		"budget":  "Set both spending limits, or tick Practice mode.",
	}
	ctx := schedCall(h.create, empty, "")
	check("create", ctx.Response.StatusCode(), ctx.Response.Body(), all)

	// The project checks join the same list: an unknown agent and benchmark
	// next to a form problem.
	mixed := strings.NewReplacer(`"agent":""`, `"agent":"agents/missing.yaml"`, `"suite":""`, `"suite":"not-a-benchmark"`, `"name":""`, `"name":"ok"`).Replace(empty)
	ctx = schedCall(h.create, mixed, "")
	check("create mixed", ctx.Response.StatusCode(), ctx.Response.Body(), map[string]string{
		"agent":   "Agent agents/missing.yaml is not in this project. Create it on the Agents page first.",
		"suite":   "Benchmark not-a-benchmark does not exist. Pick one from the Benchmarks page.",
		"targets": "Add at least one model to watch.",
	})

	if ctx := schedCall(h.create, fw17CanaryBody, ""); ctx.Response.StatusCode() != 201 {
		t.Fatalf("create: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx = schedCall(c.edit, empty, "nightly")
	delete(all, "name") // the name comes from the path
	check("edit", ctx.Response.StatusCode(), ctx.Response.Body(), all)
}
