package agentdiff

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// groupOf returns one group of a config diff, or an empty one.
func groupOf(c ConfigDiff, id string) ConfigGroup {
	for _, g := range c.Groups {
		if g.ID == id {
			return g
		}
	}
	return ConfigGroup{}
}

// fieldOf returns one field change by path.
func fieldOf(t *testing.T, c ConfigDiff, path string) FieldChange {
	t.Helper()
	for _, g := range c.Groups {
		for _, f := range g.Fields {
			if f.Path == path {
				return f
			}
		}
	}
	t.Fatalf("no change at %s in %+v", path, c.Groups)
	return FieldChange{}
}

func TestDiffConfigsFields(t *testing.T) {
	a := botArm(hashA, "v1", "m1", "p\n")
	b := botArm(hashB, "v2", "m2", "p\n")
	b.Config.Argv = []string{"bot", "--token=abc123"}
	b.Config.Budgets.Turns = 30
	b.Config.Skills = &schemas.AgentSkills{Extra: []string{"skills/lint"}}
	b.Config.Gateway.Fallbacks = []string{"anthropic"}
	b.ImageID = "sha256:feed"
	b.Identity = &schemas.CanonicalArmIdentity{AdapterVersion: "1.1.0", Budgets: schemas.Budgets{WallS: 1}}
	a.Config.Notes.ProjectFile = "AGENTS.md"
	c, err := DiffConfigs(a, b, schemas.ConditionClean, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, kind, group string
		old, new          any
		redacted          bool
	}{
		{"model", KindChanged, GroupModel, "m1", "m2", false},
		{"env.BOT_MODEL", KindChanged, GroupModel, "m1", "m2", false},
		{"env.OPENAI_API_KEY", KindChanged, GroupOther, redacted, redacted, true},
		{"argv", KindAdded, GroupOther, nil, []any{"bot", "--token=[redacted]"}, true},
		{"budgets.turns", KindAdded, GroupBudgets, nil, float64(30), false},
		{"skills.extra", KindAdded, GroupTools, nil, []any{"skills/lint"}, false},
		{"gateway.fallbacks", KindAdded, GroupGateway, nil, []any{"anthropic"}, false},
		{"image_id", KindAdded, GroupImage, nil, "sha256:feed", false},
		{"identity.adapter_version", KindAdded, GroupOther, nil, "1.1.0", false},
		{"notes.project_file", KindRemoved, GroupPrompt, "AGENTS.md", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			f := fieldOf(t, c, tc.path)
			if f.Kind != tc.kind || fieldGroup(tc.path) != tc.group || f.Redacted != tc.redacted {
				t.Fatalf("got %+v group %s", f, fieldGroup(tc.path))
			}
			if !equalJSON(f.Old, tc.old) || !equalJSON(f.New, tc.new) {
				t.Fatalf("values %#v %#v, want %#v %#v", f.Old, f.New, tc.old, tc.new)
			}
		})
	}
	for _, g := range c.Groups {
		for _, f := range g.Fields {
			if strings.HasPrefix(f.Path, "identity.budgets") || strings.HasPrefix(f.Path, "files.") {
				t.Errorf("unexpected path %s", f.Path)
			}
		}
	}
}

func TestRedactLeaf(t *testing.T) {
	got, hid := redactLeaf([]any{"ok", map[string]any{"cmd": "run --api_key=s3cret"}})
	if !hid || !equalJSON(got, []any{"ok", map[string]any{"cmd": "run --api_key=[redacted]"}}) {
		t.Fatalf("got %#v %v", got, hid)
	}
	if _, hid := redactLeaf(float64(3)); hid {
		t.Fatal("a number hides nothing")
	}
}

