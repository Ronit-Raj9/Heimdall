package msibench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

func TestImportFixture(t *testing.T) {
	out := t.TempDir()
	im := &importer{}
	res, err := im.Import(context.Background(), importers.Options{From: "testdata/release", BenchmarksDir: out, SuiteID: "msi-bench-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 1 || len(res.Skipped) != 0 {
		t.Fatalf("tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	dir := filepath.Join(res.SuiteDir, "tasks", res.Tasks[0])
	for _, f := range []string{"task.yaml", "user.json", "audio/00.wav", "audio/01.wav", "verifier/answer.yaml", "solution/answer.txt", "controls/exploit.sh", "instruction.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	var u struct {
		Script struct {
			Opening string `json:"opening"`
			Replies []struct {
				Say  string `json:"say"`
				Stop bool   `json:"stop"`
			} `json:"replies"`
		} `json:"script"`
		Audio map[string]string `json:"audio"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "user.json"))
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatal(err)
	}
	if u.Audio[u.Script.Opening] != "/sim/00.wav" || len(u.Script.Replies) != 2 || u.Audio[u.Script.Replies[0].Say] != "/sim/01.wav" || !u.Script.Replies[1].Stop {
		t.Fatalf("block script wrong: %+v", u)
	}
	ans, _ := os.ReadFile(filepath.Join(dir, "verifier/answer.yaml"))
	if !strings.Contains(string(ans), "prediction.tool_calls") || !strings.Contains(string(ans), "wrong_answers") {
		t.Fatalf("answer.yaml lacks rubric or wrong answer:\n%s", ans)
	}
	ref, _ := os.ReadFile(filepath.Join(dir, "solution/answer.txt"))
	if !strings.Contains(string(ref), "Tool calls: [{") {
		t.Fatalf("reference lacks gold calls: %s", ref)
	}
	suite, _ := os.ReadFile(filepath.Join(res.SuiteDir, "suite.yaml"))
	for _, want := range []string{"grading: judged", "non-commercial evaluation use", "track: voice", "msi2-en-disc-domestic-05"} {
		if !strings.Contains(string(suite), want) {
			t.Fatalf("suite.yaml lacks %q", want)
		}
	}
}
