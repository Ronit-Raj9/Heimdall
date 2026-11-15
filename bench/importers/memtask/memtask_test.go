package memtask

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

func TestSplit(t *testing.T) {
	ts := func(n int) []Transcript {
		out := make([]Transcript, n)
		for i := range out {
			out[i] = Transcript{Date: string(rune('a' + i))}
		}
		return out
	}
	sizes := func(groups [][]Transcript) []int {
		var out []int
		for _, g := range groups {
			out = append(out, len(g))
		}
		return out
	}
	tests := []struct {
		name string
		n, k int
		want []int
	}{
		{"even", 6, 3, []int{2, 2, 2}},
		{"uneven larger first", 7, 3, []int{3, 2, 2}},
		{"fewer transcripts than sessions", 2, 3, []int{1, 1}},
		{"one session", 5, 1, []int{5}},
		{"none", 0, 3, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := ts(tt.n)
			got := Split(in, tt.k)
			if !reflect.DeepEqual(sizes(got), tt.want) {
				t.Fatalf("sizes %v, want %v", sizes(got), tt.want)
			}
			var flat []Transcript
			for _, g := range got {
				flat = append(flat, g...)
			}
			if len(in) > 0 && !reflect.DeepEqual(flat, in) {
				t.Fatal("split changed the order")
			}
		})
	}
}

