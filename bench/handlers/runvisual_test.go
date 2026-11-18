package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// X1 vision grading: GET /runs/{id}/visual/{name}.

type visualIndex struct {
	*fakeIndex
	verifyErr error
}

func (v *visualIndex) RunVisual(runID, name string) ([]byte, error) {
	if data, ok := v.files[runID]["visual/"+name]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("run %s visual %s: %w", runID, name, schemas.ErrNotFound)
}

func (v *visualIndex) VerifySeal(context.Context, string) error { return v.verifyErr }

var capturePNG = []byte("\x89PNG\r\n\x1a\nbench-encoded capture")

func visualFixture(t *testing.T) *visualIndex {
	t.Helper()
	idx := testIndex()
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t02_A" {
			idx.runs[i].Sealed, idx.runs[i].SealHash = false, ""
		}
	}
	good := utils.SHA256Bytes(capturePNG)
	grade := schemas.Grade{Passed: true, Visual: &schemas.VisualGrade{
		Capture:   schemas.VisualImage{Path: "visual/capture.png", SHA256: good, Width: 4, Height: 4},
		Reference: &schemas.VisualImage{Path: "visual/reference.png", SHA256: good},
		Diff:      &schemas.VisualImage{Path: "visual/elsewhere.png", SHA256: good},
	}}
	g, _ := json.Marshal(grade)
	seal, _ := json.Marshal(schemas.SealRecord{RunID: "r_t01_A", Files: map[string]string{"visual/capture.png": good, "visual/reference.png": utils.SHA256Bytes([]byte("other"))}})
	for _, runID := range []string{"r_t01_A", "r_t02_A"} {
		idx.files[runID][FileGrade] = g
		idx.files[runID][FileSeal] = seal
		idx.files[runID]["visual/capture.png"] = capturePNG
		idx.files[runID]["visual/reference.png"] = capturePNG
	}
	return &visualIndex{fakeIndex: idx}
}

func TestRunVisualServesSealedCapture(t *testing.T) {
	d := fullDeps()
	d.index = visualFixture(t)
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/visual/capture.png", nil, nil)
	if resp.StatusCode() != 200 || !bytes.Equal(resp.Body(), capturePNG) {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	for k, v := range map[string]string{"Content-Type": "image/png", "X-Content-Type-Options": "nosniff", "Content-Security-Policy": "sandbox", "Cache-Control": "no-store"} {
		if got := string(resp.Header.Peek(k)); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

func TestRunVisualRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		status     int
	}{
		{"unsealed run", "/runs/r_t02_A/visual/capture.png", 409},
		{"seal disagrees", "/runs/r_t01_A/visual/reference.png", 409},
		{"recorded elsewhere", "/runs/r_t01_A/visual/diff.png", 404},
		{"other name", "/runs/r_t01_A/visual/grade.json", 404},
		{"unknown run", "/runs/r_none/visual/capture.png", 404},
	} {
		d := fullDeps()
		d.index = visualFixture(t)
		c := newTestClient(t, d)
		resp := c.do(t, "GET", APIPrefix+tc.path, nil, nil)
		if resp.StatusCode() != tc.status || bytes.Contains(resp.Body(), capturePNG) {
			t.Errorf("%s: status %d, want %d: %s", tc.name, resp.StatusCode(), tc.status, resp.Body())
		}
	}
	d := fullDeps()
	d.index = visualFixture(t).fakeIndex
	c := newTestClient(t, d)
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/visual/capture.png", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("index without a visual reader: status %d", resp.StatusCode())
	}
}

// TestRunVisualContract pins the visual grade's keys in the run detail to
// testdata/contract/Visual*.keys, the key lists of the bench.ts types.
func TestRunVisualContract(t *testing.T) {
	idx := visualFixture(t)
	sc := 0.5
	g := schemas.Grade{Visual: &schemas.VisualGrade{
		Capture: schemas.VisualImage{Path: "visual/capture.png", SHA256: "a", Width: 4, Height: 4},
		Source:  schemas.VisualSource{Kind: schemas.VisualCaptureArtifact, Format: "png"},
		Checks:  []schemas.VisualCheckResult{{ID: "ssim", Method: schemas.VisualMethodSSIM, Value: 0.9, Threshold: 0.98}},
		Score:   &sc,
		Judge:   &schemas.VisualJudgeResult{Model: "m", PromptVersion: "visual-v1", Aggregate: "majority", Votes: []string{"pass"}, Scores: []float64{1}, Verdict: "pass", USD: schemas.Unknown("x", schemas.EvidenceGateway)},
	}}
	b, _ := json.Marshal(g)
	idx.files["r_t01_A"][FileGrade] = b
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/runs/r_t01_A", nil, nil))
	for path, typ := range map[string]string{"grade.visual": "VisualGrade", "grade.visual.capture": "VisualImage", "grade.visual.source": "VisualSource",
		"grade.visual.checks[]": "VisualCheckResult", "grade.visual.judge": "VisualJudgeResult"} {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
}
