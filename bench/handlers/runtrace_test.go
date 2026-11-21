package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunTraceFormats(t *testing.T) {
	d := fullDeps()
	d.index = stepsFixture(t)
	c := newTestClient(t, d)

	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/trace", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("json status = %d: %s", resp.StatusCode(), resp.Body())
	}
	if cd := string(resp.Header.Peek("Content-Disposition")); !strings.Contains(cd, `r_t01_A.trace.json`) {
		t.Fatalf("json disposition %q", cd)
	}
	var body RunTrace
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RunID != "r_t01_A" || len(body.Steps.Steps) != 3 || body.Events == nil {
		t.Fatalf("json body = %+v", body)
	}

	resp = c.do(t, "GET", APIPrefix+"/runs/r_t01_A/trace?format=jsonl", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("jsonl status = %d: %s", resp.StatusCode(), resp.Body())
	}
	if ct := string(resp.Header.Peek("Content-Type")); !strings.HasPrefix(ct, "application/x-ndjson") {
		t.Fatalf("jsonl content type %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(string(resp.Body())), "\n")
	if len(lines) != 3 {
		t.Fatalf("jsonl lines = %d: %s", len(lines), resp.Body())
	}
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("jsonl line %q: %v", line, err)
		}
	}

	resp = c.do(t, "GET", APIPrefix+"/runs/r_t01_A/trace?format=xml", nil, nil)
	if resp.StatusCode() != 400 {
		t.Fatalf("bad format status = %d", resp.StatusCode())
	}
	resp = c.do(t, "GET", APIPrefix+"/runs/nope/trace", nil, nil)
	if resp.StatusCode() != 404 {
		t.Fatalf("missing run status = %d", resp.StatusCode())
	}
}
