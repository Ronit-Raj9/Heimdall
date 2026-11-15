package handlers

import (
	"context"
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
