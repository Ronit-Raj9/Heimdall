package evidence

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
)

var update = flag.Bool("update", false, "rewrite the golden digest")

// fakeRun is the part of a sealed run folder the builder reads.
type fakeRun struct {
	outcome     string
	success     float64
	instruction string
	events      []map[string]any
	stream      string
	diff        string
	stderr      string
	grade       map[string]any
	env         map[string]string
}

func writeRun(t *testing.T, dir string, r fakeRun) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	put := func(name string, v any) {
		var b []byte
		switch x := v.(type) {
		case string:
			b = []byte(x)
		default:
			var err error
			if b, err = json.Marshal(x); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("run.json", map[string]any{"outcome": r.outcome, "y": map[string]any{"state": "observed", "v": r.success}})
	g := r.grade
	if g == nil {
		g = map[string]any{"passed": r.success >= 1, "tests_ran": 4, "failures": 0}
	}
	put("grade.json", g)
	env := map[string]string{"BENCH_INSTRUCTION_FILE": "/tmp/bench/instruction.md"}
	for k, v := range r.env {
		env[k] = v
	}
	put("plan.json", map[string]any{"env": env, "files": map[string]string{"/tmp/bench/instruction.md": r.instruction}})
	var ev strings.Builder
	for _, e := range r.events {
		b, _ := json.Marshal(e)
		ev.Write(b)
		ev.WriteByte('\n')
	}
	put("events.jsonl", ev.String())
	put("agent.stream.jsonl", r.stream)
	put("workspace.diff", r.diff)
	put("agent.stderr.txt", r.stderr)
	put("cost.json", []map[string]any{{"tokens": map[string]int{"fresh": 100, "cache_read": 50, "out": 10}, "usd": map[string]any{"v": 0.01}}})
	put("metrics.json", map[string]any{"wall_time_s": map[string]any{"v": 12.5}})
}

func call(i int, tool, target string) map[string]any {
	return map[string]any{"type": "tool_call", "seq": i, "tool_name": tool, "target": target, "tool_call_id": fmt.Sprintf("c%d", i), "ts": fmt.Sprintf("2026-11-14T01:00:%02dZ", i)}
}

func result(i int, tool, summary string, isErr bool) map[string]any {
	return map[string]any{"type": "tool_result", "seq": i, "tool_name": tool, "summary": summary, "is_error": isErr, "tool_call_id": fmt.Sprintf("c%d", i-1), "ts": fmt.Sprintf("2026-11-14T01:00:%02dZ", i)}
}

func failingRun(instr string) fakeRun {
	return fakeRun{
		outcome: "fail", success: 0, instruction: instr,
		events: []map[string]any{
			call(1, "run_shell", "pytest tests/test_dates.py"),
			result(2, "run_shell", "exit 1: TypeError: unsupported operand at line 42", true),
			call(3, "edit", "src/dates.py"),
			result(4, "edit", "exit 0: ok", false),
		},
		diff: "--- a/src/dates.py\n+++ b/src/dates.py\n@@ -1 +1 @@\n-x\n+y\n",
	}
}

func passingRun(instr string) fakeRun {
	return fakeRun{outcome: "pass", success: 1, instruction: instr,
		events: []map[string]any{call(1, "edit", "src/a.py"), result(2, "edit", "exit 0: ok", false)}}
}

func build(t *testing.T, req improve.EvidenceRequest) *improve.EvidenceDoc {
	t.Helper()
	doc, err := Builder{}.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

// everything is the whole text the improver would see.
func everything(doc *improve.EvidenceDoc) string {
	var b strings.Builder
	b.Write(doc.EvidenceJSON)
	for _, k := range sortedKeys(doc.Files) {
		b.WriteString(k)
		b.Write(doc.Files[k])
	}
	return b.String()
}

func TestBuildRefusesProtectedTask(t *testing.T) {
	dir := t.TempDir()
	writeRun(t, filepath.Join(dir, "a"), failingRun("Fix it."))
	writeRun(t, filepath.Join(dir, "b"), failingRun("Fix it."))
	req := improve.EvidenceRequest{Round: 1, Runs: []improve.RunRef{
		{ExpID: "e1", RunID: "r1", Task: "dev-task-one", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "a")},
		{ExpID: "e1", RunID: "r2", Task: "audit-task-secret", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "b")},
	}, Prot: improve.Protected{TaskIDs: []string{"audit-task-secret"}}}
	doc, err := Builder{}.Build(context.Background(), req)
	if err == nil || doc != nil {
		t.Fatalf("a run of a protected task must fail the build, got doc=%v err=%v", doc != nil, err)
	}
	if strings.Contains(err.Error(), "audit-task-secret") {
		t.Fatalf("the error names the protected task: %v", err)
	}
}

