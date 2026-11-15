package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// writeTree writes files (relative path -> content) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const skillMD = "---\nname: %s\ndescription: test skill\n---\nBody.\n"

func boolPtr(b bool) *bool { return &b }

func TestResolveSkills(t *testing.T) {
	tests := []struct {
		name   string
		task   map[string]string // files under the task folder
		bench  map[string]string // files under the bench root
		skills string            // Task.Skills
		agent  *schemas.AgentSkills
		setup  func(t *testing.T, taskDir, benchRoot string)
		want   []string // skill names, or nil with wantErr
		errHas string
	}{
		{name: "task collection", skills: "skills",
			task: map[string]string{"skills/b/SKILL.md": "b", "skills/a/SKILL.md": "a", "skills/a/ref/x.txt": "x", "skills/README.md": "loose files are skipped", "skills/.git/HEAD": "hidden entries are skipped"},
			want: []string{"a", "b"}},
		{name: "task folder that is one skill", skills: "skills/only",
			task: map[string]string{"skills/only/SKILL.md": "o"}, want: []string{"only"}},
		{name: "extra skill and extra collection", agent: &schemas.AgentSkills{Extra: []string{"lib/one", "lib/many"}},
			bench: map[string]string{"lib/one/SKILL.md": "1", "lib/many/two/SKILL.md": "2", "lib/many/three/SKILL.md": "3"},
			want:  []string{"one", "three", "two"}},
		{name: "task and extra together", skills: "skills", agent: &schemas.AgentSkills{Extra: []string{"lib/one"}},
			task: map[string]string{"skills/a/SKILL.md": "a"}, bench: map[string]string{"lib/one/SKILL.md": "1"},
			want: []string{"a", "one"}},
		{name: "disabled installs none", skills: "skills", agent: &schemas.AgentSkills{Enabled: boolPtr(false), Extra: []string{"lib/one"}},
			task: map[string]string{"skills/a/SKILL.md": "a"}, bench: map[string]string{"lib/one/SKILL.md": "1"}},
		{name: "enabled explicitly", skills: "skills", agent: &schemas.AgentSkills{Enabled: boolPtr(true)},
			task: map[string]string{"skills/a/SKILL.md": "a"}, want: []string{"a"}},
		{name: "no skills anywhere"},
		{name: "duplicate name", skills: "skills", agent: &schemas.AgentSkills{Extra: []string{"lib/a"}},
			task: map[string]string{"skills/a/SKILL.md": "a"}, bench: map[string]string{"lib/a/SKILL.md": "a2"},
			errHas: `skill "a" comes from both`},
		{name: "subfolder without SKILL.md", skills: "skills",
			task: map[string]string{"skills/a/SKILL.md": "a", "skills/b/notes.md": "b"}, errHas: "skill folder b holds no SKILL.md"},
		{name: "empty collection", skills: "skills", task: map[string]string{"skills/README.md": "x"}, errHas: "no skill folders"},
		{name: "missing folder", skills: "nope", errHas: "task t01 skills"},
		{name: "task path climbs out", skills: "../escape", errHas: "leaves"},
		{name: "extra outside bench root", agent: &schemas.AgentSkills{Extra: []string{"/etc"}}, errHas: "leaves"},
		{name: "extra is a file", agent: &schemas.AgentSkills{Extra: []string{"lib/f.md"}}, bench: map[string]string{"lib/f.md": "x"}, errHas: "not a folder"},
		{name: "symlink inside a skill", skills: "skills", task: map[string]string{"skills/a/SKILL.md": "a"},
			setup: func(t *testing.T, taskDir, _ string) {
				if err := os.Symlink("/etc/hostname", filepath.Join(taskDir, "skills/a/leak")); err != nil {
					t.Fatal(err)
				}
			}, errHas: "leak is not a regular file"},
		{name: "symlinked skill folder in a collection", skills: "skills", task: map[string]string{"skills/a/SKILL.md": "a", "other/b/SKILL.md": "b"},
			setup: func(t *testing.T, taskDir, _ string) {
				if err := os.Symlink(filepath.Join(taskDir, "other/b"), filepath.Join(taskDir, "skills/b")); err != nil {
					t.Fatal(err)
				}
			}, errHas: "is a symlink"},
		{name: "symlinked skills folder leaving the task", skills: "skills",
			setup: func(t *testing.T, taskDir, benchRoot string) {
				writeTree(t, benchRoot, map[string]string{"outside/a/SKILL.md": "a"})
				if err := os.Symlink(filepath.Join(benchRoot, "outside"), filepath.Join(taskDir, "skills")); err != nil {
					t.Fatal(err)
				}
			}, errHas: "leaves"},
		{name: "bad folder name", skills: "skills", task: map[string]string{"skills/a b/SKILL.md": "a"}, errHas: "must match"},
		{name: "SKILL.md is a folder", skills: "skills/a", task: map[string]string{"skills/a/SKILL.md/x": "a"}, errHas: "not a regular file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskDir, benchRoot := t.TempDir(), t.TempDir()
			writeTree(t, taskDir, tt.task)
			writeTree(t, benchRoot, tt.bench)
			if tt.setup != nil {
				tt.setup(t, taskDir, benchRoot)
			}
			cfg := &schemas.AgentConfig{ID: "a", Skills: tt.agent}
			task := &schemas.Task{ID: "t01", Dir: taskDir, Skills: tt.skills}
			got, err := ResolveSkills(benchRoot, cfg, task)
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.errHas)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, s := range got {
				names = append(names, s.Name)
				if _, err := os.Stat(filepath.Join(s.Host, SkillFile)); err != nil {
					t.Errorf("skill %s host %s has no SKILL.md", s.Name, s.Host)
				}
			}
			if strings.Join(names, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("skills = %v, want %v", names, tt.want)
			}
		})
	}
}

