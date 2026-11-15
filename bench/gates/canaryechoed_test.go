package gates

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	ownCanary     = "0f6c1a2b-3c4d-4e5f-8a9b-0c1d2e3f4a5b"
	foreignCanary = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	strayCanary   = "12345678-1234-4234-8234-123456789abc"
)

// canaryRC builds a benchmarks folder with the run's suite "own" and a
// sibling suite "other", each with a canary, and a run folder. stamped puts
// the own canary line into the task instruction.
func canaryRC(t *testing.T, stamped bool) *schemas.RunContext {
	t.Helper()
	root := t.TempDir()
	instr := "Fix the parser.\n"
	if stamped {
		instr += "\n" + utils.CanaryLine("instruction.md", ownCanary) + "\n"
	}
	files := map[string]string{
		"own/suite.yaml":                "id: own\ncanary: " + ownCanary + "\n",
		"own/tasks/t01/instruction.md":  instr,
		"other/suite.yaml":              "id: other\ncanary: " + foreignCanary + "\n",
		"plain/suite.yaml":              "id: plain\n",
		"run/artifacts/report/out.html": "<p>report</p>\n",
	}
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &schemas.RunContext{
		Suite: &schemas.Suite{ID: "own", Root: filepath.Join(root, "own"), Canary: ownCanary},
		Task:  &schemas.Task{ID: "t01", Dir: filepath.Join(root, "own", "tasks", "t01"), Instruction: "instruction.md"},
		Dir:   filepath.Join(root, "run"),
	}
}