func TestBuildRefusesRunWithoutFolder(t *testing.T) {
	_, err := Builder{}.Build(context.Background(), improve.EvidenceRequest{Runs: []improve.RunRef{{Task: "t1", Trial: 1}}})
	if err == nil {
		t.Fatal("a run without a folder must fail the build")
	}
}

func TestBuildDropsPlantedProviderKey(t *testing.T) {
	dir := t.TempDir()
	key := "sk-proj-ABCDEFGHIJKLMNOPQRSTUVWX1234"
	lit := "gateway-virtual-key-literal-value-7f3a"
	r := failingRun("Fix the parser. Your key is " + key)
	r.stderr = "Authorization: Bearer " + key + "\nOPENAI_API_KEY=" + key + "\n" + lit
	r.stream = `{"type":"tool_result","output":"token ` + key + ` and ` + lit + `"}` + "\n"
	r.env = map[string]string{"BENCH_API_KEY": lit}
	writeRun(t, filepath.Join(dir, "a"), r)
	doc := build(t, improve.EvidenceRequest{Round: 1, Runs: []improve.RunRef{{ExpID: "e1", Task: "t03-fix", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "a")}}})
	all := everything(doc)
	for _, s := range []string{key, lit} {
		if strings.Contains(all, s) {
			t.Fatalf("planted secret %q reached the evidence", s)
		}
	}
}

func TestBuildDropsRunWithAuditAnswer(t *testing.T) {
	dir := t.TempDir()
	answer := "the expected total for invoice nine is four hundred and twelve dollars exactly"
	leak := failingRun("Fix the totals.")
	leak.stream = `{"type":"step","text":"I think ` + answer + `"}` + "\n"
	writeRun(t, filepath.Join(dir, "leak"), leak)
	writeRun(t, filepath.Join(dir, "ok"), failingRun("Fix the dates."))
	doc := build(t, improve.EvidenceRequest{Round: 1, Prot: improve.Protected{Secrets: []string{"Expected total for invoice nine is four hundred and twelve dollars exactly."}},
		Runs: []improve.RunRef{
			{ExpID: "e1", Task: "t01-leaky", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "leak")},
			{ExpID: "e1", Task: "t02-clean", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "ok")},
		}})
	all := everything(doc)
	if strings.Contains(all, "four hundred and twelve") || strings.Contains(all, "t01-leaky") {
		t.Fatal("a run holding an audit answer must be dropped whole")
	}
	if !strings.Contains(all, "t02-clean") {
		t.Fatal("the clean run must stay")
	}
}

func TestBuildRemovesAuditTaskIDsAndCanaries(t *testing.T) {
	dir := t.TempDir()
	r := failingRun("Fix it; see also audit-zz-held-out and canary 4f1c2d3e-aaaa-bbbb-cccc-1234567890ab.")
	r.stream = `{"output":"cat ../audit-zz-held-out/notes"}` + "\n"
	writeRun(t, filepath.Join(dir, "a"), r)
	doc := build(t, improve.EvidenceRequest{Round: 1,
		Prot: improve.Protected{TaskIDs: []string{"audit-zz-held-out"}, Canaries: []string{"4f1c2d3e-aaaa-bbbb-cccc-1234567890ab"}},
		Runs: []improve.RunRef{{ExpID: "e1", Task: "t03-fix", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "a")}}})
	all := everything(doc)
	for _, s := range []string{"audit-zz-held-out", "4f1c2d3e-aaaa-bbbb-cccc-1234567890ab"} {
		if strings.Contains(all, s) {
			t.Fatalf("%q reached the evidence", s)
		}
	}
}

