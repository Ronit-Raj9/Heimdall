package research_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/research"
)

// TestExampleVerifiers runs each acme-research task's verifier (verify.py,
// with no docker) on its controls: the reference answer and sources pass,
// also with the sources as a Sources section of the answer; every wrong
// answer of research.json, the mutant, the exploit (right answer, invented
// citations) and the empty control fail. Admission repeats this through
// the real pipeline; this test catches a broken spec before it gets there.
func TestExampleVerifiers(t *testing.T) {
	py := python(t)
	tasks, err := filepath.Glob("../examples/acme-research/benchmarks/acme-research/tasks/*")
	if err != nil || len(tasks) != 8 {
		t.Fatalf("tasks: %v %v", tasks, err)
	}
	planRE := regexp.MustCompile(`(?s)PLAN = json\.loads\(r'''(.*?)'''\)`)
	for _, dir := range tasks {
		dir, _ = filepath.Abs(dir)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			ref, err := os.ReadFile(filepath.Join(dir, "solution", "reference.sh"))
			if err != nil {
				t.Fatal(err)
			}
			var plan struct {
				Answer  string   `json:"answer"`
				Sources []string `json:"sources"`
			}
			if err := json.Unmarshal(planRE.FindSubmatch(ref)[1], &plan); err != nil {
				t.Fatal(err)
			}
			spec, err := os.ReadFile(filepath.Join(dir, "verifier", "tests", research.SpecFile))
			if err != nil {
				t.Fatal(err)
			}
			var s research.Spec
			if err := json.Unmarshal(spec, &s); err != nil {
				t.Fatal(err)
			}
			srcJSON, _ := json.Marshal(plan.Sources)
			grade := func(t *testing.T, files map[string]string) (int, map[string]int) {
				t.Helper()
				root := t.TempDir()
				for p, body := range files {
					f := filepath.Join(root, "artifacts", p)
					if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				cmd := exec.Command(py, "-I", filepath.Join(dir, "verifier", "tests", "verify.py"))
				cmd.Env = []string{"GRADER_ARTIFACTS=" + filepath.Join(root, "artifacts"), "GRADER_OUT=" + filepath.Join(root, "out")}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("verify.py: %v: %s", err, out)
				}
				b, err := os.ReadFile(filepath.Join(root, "out", "reward.json"))
				if err != nil {
					t.Fatal(err)
				}
				var r struct {
					Reward     int            `json:"reward"`
					Dimensions map[string]int `json:"dimensions"`
				}
				if err := json.Unmarshal(b, &r); err != nil {
					t.Fatal(err)
				}
				return r.Reward, r.Dimensions
			}
			// run executes a control script in a scratch workspace and
			// returns what it left as artifacts.
			run := func(t *testing.T, script string) map[string]string {
				t.Helper()
				ws := t.TempDir()
				cmd := exec.Command("sh", filepath.Join(dir, script))
				cmd.Dir = ws
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v: %s", script, err, out)
				}
				files := map[string]string{}
				for id, name := range map[string]string{research.AnswerArtifact: research.AnswerFile, research.SourcesArtifact: research.SourcesFile} {
					if b, err := os.ReadFile(filepath.Join(ws, name)); err == nil {
						files[id+"/"+name] = string(b)
					}
				}
				return files
			}
			cases := []struct {
				name  string
				files func(t *testing.T) map[string]string
				want  int
			}{
				{name: "reference", want: 1, files: func(*testing.T) map[string]string {
					return map[string]string{"answer/answer.txt": plan.Answer + "\n", "sources/sources.json": string(srcJSON)}
				}},
				{name: "reference with a Sources section", want: 1, files: func(*testing.T) map[string]string {
					return map[string]string{"answer/answer.txt": plan.Answer + "\n\nSources:\n- " + strings.Join(plan.Sources, "\n- ") + "\n"}
				}},
				{name: "empty", want: 0, files: func(t *testing.T) map[string]string { return run(t, "controls/empty.sh") }},
				{name: "exploit: right answer, invented citations", want: 0, files: func(t *testing.T) map[string]string { return run(t, "controls/exploit.sh") }},
			}
			mutants, _ := filepath.Glob(filepath.Join(dir, "controls", "mutant-*.sh"))
			for _, m := range mutants {
				rel, _ := filepath.Rel(dir, m)
				cases = append(cases, struct {
					name  string
					files func(t *testing.T) map[string]string
					want  int
				}{name: rel, want: 0, files: func(t *testing.T) map[string]string { return run(t, rel) }})
			}
			for _, w := range s.WrongAnswers {
				cases = append(cases, struct {
					name  string
					files func(t *testing.T) map[string]string
					want  int
				}{name: "wrong answer " + w, want: 0, files: func(*testing.T) map[string]string {
					return map[string]string{"answer/answer.txt": w + "\n", "sources/sources.json": string(srcJSON)}
				}})
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					got, dims := grade(t, c.files(t))
					if got != c.want {
						t.Errorf("reward = %d, want %d (%v)", got, c.want, dims)
					}
				})
			}
		})
	}
}
