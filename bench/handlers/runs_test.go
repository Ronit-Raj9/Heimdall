package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// sealIndex is a fakeIndex that verifies seals: the listed runs fail.
type sealIndex struct {
	*fakeIndex
	bad map[string]error
}

func (s sealIndex) VerifySeal(_ context.Context, runID string) error {
	return s.bad[runID]
}

func TestRunFileAllowlist(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_A"][FileRun] = []byte(`{"run_id":"r_t01_A"}`)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	tests := []struct {
		name   string
		status int
	}{
		{"run.json", 200},
		{"workspace.diff", 200},
		{"agent.stream.jsonl", 404},     // content log, never served
		{"gateway.requests.jsonl", 404}, // content log, never served
		{"evidence.json", 404},          // read internally only
		{"..%2Fmanifest.json", 404},
		{"..", 301},        // the router cleans dot segments with a redirect; the index is never asked
		{"seal.json", 404}, // allowlisted but absent
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := c.do(t, "GET", "/api/bench/runs/r_t01_A/files/"+tt.name, nil, nil)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode(), tt.status)
			}
			if ct := string(resp.Header.ContentType()); strings.Contains(ct, "html") {
				t.Errorf("served as %q", ct)
			}
		})
	}
	for _, name := range []string{"agent.stream.jsonl", "gateway.requests.jsonl", ".."} {
		if slices.Contains(idx.asked, name) {
			t.Errorf("index was asked for %q", name)
		}
	}
}

func TestRunEvidenceDownload(t *testing.T) {
	idx := testIndex()
	for _, name := range []string{"gateway.raw.jsonl", "tools.jsonl", "agent.stream.jsonl", "agent.stderr.txt", "agent.stream.2.jsonl", "agent.stderr.2.txt"} {
		idx.files["r_t01_A"][name] = []byte("raw-" + name)
	}
	d := fullDeps()
	d.index = sealIndex{fakeIndex: idx, bad: map[string]error{}}
	c := newTestClient(t, d)
	for _, name := range []string{"gateway.raw.jsonl", "tools.jsonl", "agent.stream.jsonl", "agent.stderr.txt", "agent.stream.2.jsonl", "agent.stderr.2.txt"} {
		resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/evidence/"+name, nil, nil)
		if resp.StatusCode() != 200 {
			t.Fatalf("%s: status = %d: %s", name, resp.StatusCode(), resp.Body())
		}
		if got := string(resp.Body()); got != "raw-"+name {
			t.Errorf("%s: body = %q", name, got)
		}
		for k, want := range map[string]string{
			"Content-Type":           "application/octet-stream",
			"Content-Disposition":    `attachment; filename="` + name + `"`,
			"Cache-Control":          "no-store",
			"X-Content-Type-Options": "nosniff",
			"X-Bench-Mode":           string(testMode),
		} {
			if got := string(resp.Header.Peek(k)); got != want {
				t.Errorf("%s: header %s = %q, want %q", name, k, got, want)
			}
		}
	}
}

func TestRunEvidenceRefuses(t *testing.T) {
	tests := []struct {
		name    string
		index   ReadIndex
		run     string
		path    string
		status  int
		message string
	}{
		{name: "unknown name", index: testIndex(), run: "r_t01_A", path: "secret.txt", status: 404},
		{name: "path traversal", index: testIndex(), run: "r_t01_A", path: "..%2Ftools.jsonl", status: 404},
		{name: "missing file", index: sealIndex{fakeIndex: testIndex(), bad: map[string]error{}}, run: "r_t01_A", path: "tools.jsonl", status: 404},
		{name: "unsealed", index: sealIndex{fakeIndex: testIndex(), bad: map[string]error{}}, run: "r_t02_A", path: "tools.jsonl", status: 409, message: "not sealed"},
		{name: "seal mismatch", index: sealIndex{fakeIndex: testIndex(), bad: map[string]error{"r_t01_A": fmt.Errorf("changed: %w", schemas.ErrSealMismatch)}}, run: "r_t01_A", path: "tools.jsonl", status: 409, message: "does not match its seal"},
		{name: "no verifier", index: testIndex(), run: "r_t01_A", path: "tools.jsonl", status: 503},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "unsealed" {
				idx := tt.index.(sealIndex).fakeIndex
				for i := range idx.runs {
					if idx.runs[i].Run.RunID == tt.run {
						idx.runs[i].Sealed, idx.runs[i].SealHash = false, ""
					}
				}
			}
			d := fullDeps()
			d.index = tt.index
			resp := newTestClient(t, d).do(t, "GET", APIPrefix+"/runs/"+tt.run+"/evidence/"+tt.path, nil, nil)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if tt.message != "" && !strings.Contains(string(resp.Body()), tt.message) {
				t.Fatalf("body %s lacks %q", resp.Body(), tt.message)
			}
			if strings.Contains(string(resp.Header.Peek("Content-Disposition")), "attachment") {
				t.Fatal("a refused file was offered as a download")
			}
		})
	}
}