func TestTaskID(t *testing.T) {
	tests := map[string]string{"gpt4_2655b836": "gpt4-2655b836", "e47becba_abs": "e47becba-abs", "Conv-26-Q001": "conv-26-q001", "__x__": "x", "a b/c": "a-b-c"}
	for in, want := range tests {
		if got := TaskID(in); got != want {
			t.Errorf("TaskID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseOptions(t *testing.T) {
	o, rest, err := ParseOptions(map[string]string{"ingest_sessions": "5", "grading": "match", "version": "2.0.1", "revision": "abc", "variant": "s"})
	if err != nil {
		t.Fatal(err)
	}
	if o != (Options{IngestSessions: 5, Grading: GradingMatch, Version: "2.0.1", Revision: "abc"}) || !reflect.DeepEqual(rest, map[string]string{"variant": "s"}) {
		t.Fatalf("options %+v rest %v", o, rest)
	}
	d, _, err := ParseOptions(nil)
	if err != nil || d.IngestSessions != 3 || d.Grading != GradingJudged || d.Version != "1.0.0" {
		t.Fatalf("defaults %+v %v", d, err)
	}
	for _, bad := range []map[string]string{{"ingest_sessions": "21"}, {"ingest_sessions": "x"}, {"grading": "llm"}, {"version": "v1"}} {
		if _, _, err := ParseOptions(bad); err == nil {
			t.Errorf("ParseOptions(%v) accepted", bad)
		}
	}
	if err := RefuseUnknown(map[string]string{"x": "1"}, "a"); err == nil || !strings.Contains(err.Error(), "-set x") {
		t.Fatalf("RefuseUnknown: %v", err)
	}
}

func TestHistoryFiles(t *testing.T) {
	files := HistoryFiles([]Transcript{{Date: "d1", Turns: []Turn{{Speaker: "User", Text: " hi \n"}}}, {Date: "d2"}}, 4, 9)
	got := files["history/conversation-004.md"]
	if got != "# Conversation 4 of 9\n\nDate: d1\n\n**User:** hi\n\n" || files["history/conversation-005.md"] == "" || len(files) != 2 {
		t.Fatalf("files %q", files)
	}
	if ApproxTokens("abcdefgh") != 2 || ApproxTokens("abc") != 1 {
		t.Fatal("ApproxTokens")
	}
	if Count(1, "transcript") != "1 transcript" || Count(3, "transcript") != "3 transcripts" {
		t.Fatal("Count")
	}
}

func sampleTask(id string) Task {
	return Task{
		ID: id, Title: "t", Project: "p", Tags: []string{"x"},
		Ingest:   []Session{{Instruction: "read\n", Files: map[string]string{"history/conversation-001.md": "c"}}},
		Question: "ask\n", Reference: "ref", Exploit: ExploitAnswer, AgentS: 60,
		Answer:     schemas.AnswerSpec{Question: "q", Expected: []string{"ref"}, Category: "c", WrongOnes: []string{"w1", "w2"}},
		Provenance: schemas.TaskProvenance{Source: "s", License: "MIT", ImportedFrom: "b#1"},
	}
}

func TestWriteTask(t *testing.T) {
	suite := t.TempDir()
	if err := WriteTask(suite, sampleTask("q1"), false); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(suite, "tasks", "q1")
	for _, f := range []string{"task.yaml", "sessions/s1.md", "sessions/s1-files/history/conversation-001.md", "sessions/question.md", "repo/README.md",
		"verifier/answer.yaml", "solution/answer.txt", "controls/empty.txt", "controls/exploit.sh"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "task.yaml"))
	for _, bad := range []string{"base_commit", "notes:", "match:"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("task.yaml carries empty field %s:\n%s", bad, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "controls", "wrong-1.txt")); err == nil {
		t.Error("wrong answers written as mutant files too; admission would grade each twice")
	}
	ans, _ := os.ReadFile(filepath.Join(dir, "verifier", "answer.yaml"))
	if strings.Contains(string(ans), "match:") || !strings.Contains(string(ans), "wrong_answers:") {
		t.Fatalf("answer.yaml:\n%s", ans)
	}
	if err := WriteTask(suite, sampleTask("q1"), false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("rewrite without force: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteTask(suite, sampleTask("q1"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); err == nil {
		t.Fatal("force kept a stale file")
	}
}

func TestWriteSuite(t *testing.T) {
	dir := t.TempDir()
	rec := schemas.ImportRecord{Importer: "lme", Benchmark: "b", Source: "s", License: "MIT", At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	spec := SuiteSpec{ID: "m", Version: "1.0.0", Grading: GradingJudged, Judge: PlaceholderJudge("qa-v1"), Imported: rec}
	if err := WriteSuite(dir, spec, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	// The user pins the judge; a second import keeps it and appends tasks.
	raw, _ := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	pinned := strings.Replace(strings.Replace(string(raw), "provider: pin-me", "provider: openai", 1), "model: pin-me", "model: gpt-x", 1)
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteSuite(dir, spec, []string{"b", "c"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tasks    []string
		Probes   []string
		Judge    schemas.JudgeSpec
		Imported map[string]string
		Verifier struct{ Command []string }
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Tasks, []string{"a", "b", "c"}) || got.Probes == nil || got.Judge.Provider != "openai" || got.Judge.Model != "gpt-x" {
		t.Fatalf("suite %+v", got)
	}
	if got.Imported["at"] != "2026-01-02T03:04:05Z" || strings.Contains(string(raw), "max_tokens") {
		t.Fatalf("suite.yaml:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "base", "Dockerfile")); err != nil {
		t.Fatal("no base/Dockerfile")
	}
	spec.Grading = GradingMatch
	if err := WriteSuite(dir, spec, []string{"d"}); err == nil {
		t.Fatal("extended a judged suite with a match import")
	}
	spec.Grading, spec.Imported.Importer = GradingJudged, "other"
	if err := WriteSuite(dir, spec, []string{"d"}); err == nil {
		t.Fatal("extended a suite written by another importer")
	}
}

func TestExploitScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "exploit.sh")
	if err := os.WriteFile(script, []byte(exploitScript("it's 'quoted' $HOME `x`")), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "answer.txt")
	cmd := exec.Command("sh", script)
	cmd.Env = append(os.Environ(), "BENCH_ANSWER_PATH="+out)
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("run exploit: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "it's 'quoted' $HOME `x`\n"; string(b) != want || string(stdout) != want {
		t.Fatalf("answer %q stdout %q, want %q", b, stdout, want)
	}
}
