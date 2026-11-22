package onboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/dataset"
	_ "github.com/maximhq/bifrost/bench/importers/harbor"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

const faqCSV = `question,answer,wrong_answers
What is the hotel cap per night?,250,300
How many vacation days do new staff get?,20,
Which form files an expense?,EXP-7,EXP-9
How many vacation days do new staff get each year?,20,
`

func testEnv(t *testing.T) *Env {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"benchmarks", "uploads"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &Env{Root: root, Actor: "tester"}
}

func TestDetectKinds(t *testing.T) {
	env := testEnv(t)
	ctx := context.Background()
	cases := []struct {
		in     Input
		source string
		draft  bool
	}{
		{Input{Data: faqCSV, Name: "faq.csv"}, "rows", true},
		{Input{Data: `{"question":"q","answer":"a"}` + "\n", Name: "x.jsonl"}, "rows", true},
		{Input{Data: "openapi: 3.1.0\ninfo:\n  title: Shop\npaths: {}\n", Name: "api.yaml"}, "openapi", false},
		{Input{Data: `{"resourceSpans":[]}`, Name: "t.json"}, "traces", false},
		{Input{URL: "https://huggingface.co/datasets/org/name"}, "huggingface", true},
		{Input{URL: "https://github.com/acme/agent"}, "github", false},
		{Input{URL: "terminal-bench@2.0"}, "harbor-registry", false},
	}
	for _, c := range cases {
		ms, _, err := Detect(ctx, env, c.in)
		if err != nil {
			t.Fatalf("%v: %v", c.in.Label(), err)
		}
		if len(ms) == 0 || ms[0].Source != c.source || ms[0].CanDraft != c.draft {
			t.Errorf("%s: matches %+v, want %s (draft %v)", c.in.Label(), ms, c.source, c.draft)
		}
	}
	if _, _, err := Detect(ctx, env, Input{}); err == nil {
		t.Error("empty input accepted")
	}
}