func TestRunDiffFormats(t *testing.T) {
	c := newTestClient(t, fullDeps())
	t.Run("json default", func(t *testing.T) {
		body := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A/diff", nil, nil))
		diff, _ := body["diff"].(map[string]any)
		if body["run_id"] != "r_t01_A" || !strings.Contains(body["patch"].(string), "+b") || diff == nil {
			t.Fatalf("body = %v", body)
		}
	})
	t.Run("text", func(t *testing.T) {
		resp := c.do(t, "GET", "/api/bench/runs/r_t01_A/diff?format=text", nil, nil)
		if ct := string(resp.Header.ContentType()); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("content type = %q", ct)
		}
		if !strings.HasPrefix(string(resp.Body()), "diff --git") {
			t.Errorf("body = %q", resp.Body())
		}
	})
}

func TestRunEventsAndDetail(t *testing.T) {
	idx := testIndex()
	delete(idx.files["r_t02_A"], FileEvidence)
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A/events", nil, nil))
	if events, _ := body["events"].([]any); len(events) != 1 || body["complete"] != true {
		t.Fatalf("events body = %v", body)
	}
	body = decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t02_A/events", nil, nil))
	if body["complete"] != false {
		t.Errorf("no evidence must be incomplete: %v", body)
	}
	body = decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil))
	metrics, _ := body["metrics"].(map[string]any)
	gates, _ := body["gates"].([]any)
	receipts, _ := body["receipts"].([]any)
	canonical, _ := body["canonical"].(map[string]any)
	if len(gates) != 1 || len(receipts) != 1 || metrics["steps"] == nil || body["grade"] != nil ||
		body["grade_source"] != "original" || body["sealed"] != true || body["seal_hash"] != "sha256:seal" || canonical["canonical_version"] != "legacy" {
		t.Errorf("run detail = %v", body)
	}
	idx.files["r_t02_A"][FileGrade] = []byte(`{"passed":true}`)
	delete(idx.files["r_t02_A"], FileGates)
	delete(idx.files["r_t02_A"], FileReceipts)
	body = decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t02_A", nil, nil))
	grade, _ := body["grade"].(map[string]any)
	if gates, _ := body["gates"].([]any); gates == nil || len(gates) != 0 || grade["passed"] != true {
		t.Errorf("run detail without gates = %v", body)
	}
	if receipts, _ := body["receipts"].([]any); receipts == nil || len(receipts) != 0 {
		t.Errorf("run detail without receipts = %v", body)
	}
	body = decodeBody(t, c.do(t, "GET", "/api/bench/runs/r_t01_A/receipts", nil, nil))
	if receipts, _ := body["receipts"].([]any); len(receipts) != 1 {
		t.Errorf("receipts = %v", body)
	}
}

func TestRunDetailExposesTraceCoverage(t *testing.T) {
	idx := testIndex()
	diagnostics := &schemas.ParseDiagnostics{Source: schemas.TraceSourceProtocol, Quality: schemas.TraceQualityPartial,
		Parser: "bifrost-agent-event", ParserVersion: "1.0.0", Emitted: 2, Malformed: 1}
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t01_A" {
			idx.runs[i].Run.Agent.TraceSource = diagnostics.Source
			idx.runs[i].Run.Agent.TraceQuality = diagnostics.Quality
			idx.runs[i].Run.Agent.TraceDiagnostics = diagnostics
		}
	}
	canonical, err := json.Marshal(schemas.CanonicalRun{AgentTrace: diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	idx.files["r_t01_A"][FileCanonical] = canonical
	d := fullDeps()
	d.index = idx
	resp := newTestClient(t, d).do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil)
	var detail schemas.RunDetail
	if err := json.Unmarshal(resp.Body(), &detail); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != 200 || detail.Run.Agent.TraceQuality != diagnostics.Quality || detail.Canonical == nil || detail.Canonical.AgentTrace == nil || detail.Canonical.AgentTrace.Malformed != 1 {
		t.Fatalf("trace metadata missing: %s", resp.Body())
	}
}

