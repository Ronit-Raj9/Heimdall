package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// skillsIndex is a recorded Codex run of arm B on task t01 with the uploaded
// skill bfcl-check installed (two files in the manifest) when withSkill, and
// the given steps.jsonl.
func skillsIndex(withSkill bool, steps string) *fakeIndex {
	ix := debugIndex(true, schemas.ConditionClean, "")
	m := ix.manifests[testExp]
	files := map[string]string{"t01:/workspace/NOTES.md": "aa"}
	if withSkill {
		files["t01:/home/agent/.codex/skills/bfcl-check/SKILL.md"] = "s1"
		files["t01:/home/agent/.codex/skills/bfcl-check/scripts/check.py"] = "s2"
		files["t02:/home/agent/.codex/skills/other/SKILL.md"] = "s3" // another task: not this run's
	}
	m.Arms = map[string]schemas.ArmManifest{"B": {Label: "v2", Config: schemas.AgentConfig{Adapter: "command", Parser: "codex"},
		Files: map[string]map[string]string{schemas.ConditionClean: files}}}
	ix.files["r_1"][schemas.StepsFile] = []byte(steps)
	return ix
}

func getRunSkills(t *testing.T, ix *fakeIndex) *schemas.RunSkills {
	t.Helper()
	r := router.New()
	NewRunsHandler(testMode, ix).RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(APIPrefix + "/runs/r_1")
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	r.Handler(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("GET run: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var got schemas.RunDetail
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Skills
}

const stepsReadSkill = `{"seq":1,"kind":"model_turn","source":"gateway","summary":"turn 1"}
{"seq":4,"kind":"tool_call","source":"agent_stream","tool_name":"exec_command","summary":"bash -lc 'cat ~/.codex/skills/bfcl-check/SKILL.md'"}
{"seq":7,"kind":"tool_call","source":"agent_stream","tool_name":"exec_command","summary":"python /home/agent/.codex/skills/bfcl-check/scripts/check.py"}
`

const stepsNoRead = `{"seq":1,"kind":"model_turn","source":"gateway","summary":"turn 1"}
{"seq":2,"kind":"tool_call","source":"agent_stream","tool_name":"exec_command","summary":"ls ~/.codex/skills/bfcl-check"}
{"seq":3,"kind":"tool_call","source":"agent_stream","tool_name":"exec_command","summary":"cat /workspace/README.md"}
`

func TestRunDetailSkillRead(t *testing.T) {
	s := getRunSkills(t, skillsIndex(true, stepsReadSkill))
	if s == nil || len(s.Installed) != 1 {
		t.Fatalf("skills = %+v", s)
	}
	k := s.Installed[0]
	if k.Name != "bfcl-check" || k.Files != 2 || !k.Read || k.ReadStep != 4 || k.ReadPath != "bfcl-check/SKILL.md" {
		t.Fatalf("skill = %+v, want bfcl-check, 2 files, read at step 4", k)
	}
}

func TestRunDetailSkillNotRead(t *testing.T) {
	s := getRunSkills(t, skillsIndex(true, stepsNoRead))
	if s == nil || len(s.Installed) != 1 {
		t.Fatalf("skills = %+v", s)
	}
	if k := s.Installed[0]; k.Read || k.ReadStep != 0 {
		t.Fatalf("a listing of the folder is no read: %+v", k)
	}
}

func TestRunDetailNoSkills(t *testing.T) {
	s := getRunSkills(t, skillsIndex(false, stepsNoRead))
	if s == nil || s.Installed == nil || len(s.Installed) != 0 {
		t.Fatalf("skills = %+v, want an empty list", s)
	}
}

func TestSkillToolStepNamesSkill(t *testing.T) {
	steps := []schemas.StepRecord{{Seq: 3, ToolName: "Skill", Summary: `{"skill":"bfcl-check-old"}`}, {Seq: 5, ToolName: "Skill", Summary: `{"skill":"bfcl-check"}`}}
	if seq, _, ok := firstSkillRead("bfcl-check", nil, steps); !ok || seq != 5 {
		t.Fatalf("seq=%d ok=%v, want step 5", seq, ok)
	}
}
