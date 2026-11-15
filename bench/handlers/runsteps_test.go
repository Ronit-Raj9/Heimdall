package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// dirIndex is testIndex with run files read from a run folder on disk
// (<root>/<run id>/<name>), the way the store serves them.
type dirIndex struct {
	*fakeIndex
	root string
}

func (d *dirIndex) RunFile(runID, name string) ([]byte, error) {
	if strings.ContainsAny(name, `/\`) || strings.ContainsAny(runID, `/\`) {
		return nil, fmt.Errorf("bad name %q: %w", name, os.ErrNotExist)
	}
	return os.ReadFile(filepath.Join(d.root, runID, name))
}

// stepsFixture writes run folders under t.TempDir: r_t01_A has a step
// timeline with one bad line and observers in evidence.json, r_t01_B has
// only evidence.json steps and an observers.json, r_t02_A is an old run
// with neither.
func stepsFixture(t *testing.T) *dirIndex {
	t.Helper()
	root := t.TempDir()
	write := func(runID, name, body string) {
		dir := filepath.Join(root, runID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	steps := strings.Join([]string{
		`{"seq":1,"ts":"2026-11-15T10:00:01Z","kind":"model_turn","source":"gateway","proof":"verified","turn":1,"summary":"model m answered","request_id":"q1","counted":true}`,
		`{"seq":2,"ts":"2026-11-15T10:00:02Z","kind":"tool_request","source":"gateway","proof":"verified","turn":1,"summary":"<script>alert(1)</script>","tool_name":"bash","tool_call_id":"c1","execution":"executed_with_proof","witnessed_by":[3]}`,
		`not json at all`,
		``,
		`{"seq":3,"ts":"2026-11-15T10:00:03Z","kind":"command","source":"process_observer","proof":"strong","turn":1,"summary":"pytest -q","pid":42,"ppid":7,"exit_code":1,"witness_of":2,"conflict":"agent says exit 0"}`,
	}, "\n") + "\n"
	write("r_t01_A", schemas.StepsFile, steps)
	write("r_t01_A", FileEvidence, `{"requests":{"complete":true},"observers":[{"observer":"process","state":"observed","mode":"proc_poll","interval_ms":50,"observer_coverage":"full","records":4,"dropped":1,"pids_allocated":5,"pids_seen":4},{"observer":"workspace","state":"missing","reason":"inotify watch limit reached","records":0}]}`)
	write("r_t01_B", FileEvidence, `{"steps":[{"seq":1,"ts":"2026-11-15T10:00:01Z","kind":"model_turn","source":"gateway","proof":"verified","turn":1,"summary":"from evidence","counted":true}],"observers":[{"observer":"process","state":"off","records":0}]}`)
	write("r_t01_B", schemas.ObserversFile, `[{"observer":"process","state":"observed","records":2},{"observer":"workspace","state":"off","reason":"switched off in config","records":0}]`)
	write("r_t02_A", FileEvidence, `{"requests":{"complete":true}}`)
	return &dirIndex{fakeIndex: testIndex(), root: root}
}

func TestRunStepsTimeline(t *testing.T) {
	d := fullDeps()
	d.index = stepsFixture(t)
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/steps", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	if ct := string(resp.Header.Peek("Content-Type")); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q, want JSON", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatal(err)
	}
	checkKeys(t, "RunSteps", body, loadKeys(t, "RunSteps"), true)
	for path, typ := range map[string]string{"steps[]": "StepRecord", "observers[]": "ObserverStatus"} {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("RunSteps.%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
	var out RunSteps
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || out.RunID != "r_t01_A" || out.SkippedLines != 1 || out.Truncated {
		t.Fatalf("available %v, run %q, skipped %d, truncated %v", out.Available, out.RunID, out.SkippedLines, out.Truncated)
	}
	if len(out.Steps) != 3 || out.Steps[0].Seq != 1 || out.Steps[2].Seq != 3 {
		t.Fatalf("steps = %+v", out.Steps)
	}
	req := out.Steps[1]
	if req.Summary != "<script>alert(1)</script>" || req.Execution != schemas.ExecutedWithProof || len(req.WitnessedBy) != 1 || req.WitnessedBy[0] != 3 {
		t.Fatalf("tool request row = %+v", req)
	}
	if cmd := out.Steps[2]; cmd.WitnessOf != 2 || cmd.Conflict == "" || cmd.ExitCode == nil || *cmd.ExitCode != 1 {
		t.Fatalf("command row = %+v", cmd)
	}
	// The JSON encoder escapes markup, so the body never carries a raw tag.
	if bytes.Contains(resp.Body(), []byte("<script>")) {
		t.Fatal("a raw <script> tag reached the response body")
	}
	if len(out.Observers) != 2 || out.Observers[0].IntervalMS != 50 || out.Observers[1].State != schemas.ObserverFailed ||
		out.Observers[1].Reason == "" {
		t.Fatalf("observers = %+v", out.Observers)
	}
}

func TestRunStepsFallbacks(t *testing.T) {
	d := fullDeps()
	d.index = stepsFixture(t)
	c := newTestClient(t, d)

	// Steps from evidence.json, observers from observers.json over evidence.json.
	var out RunSteps
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_B/steps", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || len(out.Steps) != 1 || out.Steps[0].Summary != "from evidence" {
		t.Fatalf("steps = %+v", out)
	}
	if len(out.Observers) != 2 || out.Observers[0].State != schemas.ObserverOn {
		t.Fatalf("observers.json not preferred: %+v", out.Observers)
	}

	// A run recorded before the timeline: 200, available false, empty lists.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_A/steps", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("old run status = %d: %s", resp.StatusCode(), resp.Body())
	}
	body := decodeBody(t, resp)
	checkKeys(t, "RunSteps (old run)", body, loadKeys(t, "RunSteps"), true)
	if body["available"] != false {
		t.Fatalf("available = %v, want false", body["available"])
	}
	for _, k := range []string{"steps", "observers"} {
		if arr, ok := body[k].([]any); !ok || len(arr) != 0 {
			t.Fatalf("%s = %#v, want an empty list", k, body[k])
		}
	}

	// A run with no evidence at all is the same.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_B/steps", nil, nil)
	if resp.StatusCode() != 200 || decodeBody(t, resp)["available"] != false {
		t.Fatalf("run without files: %d %s", resp.StatusCode(), resp.Body())
	}

	for _, path := range []string{"/runs/r_nope/steps", "/runs/bad%20id/steps"} {
		if resp := c.do(t, "GET", APIPrefix+path, nil, nil); resp.StatusCode() != 404 {
			t.Errorf("%s: status = %d, want 404", path, resp.StatusCode())
		}
	}
}

func TestRunStepsBadObservers(t *testing.T) {
	idx := stepsFixture(t)
	if err := os.WriteFile(filepath.Join(idx.root, "r_t01_A", schemas.ObserversFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/steps", nil, nil); resp.StatusCode() != 500 {
		t.Fatalf("status = %d, want 500 for an unreadable observers.json", resp.StatusCode())
	}
}

func TestParseStepLinesCaps(t *testing.T) {
	line := `{"seq":1,"ts":"2026-11-15T10:00:01Z","kind":"command","source":"shim","proof":"partial","turn":0,"summary":"x"}` + "\n"

	steps, truncated, skipped := parseStepLines([]byte(strings.Repeat(line, maxStepRows+5)))
	if len(steps) != maxStepRows || !truncated || skipped != 0 {
		t.Fatalf("row cap: %d rows, truncated %v, skipped %d", len(steps), truncated, skipped)
	}

	steps, truncated, _ = parseStepLines([]byte(strings.Repeat(line, 3)))
	if len(steps) != 3 || truncated {
		t.Fatalf("small input: %d rows, truncated %v", len(steps), truncated)
	}

	// Past the byte cap: whole rows before the cut are kept, the cut row is
	// dropped without counting as bad.
	long := `{"seq":2,"ts":"2026-11-15T10:00:01Z","kind":"command","source":"shim","proof":"partial","turn":0,"summary":"` +
		strings.Repeat("a", 1<<16) + `"}` + "\n"
	n := maxStepsBytes/len(long) + 2
	steps, truncated, skipped = parseStepLines([]byte(strings.Repeat(long, n)))
	if !truncated || skipped != 0 || len(steps) != maxStepsBytes/len(long) {
		t.Fatalf("byte cap: %d rows (want %d), truncated %v, skipped %d", len(steps), maxStepsBytes/len(long), truncated, skipped)
	}

	steps, truncated, skipped = parseStepLines(nil)
	if steps == nil || len(steps) != 0 || truncated || skipped != 0 {
		t.Fatalf("empty input: %#v %v %d", steps, truncated, skipped)
	}
}