func TestDiffSummary(t *testing.T) {
	patch := "diff --git a/a.py b/a.py\nnew file mode 100644\n--- /dev/null\n+++ b/a.py\n@@\n+x\n" +
		"diff --git a/b.py b/b.py\ndeleted file mode 100644\n" +
		"diff --git a/c.py b/c.py\n--- a/c.py\n+++ b/c.py\n"
	d := DiffSummary([]byte(patch))
	want := []string{"a.py:added", "b.py:deleted", "c.py:modified"}
	if len(d.Files) != len(want) || !strings.HasPrefix(d.SHA256, "sha256:") || d.Path != FileDiff {
		t.Fatalf("summary = %+v", d)
	}
	for i, f := range d.Files {
		if got := f.Path + ":" + f.Status; got != want[i] {
			t.Errorf("file %d = %s, want %s", i, got, want[i])
		}
	}
	if empty := DiffSummary(nil); len(empty.Files) != 0 {
		t.Errorf("empty diff files = %v", empty.Files)
	}
}

func TestReadEventsRejectsMalformedLine(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_A"][FileEvents] = []byte("{\"type\":\"claim\"}\nnot json\n")
	if _, err := readEvents(idx, "r_t01_A"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want line 2", err)
	}
}

func TestRunDetailSealError(t *testing.T) {
	mismatch := fmt.Errorf("verify run r_t01_A: file metrics.json changed: %w", schemas.ErrSealMismatch)
	tests := []struct {
		name  string
		index ReadIndex
		want  string // seal_error, "" for absent
	}{
		{"an index that cannot verify", testIndex(), ""},
		{"a run that verifies", sealIndex{testIndex(), map[string]error{}}, ""},
		{"a run that does not verify", sealIndex{testIndex(), map[string]error{"r_t01_A": mismatch}}, mismatch.Error()},
		{"a check that could not run", sealIndex{testIndex(), map[string]error{"r_t01_A": errors.New("read seal.json: permission denied")}}, "read seal.json: permission denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fullDeps()
			d.index = tt.index
			body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil))
			got, present := body["seal_error"]
			if tt.want == "" {
				if present {
					t.Fatalf("seal_error = %v, want absent", got)
				}
				return
			}
			if got != tt.want || body["metrics"] == nil {
				t.Fatalf("seal_error = %v, want %q, with the files still served", got, tt.want)
			}
		})
	}
}

func TestRunDetailEvidenceContract(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_A"][FileEvidence] = []byte(`{"requests":{"complete":true},` +
		`"gateway_rows":[{"body":"sk-secret-row"}],` +
		`"evidence_contract":{"required":["trace","grade"],"available":["trace","grade"],"quality":"complete"},` +
		`"adapter_fault":{"version":"1","run_id":"r_t01_A","rule_hash":"sha256:r","complete":true,"ledger_hash":"sha256:l"}}`)
	d := fullDeps()
	d.index = idx
	resp := newTestClient(t, d).do(t, "GET", "/api/bench/runs/r_t01_A", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	var detail schemas.RunDetail
	if err := json.Unmarshal(resp.Body(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Evidence == nil || detail.Evidence.Quality != schemas.EvidenceQualityComplete || len(detail.Evidence.Available) != 2 {
		t.Fatalf("evidence contract = %+v, want the quality from evidence.json", detail.Evidence)
	}
	if detail.AdapterFault == nil || detail.AdapterFault.RuleHash != "sha256:r" {
		t.Errorf("adapter fault = %+v, want the one from evidence.json", detail.AdapterFault)
	}
	// Only the contract and the adapter fault leave the server.
	if strings.Contains(string(resp.Body()), "sk-secret-row") {
		t.Errorf("run detail leaks the gateway rows of evidence.json")
	}
}
