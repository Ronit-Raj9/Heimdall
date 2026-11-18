package graders

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// X1 vision grading: the visual grader on artifact images, and the vision
// judge against a scripted model that answers rubric JSON.

// chartPNG draws a 120x80 white image with two bars; c is the second bar's
// colour and h2 its height.
func chartPNG(t *testing.T, c color.NRGBA, h2 int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 120, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	bar := func(x0, h int, col color.NRGBA) {
		for y := 70 - h; y < 70; y++ {
			for x := x0; x < x0+20; x++ {
				img.Set(x, y, col)
			}
		}
	}
	bar(20, 50, color.NRGBA{0x25, 0x63, 0xeb, 255})
	bar(70, h2, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var barBlue = color.NRGBA{0x25, 0x63, 0xeb, 255}

func visualRC(t *testing.T, spec *schemas.VisualCheck, artifact []byte) *schemas.RunContext {
	t.Helper()
	taskDir, runDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(taskDir, "verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "verifier", "reference.png"), chartPNG(t, barBlue, 30), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "verifier", "rubric.md"), []byte("- two blue bars\n- the second bar is shorter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if artifact != nil {
		dir := filepath.Join(runDir, utils.ArtifactsDir, "chart")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "chart.png"), artifact, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &schemas.RunContext{
		Suite: &schemas.Suite{ID: "s", Version: "1.0.0", Root: t.TempDir(), Grader: VisualType},
		Task: &schemas.Task{ID: "chart", Dir: taskDir, Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact,
			Artifacts: []schemas.ArtifactSpec{{ID: "chart", Path: "out/chart.png", Required: true}}, Visual: spec}},
		Run: &schemas.Run{RunID: "r_vis1", ExpID: "e_vis"},
		Dir: runDir,
		Key: &schemas.RunKey{ID: "vk-run", Value: "sk-run"},
	}
}

func chartSpec() *schemas.VisualCheck {
	return &schemas.VisualCheck{
		Capture:   schemas.VisualCapture{Kind: schemas.VisualCaptureArtifact, Artifact: "chart"},
		Reference: "verifier/reference.png",
		Method:    schemas.VisualMethodPixel, Tolerance: 4,
		Checks: []schemas.VisualAssert{
			{Method: schemas.VisualMethodDimensions},
			{ID: "bars_blue", Method: schemas.VisualMethodColors, Colors: []string{"#2563eb"}, MinShare: 0.1},
		},
	}
}

func TestVisualGradeArtifactImage(t *testing.T) {
	g := newVisual(VisualConfig{})
	rc := visualRC(t, chartSpec(), chartPNG(t, barBlue, 30))
	gr, err := g.Grade(t.Context(), rc, &schemas.Diff{SHA256: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if !gr.Passed || gr.Visual == nil || gr.Visual.Passed != 3 || *gr.Visual.Score != 1 || gr.Judged {
		t.Fatalf("reference chart: %+v %+v", gr, gr.Visual)
	}
	v := gr.Visual
	if v.Source.Kind != schemas.VisualCaptureArtifact || v.Source.Artifact != "artifacts/chart/chart.png" || v.Capture.Width != 120 || v.Reference == nil || v.Diff == nil {
		t.Fatalf("records: %+v", v)
	}
	for _, f := range []string{VisualCaptureFile, VisualReferenceFile, VisualDiffFile} {
		b, err := os.ReadFile(filepath.Join(rc.Dir, f))
		if err != nil {
			t.Fatalf("%s not written: %v", f, err)
		}
		if f == VisualCaptureFile && utils.SHA256Bytes(b) != v.Capture.SHA256 {
			t.Fatal("capture hash does not match the file")
		}
	}

	wrong := visualRC(t, chartSpec(), chartPNG(t, color.NRGBA{0xdc, 0x26, 0x26, 255}, 30))
	gr, err = g.Grade(t.Context(), wrong, &schemas.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	if gr.Passed || gr.InvalidEvaluator || gr.Visual.Passed != 2 || !strings.Contains(gr.Reason, "failed: pixel (600 of 9600 pixels") {
		t.Fatalf("red bar: %+v", gr)
	}

	missing := visualRC(t, chartSpec(), nil)
	gr, err = g.Grade(t.Context(), missing, &schemas.Diff{})
	if err != nil || gr.Passed || gr.InvalidEvaluator || !strings.Contains(gr.Reason, "artifact chart/chart.png was not produced") {
		t.Fatalf("missing artifact: %+v %v", gr, err)
	}

	broken := visualRC(t, chartSpec(), []byte("not an image"))
	gr, err = g.Grade(t.Context(), broken, &schemas.Diff{})
	if err != nil || gr.Passed || gr.InvalidEvaluator || !strings.Contains(gr.Reason, "artifact image") {
		t.Fatalf("broken artifact: %+v %v", gr, err)
	}
}

func TestVisualGradeRefusesWhatTheBenchCannotCapture(t *testing.T) {
	g := newVisual(VisualConfig{})
	spec := chartSpec()
	spec.Capture = schemas.VisualCapture{Kind: schemas.VisualCaptureBrowser, URL: "http://webapp:8000/"}
	gr, err := g.Grade(t.Context(), visualRC(t, spec, nil), &schemas.Diff{})
	if err != nil || !gr.InvalidEvaluator || !strings.Contains(gr.Reason, `no started browser service "browser"`) {
		t.Fatalf("no browser service: %+v %v", gr, err)
	}
	spec.Capture = schemas.VisualCapture{Kind: schemas.VisualCaptureArtifact, Artifact: "chart", Scale: 9}
	gr, _ = g.Grade(t.Context(), visualRC(t, spec, chartPNG(t, barBlue, 30)), &schemas.Diff{})
	if !gr.InvalidEvaluator || !strings.Contains(gr.Reason, "scale") {
		t.Fatalf("bad scale: %+v", gr)
	}
	spec = chartSpec()
	spec.Reference = "../outside.png"
	gr, _ = g.Grade(t.Context(), visualRC(t, spec, chartPNG(t, barBlue, 30)), &schemas.Diff{})
	if !gr.InvalidEvaluator || !strings.Contains(gr.Reason, "stay inside") {
		t.Fatalf("reference outside the task: %+v", gr)
	}
	if g.VerifierNetwork() != "none" {
		t.Fatal("verifier network")
	}
}

// scriptedVision is an OpenAI-compatible endpoint that plays a vision model:
// it answers each request with the next scripted reply and records the raw
// request bodies.
type scriptedVision struct {
	mu      sync.Mutex
	replies []string
	model   string
	bodies  []map[string]any
}

func (s *scriptedVision) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	s.bodies = append(s.bodies, body)
	i := min(len(s.bodies)-1, len(s.replies)-1)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"model":   s.model,
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": s.replies[i]}}},
		"usage":   map[string]any{"prompt_tokens": 1500, "completion_tokens": 40},
	})
}

func judgedVisual(t *testing.T, replies []string, agg string, votes int) (*visualGrader, *scriptedVision, *fakeGateway, *schemas.RunContext) {
	t.Helper()
	sv := &scriptedVision{replies: replies, model: "vision-model"}
	srv := httptest.NewServer(sv)
	t.Cleanup(srv.Close)
	gw := &fakeGateway{keyValue: "sk-judge"}
	g := newVisual(VisualConfig{Judge: JudgeConfig{Gateway: gw, URL: srv.URL, PriceRef: "snap@x",
		Price: func(m string, tok schemas.Tokens) (float64, bool) {
			return float64(tok.Fresh)*1e-6 + float64(tok.Out)*1e-5, m == "vision-model"
		}}})
	spec := chartSpec()
	spec.Rubric = "verifier/rubric.md"
	spec.Judge = &schemas.VisualJudgeSpec{Provider: "anthropic", Model: "vision-model", PromptVersion: PromptVisualV1, Votes: votes, Aggregate: agg}
	rc := visualRC(t, spec, chartPNG(t, barBlue, 30))
	rc.Suite.Grading = schemas.GradingJudged
	return g, sv, gw, rc
}

func TestVisualJudgeMajorityWithoutCalibration(t *testing.T) {
	pass := `{"verdict": "pass", "score": 1, "rationale": "Both bars are blue and the second is shorter."}`
	fail := "```json\n{\"verdict\": \"fail\", \"score\": 0.5, \"rationale\": \"one point missed\"}\n```"
	g, sv, gw, rc := judgedVisual(t, []string{pass, fail, pass}, "", 3)
	gr, err := g.Grade(t.Context(), rc, &schemas.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	j := gr.Visual.Judge
	if !gr.Passed || !gr.Judged || j == nil || j.Verdict != "pass" || j.Decides || j.Calibration != "" || !strings.Contains(j.Reason, "no calibration record for vision-model") {
		t.Fatalf("grade %+v judge %+v", gr, j)
	}
	if strings.Join(j.Votes, ",") != "pass,fail,pass" || len(j.Scores) != 3 || *j.Score != 0.833333 || j.Rationale != "Both bars are blue and the second is shorter." {
		t.Fatalf("votes %+v", j)
	}
	_, sha, _ := VisualPrompt(PromptVisualV1)
	rec := gr.Judge
	if rec.Model != "vision-model" || rec.PromptVersion != PromptVisualV1 || rec.PromptSHA256 != sha || strings.Join(rec.Votes, ",") != "correct,incorrect,correct" || rec.ServedModel != "vision-model" {
		t.Fatalf("judge record %+v", rec)
	}
	if _, ok := rec.USD.Float(); !ok || rec.PriceRef != "snap@x" {
		t.Fatalf("judge cost %+v", rec.USD)
	}
	if len(gw.minted) != 1 || len(gw.revoked) != 1 || gw.minted[0].Agent.Model != "vision-model" || !strings.HasPrefix(gw.minted[0].Run.RunID, "r_vis1v") {
		t.Fatalf("key minted %d revoked %d", len(gw.minted), len(gw.revoked))
	}
	body := sv.bodies[0]
	if body["model"] != "anthropic/vision-model" || body["temperature"].(float64) != 0 {
		t.Fatalf("request %v", body["model"])
	}
	parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 3 || !strings.Contains(parts[0].(map[string]any)["text"].(string), "two blue bars") ||
		!strings.HasPrefix(parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,") {
		t.Fatalf("prompt parts %d", len(parts))
	}
}

func TestVisualJudgeFailsAndUndecided(t *testing.T) {
	fail := `{"verdict":"fail","score":0.2,"rationale":"red bar"}`
	g, _, _, rc := judgedVisual(t, []string{fail}, "", 1)
	gr, err := g.Grade(t.Context(), rc, &schemas.Diff{})
	if err != nil || gr.Passed || gr.InvalidEvaluator || gr.Visual.Judge.Verdict != "fail" || !strings.Contains(gr.Reason, "judge fail") {
		t.Fatalf("judge fail: %+v %v", gr, err)
	}
	g, _, _, rc = judgedVisual(t, []string{"The chart looks fine to me.", `{"verdict":"pass","score":1}`, `{"verdict":"maybe","score":1}`}, "", 3)
	gr, _ = g.Grade(t.Context(), rc, &schemas.Diff{})
	if gr.Passed || !gr.InvalidEvaluator || gr.Visual.Judge.Verdict != "undecided" {
		t.Fatalf("no majority: %+v %+v", gr, gr.Visual.Judge)
	}
	g, _, _, rc = judgedVisual(t, []string{`{"verdict":"pass","score":0.4}`, `{"verdict":"pass","score":0.5}`}, "mean", 2)
	rc.Task.Output.Visual.Judge.PassScore = 0.6
	gr, _ = g.Grade(t.Context(), rc, &schemas.Diff{})
	if gr.Passed || gr.Visual.Judge.Verdict != "fail" || *gr.Visual.Judge.Score != 0.45 {
		t.Fatalf("mean under pass_score: %+v", gr.Visual.Judge)
	}
}

func TestVisualJudgeWithCalibrationRecord(t *testing.T) {
	g, _, _, rc := judgedVisual(t, []string{`{"verdict":"pass","score":1}`}, "", 1)
	_, sha, _ := VisualPrompt(PromptVisualV1)
	ag := 0.9
	rec := &schemas.CalibrationRecord{RecordID: "cr_visual1", Suite: "s", SuiteVersion: "1.0.0", Provider: "anthropic", Model: "vision-model",
		PromptVersion: PromptVisualV1, PromptSHA256: sha, Templates: map[string]string{PromptVisualV1: sha}, Votes: 1,
		Human: schemas.CalibrationStats{Items: 40, Agreement: schemas.CalibrationRate{K: 36, N: 40, Value: &ag}}, CreatedAt: time.Now().UTC()}
	if err := calibration.WriteRecord(rc.Suite.Root, rec); err != nil {
		t.Fatal(err)
	}
	gr, err := g.Grade(t.Context(), rc, &schemas.Diff{})
	if err != nil {
		t.Fatal(err)
	}
	j := gr.Visual.Judge
	if !j.Decides || j.Calibration == "" || !strings.HasPrefix(j.Calibration, "cr_visual1@") || gr.Judge.Calibration != j.Calibration || j.Agreement == nil || j.Agreement.Agreement.K != 36 {
		t.Fatalf("calibrated judge %+v", j)
	}
}

func TestVisualJudgeNeedsAJudgedSuite(t *testing.T) {
	g, sv, _, rc := judgedVisual(t, []string{`{"verdict":"pass","score":1}`}, "", 1)
	rc.Suite.Grading = ""
	if _, err := g.Grade(t.Context(), rc, &schemas.Diff{}); err == nil || !strings.Contains(err.Error(), "grading: judged") {
		t.Fatalf("verified suite with a rubric: %v", err)
	}
	if len(sv.bodies) != 0 {
		t.Fatal("the judge was asked in a verified suite")
	}
}

func TestParseVisualReplyIsStrict(t *testing.T) {
	for reply, want := range map[string]string{
		`{"verdict":"pass","score":1,"rationale":"ok"}`:      "pass",
		` {"verdict":"FAIL","score":0}`:                      "fail",
		"```json\n{\"verdict\":\"pass\",\"score\":0.7}\n```": "pass",
		`{"verdict":"pass"}`:                                 "unparsable",
		`{"verdict":"pass","score":2}`:                       "unparsable",
		`{"verdict":"pass","score":1,"extra":true}`:          "unparsable",
		`{"verdict":"pass","score":1}{"verdict":"fail"}`:     "unparsable",
		"pass": "unparsable",
	} {
		if got, _, _ := ParseVisualReply(reply); got != want {
			t.Fatalf("ParseVisualReply(%q) = %s, want %s", reply, got, want)
		}
	}
}

func TestCheckVisualJudge(t *testing.T) {
	ok := &schemas.VisualJudgeSpec{Provider: "anthropic", Model: "m", PromptVersion: PromptVisualV1}
	if err := CheckVisualJudge(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*schemas.VisualJudgeSpec{nil, {Provider: "a", Model: "pin-me", PromptVersion: PromptVisualV1},
		{Provider: "a", Model: "m", PromptVersion: "qa-v1"}, {Provider: "a", Model: "m", PromptVersion: PromptVisualV1, Votes: 2},
		{Provider: "a", Model: "m", PromptVersion: PromptVisualV1, Aggregate: "max"}, {Provider: "a", Model: "m", PromptVersion: PromptVisualV1, PassScore: 2}} {
		if CheckVisualJudge(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