func TestSkillsDir(t *testing.T) {
	tests := []struct {
		name   string
		cfg    schemas.AgentConfig
		want   string
		errHas string
	}{
		{name: "claude code default", cfg: schemas.AgentConfig{Parser: ParserClaudeCode}, want: "/home/agent/.claude/skills"},
		{name: "claude code config dir from env", cfg: schemas.AgentConfig{Parser: ParserClaudeCode, Env: map[string]string{"CLAUDE_CONFIG_DIR": "/opt/cc/"}}, want: "/opt/cc/skills"},
		{name: "codex default", cfg: schemas.AgentConfig{Parser: ParserCodex}, want: "/home/agent/.codex/skills"},
		{name: "codex home from env", cfg: schemas.AgentConfig{Parser: ParserCodex, Env: map[string]string{"CODEX_HOME": "/srv/codex"}}, want: "/srv/codex/skills"},
		{name: "templated env is ignored", cfg: schemas.AgentConfig{Parser: ParserCodex, Env: map[string]string{"CODEX_HOME": "{{workspace}}/.codex"}}, want: "/home/agent/.codex/skills"},
		{name: "opencode default", cfg: schemas.AgentConfig{Parser: ParserOpenCode}, want: "/home/agent/.config/opencode/skills"},
		{name: "opencode xdg", cfg: schemas.AgentConfig{Parser: ParserOpenCode, Env: map[string]string{"XDG_CONFIG_HOME": "/cfg"}}, want: "/cfg/opencode/skills"},
		{name: "no native folder", cfg: schemas.AgentConfig{}, want: FallbackSkillsDir},
		{name: "skills.dir wins", cfg: schemas.AgentConfig{Parser: ParserCodex, Skills: &schemas.AgentSkills{Dir: "/home/agent/.agents/skills/"}}, want: "/home/agent/.agents/skills"},
		{name: "skills.dir relative", cfg: schemas.AgentConfig{Skills: &schemas.AgentSkills{Dir: "skills"}}, errHas: "absolute"},
		{name: "skills.dir in workspace", cfg: schemas.AgentConfig{Skills: &schemas.AgentSkills{Dir: "/workspace/.skills"}}, errHas: "outside the workspace"},
		{name: "skills.dir root", cfg: schemas.AgentConfig{Skills: &schemas.AgentSkills{Dir: "/"}}, errHas: "outside the workspace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SkillsDir(&tt.cfg)
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.errHas)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("SkillsDir = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestSkillHashes(t *testing.T) {
	taskDir, benchRoot := t.TempDir(), t.TempDir()
	writeTree(t, taskDir, map[string]string{"skills/pdf/SKILL.md": "pdf skill", "skills/pdf/scripts/run.py": "print(1)"})
	writeTree(t, benchRoot, map[string]string{"lib/style/SKILL.md": "style skill"})
	cfg := &schemas.AgentConfig{ID: "a", Adapter: AdapterCommand, Parser: ParserCodex, Skills: &schemas.AgentSkills{Extra: []string{"lib/style"}}}
	task := &schemas.Task{ID: "t01", Dir: taskDir, Skills: "skills"}
	got, err := SkillHashes(benchRoot, cfg, task)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/home/agent/.codex/skills/pdf/SKILL.md":       utils.SHA256Bytes([]byte("pdf skill")),
		"/home/agent/.codex/skills/pdf/scripts/run.py": utils.SHA256Bytes([]byte("print(1)")),
		"/home/agent/.codex/skills/style/SKILL.md":     utils.SHA256Bytes([]byte("style skill")),
	}
	if len(got) != len(want) {
		t.Fatalf("hashes = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}

	t.Run("content change changes the hash", func(t *testing.T) {
		writeTree(t, benchRoot, map[string]string{"lib/style/SKILL.md": "style skill v2"})
		again, err := SkillHashes(benchRoot, cfg, task)
		if err != nil {
			t.Fatal(err)
		}
		if again["/home/agent/.codex/skills/style/SKILL.md"] == want["/home/agent/.codex/skills/style/SKILL.md"] {
			t.Fatal("skill content changed but its hash did not")
		}
	})
	t.Run("disabled arm hashes nothing", func(t *testing.T) {
		off := *cfg
		off.Skills = &schemas.AgentSkills{Enabled: boolPtr(false), Extra: cfg.Skills.Extra}
		got, err := SkillHashes(benchRoot, &off, task)
		if err != nil || len(got) != 0 {
			t.Fatalf("hashes = %v, %v; want none", got, err)
		}
	})
	t.Run("added to manifest files under the task id", func(t *testing.T) {
		files := map[string]string{"/home/agent/.codex/AGENTS.md": "x"}
		if err := AddSkillHashes(files, benchRoot, cfg, task); err != nil {
			t.Fatal(err)
		}
		if files["t01:/home/agent/.codex/skills/pdf/SKILL.md"] != want["/home/agent/.codex/skills/pdf/SKILL.md"] || len(files) != 4 {
			t.Fatalf("files = %v", files)
		}
	})
	t.Run("adapter that installs no skills adds nothing", func(t *testing.T) {
		scripted := *cfg
		scripted.Adapter = AdapterScripted
		files := map[string]string{}
		if err := AddSkillHashes(files, benchRoot, &scripted, task); err != nil || len(files) != 0 {
			t.Fatalf("files = %v, %v", files, err)
		}
	})
	t.Run("no skills adds nothing", func(t *testing.T) {
		files := map[string]string{}
		if err := AddSkillHashes(files, benchRoot, &schemas.AgentConfig{ID: "a", Adapter: AdapterCommand}, &schemas.Task{ID: "t02", Dir: taskDir}); err != nil || len(files) != 0 {
			t.Fatalf("files = %v, %v", files, err)
		}
	})
	t.Run("invalid skills fail", func(t *testing.T) {
		bad := &schemas.Task{ID: "t03", Dir: taskDir, Skills: "missing"}
		if err := AddSkillHashes(map[string]string{}, benchRoot, cfg, bad); err == nil || !strings.Contains(err.Error(), "skills") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPlanSkills(t *testing.T) {
	agent := commandAgent()
	agent.Parser = ParserClaudeCode
	rc, cfg := fixture(t, agent)
	writeTree(t, rc.Task.Dir, map[string]string{"skills/pdf/SKILL.md": "p", "skills/xlsx/SKILL.md": "x"})
	rc.Task.Skills = "skills"
	plan, err := newCommandAdapter(cfg).Plan(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	realTask, err := filepath.EvalSymlinks(rc.Task.Dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/home/agent/.claude/skills/pdf":  filepath.Join(realTask, "skills", "pdf"),
		"/home/agent/.claude/skills/xlsx": filepath.Join(realTask, "skills", "xlsx"),
	}
	if len(plan.Dirs) != len(want) {
		t.Fatalf("dirs = %v, want %v", plan.Dirs, want)
	}
	for k, v := range want {
		if plan.Dirs[k] != v {
			t.Errorf("dirs[%s] = %q, want %q", k, plan.Dirs[k], v)
		}
	}
	if plan.Env[EnvSkillsDir] != "/home/agent/.claude/skills" {
		t.Errorf("%s = %q", EnvSkillsDir, plan.Env[EnvSkillsDir])
	}
	if plan.Files[InstructionFile] != testInstruction {
		t.Error("the instruction must not change when skills are installed")
	}

	t.Run("disabled arm installs none", func(t *testing.T) {
		agent.Skills = &schemas.AgentSkills{Enabled: boolPtr(false)}
		defer func() { agent.Skills = nil }()
		plan, err := newCommandAdapter(cfg).Plan(t.Context(), rc)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Dirs != nil {
			t.Fatalf("dirs = %v, want none", plan.Dirs)
		}
		if _, ok := plan.Env[EnvSkillsDir]; ok {
			t.Fatalf("%s set without skills", EnvSkillsDir)
		}
	})
	t.Run("env already defines BENCH_SKILLS_DIR", func(t *testing.T) {
		agent.Env[EnvSkillsDir] = "/x"
		defer delete(agent.Env, EnvSkillsDir)
		if _, err := newCommandAdapter(cfg).Plan(t.Context(), rc); err == nil || !strings.Contains(err.Error(), EnvSkillsDir) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad skills fail the plan", func(t *testing.T) {
		rc.Task.Skills = "../.."
		defer func() { rc.Task.Skills = "skills" }()
		if _, err := newCommandAdapter(cfg).Plan(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "leaves") {
			t.Fatalf("err = %v", err)
		}
	})
}