func TestDiffConfigsIdentical(t *testing.T) {
	a := botArm(hashA, "v1", "m1", "p\n")
	b := botArm(hashA, "v1", "m1", "p\n")
	b.Config.Env = map[string]string{"OPENAI_API_KEY": "sk-v1", "BOT_MODEL": "m1", "EMPTY": ""}
	c, err := DiffConfigs(a, b, schemas.ConditionClean, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Identical || c.Changes != 0 || len(c.Groups) != 0 {
		t.Fatalf("want identical, got %+v", c)
	}
}

func TestDiffConfigsFiles(t *testing.T) {
	notesV1, notesV2 := "Use pytest.\n", "Use pytest -x.\n"
	a := botArm(hashA, "v1", "m", "Be careful.\nRun the tests.\n")
	b := botArm(hashB, "v2", "m", "Be careful.\nRun every test.\napi_key=hunter2\n")
	a.Config.Notes = schemas.AgentNotes{UserFile: "notes/v1.md", UserPath: "/home/agent/NOTES.md"}
	b.Config.Notes = schemas.AgentNotes{UserFile: "notes/v2.md", UserPath: "/home/agent/NOTES.md"}
	a.Files["clean"]["/home/agent/NOTES.md"] = utils.SHA256Bytes([]byte(notesV1))
	b.Files["clean"]["/home/agent/NOTES.md"] = utils.SHA256Bytes([]byte(notesV2))
	b.Files["clean"]["t9:/workspace/AGENTS.md"] = "only-b-task"
	b.Files["clean"]["/home/agent/tool.bin"] = "binhash"
	disk := map[string]string{"notes/v1.md": notesV1, "notes/v2.md": notesV2}
	contents := func(p string) ([]byte, bool) {
		s, ok := disk[p]
		return []byte(s), ok
	}
	c, err := DiffConfigs(a, b, schemas.ConditionClean, contents)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]FileChange{}
	for _, g := range c.Groups {
		for _, f := range g.Files {
			files[f.Path] = f
		}
	}
	prompt := files["/home/agent/prompt.md"]
	if prompt.Kind != KindChanged || !strings.Contains(prompt.Diff, "-Run the tests.\n+Run every test.\n") || strings.Contains(prompt.Diff, "hunter2") {
		t.Fatalf("prompt diff %+v", prompt)
	}
	notes := files["/home/agent/NOTES.md"]
	if !strings.Contains(notes.Diff, "+Use pytest -x.") {
		t.Fatalf("notes diff %+v", notes)
	}
	if _, ok := files["t9:/workspace/AGENTS.md"]; ok {
		t.Fatal("a task only one side lists is not a config change")
	}
	if bin := files["/home/agent/tool.bin"]; bin.Kind != KindAdded || bin.Diff != "" || !strings.Contains(bin.DiffNote, "not on disk") {
		t.Fatalf("added file %+v", bin)
	}
	if len(groupOf(c, GroupPrompt).Files) != 2 || len(groupOf(c, GroupOther).Files) != 1 {
		t.Fatalf("file groups %+v", c.Groups)
	}

	t.Run("notes changed on disk since the plan", func(t *testing.T) {
		disk["notes/v2.md"] = "edited later\n"
		c, err := DiffConfigs(a, b, schemas.ConditionClean, contents)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range groupOf(c, GroupPrompt).Files {
			if f.Path == "/home/agent/NOTES.md" && (f.Diff != "" || !strings.Contains(f.DiffNote, "changed since the plan")) {
				t.Fatalf("notes %+v", f)
			}
		}
	})
}

func TestFileGroup(t *testing.T) {
	cfg := schemas.AgentConfig{MCP: &schemas.AgentMCP{ConfigPath: "/home/agent/.mcp.json"}, Skills: &schemas.AgentSkills{Dir: "/home/agent/sk"}}
	cases := map[string]string{
		"/home/agent/.mcp.json":            GroupTools,
		"t1:/home/agent/sk/lint/SKILL.md":  GroupTools,
		"t1:/workspace/AGENTS.md":          GroupPrompt,
		"/home/agent/system-prompt.txt":    GroupPrompt,
		"/home/agent/.codex/config.toml":   GroupOther,
		"t2:/home/agent/.claude/skills/x/": GroupTools,
	}
	for key, want := range cases {
		if got := fileGroup(key, cfg, schemas.AgentConfig{}); got != want {
			t.Errorf("%s: got %s want %s", key, got, want)
		}
	}
}

// equalJSON compares two values by their JSON text.
func equalJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(x) == string(y)
}