// writeArtifact writes an exported artifact into the run folder and returns
// its evidence record.
func writeArtifact(t *testing.T, rc *schemas.RunContext, rel, data string) schemas.ArtifactFile {
	t.Helper()
	p := filepath.Join(rc.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return schemas.ArtifactFile{ID: "report", Name: filepath.Base(rel), Path: rel, Source: filepath.Base(rel)}
}

func TestCanaryEchoed(t *testing.T) {
	answer := func(s string) *schemas.Answer { return &schemas.Answer{Text: s, Source: "file"} }
	cases := []struct {
		name      string
		stamped   bool
		noTask    bool
		ev        func(t *testing.T, rc *schemas.RunContext) *schemas.Evidence
		fired     bool
		evidence  []string
		detailHas string
	}{
		{
			name: "none",
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{Answer: answer("The launch is on 3 March.")}
			},
			fired: false, detailHas: "proves nothing",
		},
		{
			name: "foreign canary in the answer",
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{Answer: answer("Sure. " + strings.ToUpper(foreignCanary))}
			},
			fired: true, evidence: []string{"answer:" + foreignCanary}, detailHas: "suite other",
		},
		{
			name: "foreign canary added in the diff",
			ev: func(t *testing.T, _ *schemas.RunContext) *schemas.Evidence {
				text := section("notes.md", hunkAt(1, 1, " # Notes", "+<!-- "+utils.CanaryText(foreignCanary)+" -->"))
				return &schemas.Evidence{Diff: plantedDiff(t, text, schemas.DiffFile{Path: "notes.md", Status: "modified"})}
			},
			fired: true, evidence: []string{"diff:notes.md:" + foreignCanary},
		},
		{
			name: "foreign canary only in diff context is the repository's",
			ev: func(t *testing.T, _ *schemas.RunContext) *schemas.Evidence {
				text := section("notes.md", hunkAt(1, 1, " "+foreignCanary, "+fixed"))
				return &schemas.Evidence{Diff: plantedDiff(t, text, schemas.DiffFile{Path: "notes.md", Status: "modified"})}
			},
			fired: false,
		},
		{
			name: "unknown canary line in an artifact",
			ev: func(t *testing.T, rc *schemas.RunContext) *schemas.Evidence {
				a := writeArtifact(t, rc, "artifacts/report/out.html", "<!-- "+utils.CanaryText(strayCanary)+" -->\n<p>hi</p>")
				return &schemas.Evidence{Artifacts: []schemas.ArtifactFile{a}}
			},
			fired: true, evidence: []string{"artifact:artifacts/report/out.html:" + strayCanary}, detailHas: "an unknown suite",
		},
		{
			name: "foreign canary in tool call arguments and a tool call event",
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{
					ToolCalls: []schemas.ToolCall{{Seq: 3, Server: "notes", Tool: "write", Arguments: `{"text":"` + foreignCanary + `"}`}},
					Events:    []schemas.Event{toolCall("r:agent_stream:7", "Bash", "echo "+foreignCanary)},
				}
			},
			fired: true, evidence: []string{"tool_call:3:" + foreignCanary, "event:r:agent_stream:7:" + foreignCanary},
		},
		{
			name: "own canary not in the prompt",
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{Claim: &schemas.Claim{Made: true, Text: "done " + ownCanary}}
			},
			fired: true, evidence: []string{"final_message:" + ownCanary}, detailHas: "was not in the prompt",
		},
		{
			name:    "own canary in the stamped prompt is not evidence",
			stamped: true,
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{Answer: answer("Task said: " + utils.CanaryText(ownCanary))}
			},
			fired: false, detailHas: "not evidence",
		},
		{
			name:    "foreign canary still fires with the own canary in the prompt",
			stamped: true,
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{Answer: answer(ownCanary + " and " + foreignCanary)}
			},
			fired: true, evidence: []string{"answer:" + foreignCanary}, detailHas: "not evidence",
		},
		{
			name:   "own canary with an unreadable prompt is not judged",
			noTask: true,
			ev: func(*testing.T, *schemas.RunContext) *schemas.Evidence {
				return &schemas.Evidence{Answer: answer(ownCanary)}
			},
			fired: false, detailHas: "could not be read",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rc := canaryRC(t, c.stamped)
			if c.noTask {
				rc.Task = nil
			}
			res, err := newCanaryEchoedGate().Check(context.Background(), rc, c.ev(t, rc))
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired {
				t.Fatalf("fired %v, want %v: %s", res.Fired, c.fired, res.Detail)
			}
			if !reflect.DeepEqual(res.Evidence, c.evidence) {
				t.Errorf("evidence %v, want %v", res.Evidence, c.evidence)
			}
			if c.detailHas != "" && !strings.Contains(res.Detail, c.detailHas) {
				t.Errorf("detail %q lacks %q", res.Detail, c.detailHas)
			}
			if !strings.Contains(res.Detail, "verbatim") {
				t.Errorf("detail does not state the verbatim-only limit: %q", res.Detail)
			}
			if res.Gate != CanaryEchoed || res.Effect != schemas.GateReport {
				t.Errorf("identity %s %s", res.Gate, res.Effect)
			}
		})
	}
}

func TestCanaryEchoedEdges(t *testing.T) {
	g := newCanaryEchoedGate()
	if g.Baseline() || g.Effect() != schemas.GateReport {
		t.Fatal("canary_echoed must be report only and opt-in")
	}
	res, err := g.Check(context.Background(), nil, nil)
	if err != nil || res.Fired {
		t.Fatalf("nil run context: %+v %v", res, err)
	}
	rc := canaryRC(t, false)
	rc.Suite.Canary = ""
	res, err = g.Check(context.Background(), rc, &schemas.Evidence{Answer: &schemas.Answer{Text: ownCanary}})
	if err != nil || !res.Fired {
		t.Fatalf("own canary read from suite.yaml when the loaded suite has none: %+v %v", res, err)
	}
	if _, err := g.Check(context.Background(), rc, &schemas.Evidence{Diff: &schemas.Diff{Files: []schemas.DiffFile{{Path: "a", Status: "added"}}}}); err == nil {
		t.Error("diff with files and no text: no error")
	}
	rc = canaryRC(t, false)
	rc.Suite.Root = ""
	rc.Suite.Canary = ""
	res, err = g.Check(context.Background(), rc, &schemas.Evidence{Answer: &schemas.Answer{Text: utils.CanaryText(strayCanary)}})
	if err != nil || !res.Fired || !strings.Contains(res.Detail, "no suite canary is known") {
		t.Fatalf("no known canaries still catches the canary line: %+v %v", res, err)
	}
}
