package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSkillFilesRead(t *testing.T) {
	off := false
	call := func(tool, target string) schemas.Event {
		return schemas.Event{Type: schemas.EventToolCall, Source: schemas.SourceAgentStream, ToolName: tool, Target: target, ArgsHash: "h-" + tool + target}
	}
	turn := schemas.Event{Type: schemas.EventModelResponse, Source: schemas.SourceAgentStream}
	codex := &schemas.AgentConfig{Parser: parserCodex}
	tests := []struct {
		name   string
		agent  *schemas.AgentConfig
		skills string // Task.Skills
		events []schemas.Event
		want   schemas.Value
	}{
		{name: "no skills is not applicable", agent: codex, events: []schemas.Event{turn}, want: schemas.NotApplicable()},
		{name: "skills disabled is not applicable", agent: &schemas.AgentConfig{Parser: parserCodex, Skills: &schemas.AgentSkills{Enabled: &off, Extra: []string{"lib/a"}}}, skills: "skills",
			events: []schemas.Event{turn}, want: schemas.NotApplicable()},
		{name: "no stream is unknown", agent: codex, skills: "skills",
			events: []schemas.Event{{Type: schemas.EventModelResponse, Source: schemas.SourceGateway}},
			want:   schemas.Unknown("no agent_stream events: the agent's stream gives no file reads", schemas.EvidenceNative)},
		{name: "stream without reads is zero", agent: codex, skills: "skills",
			events: []schemas.Event{turn, call("exec_command", "pytest -q")}, want: schemas.Observed(0, schemas.EvidenceNative)},
		{name: "codex shell reads, distinct files", agent: codex, skills: "skills", events: []schemas.Event{turn,
			call("exec_command", "cat /home/agent/.codex/skills/pdf/SKILL.md"),
			call("exec_command", "sed -n 1,40p ~/.codex/skills/pdf/SKILL.md && cat '$HOME/.codex/skills/pdf/ref/forms.md'"),
			call("exec_command", "ls /home/agent/.codex/skills/ /home/agent/.codex/skills/pdf/"),
			call("exec_command", "cat /home/agent/.codex/skills/.system/imagegen/SKILL.md"),
			call("exec_command", "cat /home/agent/.codex/skills/*/SKILL.md"),
		}, want: schemas.Observed(2, schemas.EvidenceNative)},
		{name: "claude code Read and Skill tool", agent: &schemas.AgentConfig{Parser: parserClaudeCode, Skills: &schemas.AgentSkills{Extra: []string{"lib/a"}}},
			events: []schemas.Event{turn,
				call("Read", "/home/agent/.claude/skills/a/SKILL.md"),
				call("Read", "/home/agent/.claude/skills/a/SKILL.md"),
				call("Skill", ""), call("Skill", ""),
				call("Read", "/workspace/SKILL.md"),
			}, want: schemas.Observed(2, schemas.EvidenceNative)},
		{name: "claude config dir from env", agent: &schemas.AgentConfig{Parser: parserClaudeCode, Env: map[string]string{"CLAUDE_CONFIG_DIR": "/cc"}}, skills: "s",
			events: []schemas.Event{turn, call("Read", "/cc/skills/a/x.md"), call("Read", "/home/agent/.claude/skills/a/x.md")},
			want:   schemas.Observed(1, schemas.EvidenceNative)},
		{name: "opencode read and skill tool", agent: &schemas.AgentConfig{Parser: parserOpenCode}, skills: "s",
			events: []schemas.Event{turn, call("read", "/home/agent/.config/opencode/skills/b/SKILL.md"), call("skill", "b")},
			want:   schemas.Observed(2, schemas.EvidenceNative)},
		{name: "skills.dir and fallback folder", agent: &schemas.AgentConfig{Skills: &schemas.AgentSkills{Dir: "/srv/skills"}}, skills: "s",
			events: []schemas.Event{turn, call("bash", "cat /srv/skills/c/SKILL.md"), call("bash", "cat /opt/bench/skills/c/SKILL.md")},
			want:   schemas.Observed(1, schemas.EvidenceNative)},
		{name: "no native folder uses the fallback", agent: &schemas.AgentConfig{}, skills: "s",
			events: []schemas.Event{turn, call("bash", "cat /opt/bench/skills/c/SKILL.md; cat /opt/bench/skills/c/../d/SKILL.md")},
			want:   schemas.Observed(1, schemas.EvidenceNative)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &schemas.RunContext{Agent: tt.agent, Task: &schemas.Task{ID: "t01", Skills: tt.skills}}
			checkValue(t, compute(t, newSkillFilesReadMetric(), rc, &schemas.Evidence{Events: tt.events}), tt.want)
		})
	}
	t.Run("nil evidence and context", func(t *testing.T) {
		m := newSkillFilesReadMetric()
		checkValue(t, compute(t, m, nil, nil), schemas.NotApplicable())
		rc := &schemas.RunContext{Agent: codex, Task: &schemas.Task{Skills: "s"}}
		if v, err := m.Compute(context.Background(), rc, nil); err != nil || v.IsObserved() || v.State != schemas.ValueUnknown {
			t.Fatalf("nil evidence = %+v, %v; want unknown", v, err)
		}
	})
	t.Run("descriptor", func(t *testing.T) {
		d := newSkillFilesReadMetric().Descriptor()
		if d.Name != SkillFilesRead || d.Kind != schemas.KindCount || d.PS2 || d.MinEvidence != schemas.EvidenceNative {
			t.Fatalf("descriptor %+v", d)
		}
	})
}