func TestBuildRemovesEvalCues(t *testing.T) {
	dir := t.TempDir()
	r := failingRun("Fix it.")
	r.stderr = "reading /opt/bench/verifier/tests and the hidden test via toolproxy, HEIMDALL grader, BENCH_MODEL"
	writeRun(t, filepath.Join(dir, "a"), r)
	doc := build(t, improve.EvidenceRequest{Round: 1, Runs: []improve.RunRef{{ExpID: "e1", Task: "t03-fix", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "a")}}})
	all := strings.ToLower(everything(doc))
	for _, cue := range []string{"bench_", "toolproxy", "/opt/bench", "heimdall", "grader", "verifier", "hidden test"} {
		if strings.Contains(all, cue) {
			t.Fatalf("cue %q reached the evidence", cue)
		}
	}
}

func TestBuildNeverShowsGraderDetails(t *testing.T) {
	dir := t.TempDir()
	r := failingRun("Fix it.")
	r.grade = map[string]any{"passed": false, "tests_ran": 7, "failures": 2, "failed_tests": []string{"tests.test_secret_case::test_expected_value_is_42"},
		"reason": "required tests failed: test_expected_value_is_42 expected 42 got 41", "dimensions": map[string]float64{"correctness": 0}}
	writeRun(t, filepath.Join(dir, "a"), r)
	doc := build(t, improve.EvidenceRequest{Round: 1, Runs: []improve.RunRef{{ExpID: "e1", Task: "t03-fix", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "a")}}})
	all := everything(doc)
	if strings.Contains(all, "test_expected_value_is_42") || strings.Contains(all, "expected 42") || strings.Contains(all, "test_secret_case") {
		t.Fatal("hidden test names or check feedback reached the evidence")
	}
	var ev evidenceFile
	if err := json.Unmarshal(doc.EvidenceJSON, &ev); err != nil {
		t.Fatal(err)
	}
	it := ev.Items[0]
	if it.TestsRan != 7 || it.TestsFailed != 2 {
		t.Fatalf("counts: ran %d failed %d", it.TestsRan, it.TestsFailed)
	}
}

func TestBuildNoisyTasksNotCountedInLost(t *testing.T) {
	dir := t.TempDir()
	writeRun(t, filepath.Join(dir, "n1"), failingRun("A."))
	writeRun(t, filepath.Join(dir, "n2"), passingRun("A."))
	writeRun(t, filepath.Join(dir, "f1"), failingRun("B."))
	doc := build(t, improve.EvidenceRequest{Round: 2, Runs: []improve.RunRef{
		{ExpID: "e1", Task: "t-noisy", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "n1")},
		{ExpID: "e1", Task: "t-noisy", Trial: 2, Arm: 1, Dir: filepath.Join(dir, "n2")},
		{ExpID: "e1", Task: "t-steady", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "f1")},
	}})
	var ev evidenceFile
	if err := json.Unmarshal(doc.EvidenceJSON, &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Split != "dev" || ev.Round != 2 {
		t.Fatalf("split %q round %d", ev.Split, ev.Round)
	}
	if len(ev.Clusters) != 1 || ev.Clusters[0].Lost != 1 {
		t.Fatalf("clusters: %+v", ev.Clusters)
	}
	for _, it := range ev.Items {
		if it.Task == "t-noisy" && !it.Noisy {
			t.Fatal("t-noisy must be marked noisy")
		}
	}
	if !strings.Contains(ev.Clusters[0].Key, "TypeError unsupported operand at line #") {
		t.Fatalf("cluster key %q", ev.Clusters[0].Key)
	}
}

