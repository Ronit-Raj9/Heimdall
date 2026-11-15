package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceexport"
)

// otlpFixture is testIndex with r_t01_A carrying the run files an export
// reads, a tool call whose arguments hold a secret, and r_t02_A unsealed.
func otlpFixture(t *testing.T, verifyErr error) *artifactIndex {
	t.Helper()
	idx := testIndex()
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t02_A" {
			idx.runs[i].Sealed, idx.runs[i].SealHash = false, ""
		}
	}
	run := `{"run_id":"r_t01_A","slot_id":"s_t01_A","exp_id":"` + testExp + `","outcome":"fail",` +
		`"timing":{"started":"2026-11-15T10:00:00Z","sealed":"2026-11-15T10:00:09Z"},"agent":{"config_id":"codex","config_hash":"h"}}`
	ev := `{"requests":{"complete":true,"requests":[{"id":"q1","timestamp":"2026-11-15T10:00:01Z","status":"success","kind":"message",` +
		`"provider":"openai","model_requested":"m","model_served":"m","latency_ms":20,"tokens":{"fresh":5,"cache_read":0,"cache_write":0,"out":2}}]},` +
		`"tool_calls":[{"seq":1,"server":"fs","tool":"read","protocol":"mcp","arguments":"{\"api_key\":\"hunter2-handler\"}","args_sha256":"x",` +
		`"result_sha256":"y","is_error":false,"started":"2026-11-15T10:00:02Z","latency_ms":3}]}`
	for _, id := range []string{"r_t01_A", "r_t02_A"} {
		idx.files[id]["run.json"] = []byte(strings.ReplaceAll(run, "r_t01_A", id))
		idx.files[id][FileEvidence] = []byte(ev)
		idx.files[id][FileSeal] = []byte(`{"run_id":"` + id + `"}`)
	}
	return &artifactIndex{fakeIndex: idx, verifyErr: verifyErr}
}

func TestRunOTLPDownload(t *testing.T) {
	for _, payloads := range []bool{false, true} {
		d := fullDeps()
		d.index = otlpFixture(t, nil)
		c := newTestClient(t, d)
		path := APIPrefix + "/runs/r_t01_A/otlp"
		if payloads {
			path += "?payloads=true"
		}
		resp := c.do(t, "GET", path, nil, nil)
		if resp.StatusCode() != 200 {
			t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
		}
		for k, v := range map[string]string{
			"Content-Type": "application/json", "Content-Disposition": `attachment; filename="r_t01_A.otlp.json"`,
			"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
		} {
			if got := string(resp.Header.Peek(k)); got != v {
				t.Errorf("header %s = %q, want %q", k, got, v)
			}
		}
		var td traceexport.TracesData
		if err := json.Unmarshal(resp.Body(), &td); err != nil {
			t.Fatal(err)
		}
		if err := traceexport.Validate(&td); err != nil {
			t.Fatal(err)
		}
		spans := td.ResourceSpans[0].ScopeSpans[0].Spans
		if spans[0].TraceID != traceexport.TraceID("r_t01_A") {
			t.Fatal("trace id is not the run's")
		}
		v, _ := traceexport.Attr(spans[0].Attributes, "heimdall.task")
		if v.String() != "t01" {
			t.Fatalf("slot not applied: task %q", v.String())
		}
		body := string(resp.Body())
		if strings.Contains(body, "hunter2-handler") {
			t.Fatal("a secret reached the trace")
		}
		if strings.Contains(body, "gen_ai.tool.call.arguments") != payloads {
			t.Fatalf("arguments exported: %v, payloads: %v", !payloads, payloads)
		}
	}
}

func TestRunOTLPRefuses(t *testing.T) {
	tests := []struct {
		name, run string
		verifyErr error
		noVerify  bool
		status    int
		msg       string
	}{
		{name: "unknown run", run: "r_none", status: 404},
		{name: "unsealed", run: "r_t02_A", status: 409, msg: "not sealed"},
		{name: "tampered", run: "r_t01_A", verifyErr: fmt.Errorf("evidence.json changed: %w", schemas.ErrSealMismatch), status: 409, msg: "does not match its seal"},
		{name: "verify failed", run: "r_t01_A", verifyErr: fmt.Errorf("disk"), status: 500},
		{name: "no verifier", run: "r_t01_A", noVerify: true, status: 503},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := fullDeps()
			idx := otlpFixture(t, tc.verifyErr)
			d.index = idx
			if tc.noVerify {
				d.index = idx.fakeIndex
			}
			c := newTestClient(t, d)
			resp := c.do(t, "GET", APIPrefix+"/runs/"+tc.run+"/otlp", nil, nil)
			if resp.StatusCode() != tc.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tc.status, resp.Body())
			}
			if tc.msg != "" && !strings.Contains(string(resp.Body()), tc.msg) {
				t.Fatalf("body %s lacks %q", resp.Body(), tc.msg)
			}
			if strings.Contains(string(resp.Header.Peek("Content-Disposition")), "attachment") {
				t.Fatal("a refused run was offered as a download")
			}
		})
	}
}