func TestRowsDraftGatesEditPublish(t *testing.T) {
	env := testEnv(t)
	ctx := context.Background()
	in := Input{Data: faqCSV, Name: "faq.csv"}
	inv, err := Inspect(ctx, env, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Units != 4 || !inv.CanDraft || inv.Suggest.Suite != "faq" || inv.Suggest.Hold.Pct != 25 || len(inv.Samples) != 4 {
		t.Fatalf("inventory %+v", inv)
	}
	plan := inv.Suggest
	set, err := Draft(ctx, env, in, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !set.NewSuite || len(set.Tasks) != 4 {
		t.Fatalf("set: new %v tasks %d problems %v", set.NewSuite, len(set.Tasks), set.Problems)
	}
	held := 0
	for _, tk := range set.Tasks {
		if tk.HeldOut {
			held++
		}
		if len(tk.Checks) == 0 || !strings.Contains(tk.Checks[0], "must equal") {
			t.Errorf("%s checks %v", tk.ID, tk.Checks)
		}
		if !strings.Contains(tk.Instruction, "?") {
			t.Errorf("%s instruction %q", tk.ID, tk.Instruction)
		}
		if tk.Review != ReviewAccepted || len(tk.Controls) != 4 {
			t.Errorf("%s review %s controls %v", tk.ID, tk.Review, tk.Controls)
		}
		for _, g := range tk.Gates {
			if g.Gate == GateStatic && !g.OK {
				t.Errorf("%s static gate: %s", tk.ID, g.Message)
			}
		}
	}
	if held != 1 {
		t.Errorf("held out %d, want 1 of 4", held)
	}
	// The fourth row is a near copy of the second.
	last := set.Tasks[3]
	var dup *GateResult
	for i := range last.Gates {
		if last.Gates[i].Gate == GateDuplicate {
			dup = &last.Gates[i]
		}
	}
	if dup == nil || dup.OK {
		t.Errorf("duplicate gate on %s: %+v", last.ID, last.Gates)
	}
	if set.Tasks[1].Controls[2].Origin != "rule" || set.Tasks[0].Controls[2].Origin != "source" {
		t.Errorf("wrong answer origins %v / %v", set.Tasks[0].Controls, set.Tasks[1].Controls)
	}
	// The draft never lands in benchmarks/.
	if _, err := os.Stat(filepath.Join(env.Benchmarks(), "faq")); err == nil {
		t.Fatal("draft wrote into benchmarks/")
	}
	// Reload, drop the copy, reject one.
	set, err = LoadSet(env.Root, set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.EditTask(ctx, env, last.ID, TaskEdit{Drop: true}); err != nil {
		t.Fatal(err)
	}
	rej := ReviewRejected
	if err := set.EditTask(ctx, env, set.Tasks[2].ID, TaskEdit{Review: &rej}); err != nil {
		t.Fatal(err)
	}
	if len(set.Tasks) != 3 {
		t.Fatalf("after drop %d tasks", len(set.Tasks))
	}
	// Nothing is ready: publish refuses unless asked for not-ready tasks.
	if _, err := Publish(ctx, env, set, PublishOptions{}); err == nil {
		t.Fatal("published tasks readiness never ran on")
	}
	rec := &schemas.AdmissionRecord{Admitted: true, At: time.Now(), Checks: []schemas.AdmissionCheck{
		{Name: "reference_1", Want: suite.GotPass, Got: "pass", Passed: true},
		{Name: "empty_control", Want: suite.GotFail, Got: "fail", Passed: true},
		{Name: "wrong_answer_1", Want: suite.GotFail, Got: "fail", Passed: true},
		{Name: "exploit", Want: suite.GotFail, Got: "pass", Passed: false},
	}}
	set.ApplyReadiness(map[string]*schemas.AdmissionRecord{set.Tasks[0].ID: rec, set.Tasks[1].ID: rec}, nil)
	if cr := set.Tasks[0].CatchRate; cr == nil || cr.Caught != 2 || cr.Total != 3 {
		t.Errorf("catch rate %+v", cr)
	}
	res, err := Publish(ctx, env, set, PublishOptions{Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || len(res.Tasks) != 2 || len(res.Records) != 2 {
		t.Fatalf("publish %+v", res)
	}
	s, err := suite.Builtin(env.Benchmarks())[0].Load(ctx, "faq")
	if err != nil {
		t.Fatalf("published benchmark does not load: %v", err)
	}
	if len(s.Tasks) != 2 {
		t.Errorf("published tasks %d", len(s.Tasks))
	}
	r, err := ReadRecipe(env.Benchmarks(), "faq")
	if err != nil || r == nil || len(r.Sources) != 1 || r.Sources[0].Draft != set.ID {
		t.Errorf("recipe %+v %v", r, err)
	}
	if _, err := Publish(ctx, env, set, PublishOptions{}); err == nil {
		t.Error("published twice")
	}
	// A second draft into the existing benchmark avoids its task ids.
	set2, err := Draft(ctx, env, in, Plan{Suite: "faq"})
	if err != nil {
		t.Fatal(err)
	}
	if set2.NewSuite {
		t.Error("existing benchmark drafted as new")
	}
	for _, tk := range set2.Tasks {
		for _, old := range res.Tasks {
			if tk.ID == old {
				t.Errorf("task id %s reused", tk.ID)
			}
		}
	}
	list, err := ListSets(env.Root)
	if err != nil || len(list) != 2 {
		t.Errorf("list %v %v", list, err)
	}
}

func TestMappingNestedColumns(t *testing.T) {
	env := testEnv(t)
	data := `{"inputs":{"q":"Capital of France?"},"outputs":{"gold":"Paris"}}` + "\n" + `{"inputs":{"q":"2+2?"},"outputs":{"gold":"4"}}` + "\n"
	in := Input{Data: data, Name: "ls.jsonl"}
	inv, err := Inspect(context.Background(), env, in, "rows")
	if err != nil {
		t.Fatal(err)
	}
	// Y13's row mapper recognises the LangSmith-style inputs/outputs shape,
	// so the nested columns map to question and expected without a manual
	// mapping.
	if !inv.CanDraft || len(inv.Samples) == 0 || inv.Samples[0].Fields["question"] != "Capital of France?" || inv.Samples[0].Fields["expected"] != "Paris" {
		t.Fatalf("nested rows not auto-mapped: %+v", inv)
	}
	want := map[string]bool{"inputs.q": false, "outputs.gold": false}
	for _, c := range inv.Columns {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	if !want["inputs.q"] || !want["outputs.gold"] {
		t.Errorf("columns %v", inv.Columns)
	}
	set, err := Draft(context.Background(), env, in, Plan{Suite: "geo", Mapping: map[string]string{"question": "inputs.q", "expected": "outputs.gold"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tasks) != 2 || !strings.Contains(set.Tasks[0].Checks[0], "Paris") {
		t.Fatalf("tasks %+v problems %v", set.Tasks, set.Problems)
	}
}

func TestHuggingFaceRows(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		rows := map[string]any{"features": []map[string]any{{"name": "question"}, {"name": "answer"}},
			"rows": []map[string]any{
				{"row_idx": 0, "row": map[string]any{"question": "What is 6 x 7?", "answer": "42"}},
				{"row_idx": 1, "row": map[string]any{"question": "What is 2 + 3?", "answer": "5"}},
			}}
		switch r.URL.Path {
		case "/splits":
			json.NewEncoder(w).Encode(map[string]any{"splits": []map[string]any{{"config": "default", "split": "train"}, {"config": "default", "split": "test"}}})
		case "/first-rows", "/rows":
			if r.URL.Query().Get("split") != "test" {
				t.Errorf("split %s", r.URL.Query().Get("split"))
			}
			json.NewEncoder(w).Encode(rows)
		case "/size":
			json.NewEncoder(w).Encode(map[string]any{"size": map[string]any{"splits": []map[string]any{{"config": "default", "split": "test", "num_rows": 1319}}}})
		case "/api/datasets/openai/gsm8k":
			json.NewEncoder(w).Encode(map[string]any{"cardData": map[string]any{"license": "mit"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	env := testEnv(t)
	env.HFAPI, env.HFHub = srv.URL, srv.URL
	env.Token = func(context.Context, string) (string, error) { return "hf_x", nil }
	in := Input{URL: "https://huggingface.co/datasets/openai/gsm8k", Connection: "k1"}
	inv, err := Inspect(context.Background(), env, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Units != 1319 || inv.License != "mit" || inv.Suggest.Hold.Pct != 0 || inv.Suggest.Split != "default/test" || auth != "Bearer hf_x" {
		t.Fatalf("inventory %+v auth %q", inv, auth)
	}
	set, err := Draft(context.Background(), env, in, inv.Suggest)
	if err != nil {
		t.Fatal(err)
	}
	if !set.Public || len(set.Tasks) != 2 || set.Input.Connection != "" {
		t.Fatalf("set %+v", set)
	}
}

func TestHarborFolderDraft(t *testing.T) {
	env := testEnv(t)
	dst := filepath.Join(env.Root, "uploads", "onboard", "tb")
	if err := copyTree(filepath.Join("..", "importers", "harbor", "testdata", "tb"), dst); err != nil {
		t.Fatal(err)
	}
	in := Input{Upload: "onboard/tb"}
	ms, _, err := Detect(context.Background(), env, in)
	if err != nil || len(ms) == 0 || ms[0].Kind != KindHarbor {
		t.Fatalf("matches %+v %v", ms, err)
	}
	inv, err := Inspect(context.Background(), env, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Units < 5 || len(inv.Importers) == 0 || inv.Importers[0] != "harbor" {
		t.Fatalf("inventory %+v", inv)
	}
	plan := inv.Suggest
	plan.Suite, plan.Limit = "tb-draft", 3
	set, err := Draft(context.Background(), env, in, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tasks) == 0 || !set.Public {
		t.Fatalf("set tasks %d problems %v", len(set.Tasks), set.Problems)
	}
	for _, tk := range set.Tasks {
		if !strings.Contains(strings.Join(tk.Checks, " "), "hidden tests") {
			t.Errorf("%s checks %v", tk.ID, tk.Checks)
		}
	}
	if _, err := env.UploadPath("../etc/passwd"); err == nil {
		t.Error("upload path escaped")
	}
}

func TestRemoveTaskIDAndRedact(t *testing.T) {
	out, err := removeTaskID([]byte("id: s\ntasks: [a, b, c]\n"), "b")
	if err != nil || strings.Contains(string(out), "b,") || !strings.Contains(string(out), "a") {
		t.Errorf("remove: %s %v", out, err)
	}
	if got := Redact("mail jo@x.com or +1 415 555 0100, answer 2718281828"); strings.Contains(got, "jo@") || strings.Contains(got, "555") || !strings.Contains(got, "2718281828") {
		t.Errorf("redact %q", got)
	}
}

// Publishing a draft records a dataset version, so the Data tab lists the
// published tasks as version 1 right away.
func TestPublishRecordsDatasetVersion(t *testing.T) {
	env := testEnv(t)
	ctx := context.Background()
	in := Input{Data: faqCSV, Name: "faq.csv"}
	inv, err := Inspect(ctx, env, in, "")
	if err != nil {
		t.Fatal(err)
	}
	set, err := Draft(ctx, env, in, inv.Suggest)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Publish(ctx, env, set, PublishOptions{IncludeUnready: true, Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	store := dataset.Open(env.Root)
	vs, err := store.Versions("faq")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("versions %+v, want 1", vs)
	}
	v := vs[0]
	if v.N != 1 || v.Actor != "alice" || v.Tasks != len(res.Tasks) || len(v.Added) != len(res.Tasks) || !strings.Contains(v.Source, set.ID) {
		t.Fatalf("version %+v, published %v", v, res.Tasks)
	}
	if res.DatasetVersion != 1 {
		t.Errorf("result dataset version %d, want 1", res.DatasetVersion)
	}
	snap, err := store.Snapshot("faq", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range res.Tasks {
		sha := snap.Tasks[id]
		row, err := store.Row("faq", sha)
		if err != nil || row.ID != id {
			t.Fatalf("row of %s (%q): %+v %v", id, sha, row, err)
		}
		if h, _ := row.Metadata["files_sha256"].(string); len(h) != 64 {
			t.Errorf("row %s has no folder hash: %+v", id, row.Metadata)
		}
	}
}

// TestRowsInventoryNoWrongAnswerWarning (N47): rows without a wrong answer get
// a plain warning that says what Heimdall does and what the user can add, and
// the estimate counts six readiness runs per task.
func TestRowsInventoryNoWrongAnswerWarning(t *testing.T) {
	env := testEnv(t)
	data := "question,answer\nWhat is the hotel cap per night?,250\nHow many vacation days do new staff get?,20\nWhich form files an expense?,EXP-7\n"
	inv, err := Inspect(context.Background(), env, Input{Data: data, Name: "faq.csv"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Estimate.Tasks != 3 || inv.Estimate.ReadinessRuns != 18 || inv.Estimate.JudgeCalls != 0 {
		t.Fatalf("estimate = %+v, want 3 tasks, 18 readiness runs, no judge calls", inv.Estimate)
	}
	want := "3 of 3 rows have no wrong answer. Heimdall will make one for the readiness check by changing the right answer slightly. A real wrong answer from your data (a wrong_answers column) tests the grader better."
	found := false
	for _, w := range inv.Warnings {
		found = found || w == want
		if strings.Contains(w, "near miss") || strings.Contains(w, "fixed rule") {
			t.Errorf("warning still uses internal words: %q", w)
		}
	}
	if !found {
		t.Fatalf("warnings = %q, want %q", inv.Warnings, want)
	}
}