func TestBuildFlipBetweenArms(t *testing.T) {
	dir := t.TempDir()
	writeRun(t, filepath.Join(dir, "a"), passingRun("A."))
	writeRun(t, filepath.Join(dir, "b"), failingRun("A."))
	doc := build(t, improve.EvidenceRequest{Round: 1, Runs: []improve.RunRef{
		{ExpID: "e1", Task: "t-one", Trial: 1, Arm: 0, Dir: filepath.Join(dir, "a")},
		{ExpID: "e1", Task: "t-one", Trial: 1, Arm: 1, Dir: filepath.Join(dir, "b")},
	}})
	var ev evidenceFile
	_ = json.Unmarshal(doc.EvidenceJSON, &ev)
	var flip string
	for _, it := range ev.Items {
		if it.Arm == "candidate" {
			flip = it.Flip
		}
	}
	if flip != "pass_to_fail" {
		t.Fatalf("flip %q", flip)
	}
	if _, ok := doc.Files["runs/t-one/1/B/digest.md"]; !ok {
		t.Fatalf("files: %v", sortedKeys(doc.Files))
	}
}

func TestBuildCapsRespected(t *testing.T) {
	dir := t.TempDir()
	r := failingRun("Fix it.")
	for i := 5; i < 400; i += 2 {
		r.events = append(r.events, call(i, "run_shell", strings.Repeat("echo long argument ", 30)), result(i+1, "run_shell", "exit 1: "+strings.Repeat("boom ", 600), true))
	}
	r.stream = strings.Repeat(`{"type":"step","text":"`+strings.Repeat("z", 1000)+`"}`+"\n", 50)
	var refs []improve.RunRef
	for i := 0; i < 6; i++ {
		d := filepath.Join(dir, fmt.Sprint(i))
		writeRun(t, d, r)
		refs = append(refs, improve.RunRef{ExpID: "e1", Task: fmt.Sprintf("t%02d-task", i), Trial: 1, Arm: 1, Dir: d})
	}
	lim := improve.EvidenceLimits{DigestTokensPerRun: 1000, DigestTokensTotal: 3000, RawBytesPerRun: 20000, RawBytesTotal: 50000, Clusters: 6, Exemplars: 2}
	doc := build(t, improve.EvidenceRequest{Round: 1, Runs: refs, Limits: lim})
	raw := 0
	for k, v := range doc.Files {
		switch {
		case strings.HasSuffix(k, "digest.md"):
			if len(v) > 1000*4 {
				t.Fatalf("%s is %d bytes", k, len(v))
			}
			if !strings.Contains(string(v), "(truncated)") {
				t.Fatalf("%s has no truncation marker", k)
			}
		case strings.HasSuffix(k, "transcript.jsonl"):
			if len(v) > 20000 {
				t.Fatalf("%s is %d bytes", k, len(v))
			}
			raw += len(v)
		}
	}
	if raw > 50000 {
		t.Fatalf("raw total %d", raw)
	}
	if len(doc.EvidenceJSON) > 3000*4 {
		t.Fatalf("evidence.json is %d bytes", len(doc.EvidenceJSON))
	}
	for _, it := range doc.Items {
		if len(it.Errors) > 0 && len(it.Errors[0]) > 2000+3 {
			t.Fatalf("error not clipped: %d", len(it.Errors[0]))
		}
	}
}

func TestBuildIsTheDefault(t *testing.T) {
	if _, ok := improve.DefaultEvidenceBuilder.(Builder); !ok {
		t.Fatalf("DefaultEvidenceBuilder is %T", improve.DefaultEvidenceBuilder)
	}
}

func TestGoldenDigestRecordedRun(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "recorded", "e_pilot_mixed_nc93", "slots", "s_2sv12hahmm", "runs", "r_gevv5gsi7b")
	doc := build(t, improve.EvidenceRequest{Round: 1, Runs: []improve.RunRef{{ExpID: "e_pilot_mixed_nc93", RunID: "r_gevv5gsi7b", Task: "fix-invoice-rounding", Trial: 1, Arm: 1, Dir: dir}}})
	got := doc.Files["runs/fix-invoice-rounding/1/B/digest.md"]
	if len(got) == 0 {
		t.Fatalf("no digest; files %v", sortedKeys(doc.Files))
	}
	if strings.Contains(string(got), "test_hidden_rounding") || strings.Contains(everything(doc), "test_hidden_rounding") {
		t.Fatal("hidden test names reached the evidence")
	}
	golden := filepath.Join("testdata", "digest_r_gevv5gsi7b.md")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("digest differs from %s; run with -update after checking", golden)
	}
}
