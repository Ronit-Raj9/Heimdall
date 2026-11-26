package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestInstructionTextStanding(t *testing.T) {
	if got := instructionText("", "do the task"); got != "do the task" {
		t.Errorf("empty standing: %q", got)
	}
	if got := instructionText("  \n", "do the task"); got != "do the task" {
		t.Errorf("blank standing: %q", got)
	}
	want := "Standing instructions from the agent's owner:\n\nRun the tests first.\n\n---\n\ndo the task"
	if got := instructionText("Run the tests first.\n\n", "do the task"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Raw files reach the container byte for byte (a "{{" is not a template),
// standing instructions go before the task, and a path in both maps fails.
func TestPlanRawFilesAndInstructions(t *testing.T) {
	agent := &schemas.AgentConfig{ID: "a", Adapter: "command", Image: "img", Argv: []string{"run"}, Model: "m",
		Files:        map[string]string{"/home/agent/a.md": "model {{model}}"},
		FilesRaw:     map[string]string{"/home/agent/skills/x/SKILL.md": "keep {{model}} as is"},
		Instructions: "Always run the tests."}
	rc, cfg := fixture(t, agent)
	plan, err := newCommandAdapter(cfg).Plan(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Files["/home/agent/skills/x/SKILL.md"]; got != "keep {{model}} as is" {
		t.Errorf("raw file %q", got)
	}
	if got := plan.Files["/home/agent/a.md"]; got != "model m" {
		t.Errorf("templated file %q", got)
	}
	if got := plan.Files[InstructionFile]; !strings.HasPrefix(got, "Standing instructions from the agent's owner:\n\nAlways run the tests.\n\n---\n\n") || !strings.HasSuffix(got, testInstruction) {
		t.Errorf("instruction file %q", got)
	}
	agent.FilesRaw["/home/agent/a.md"] = "twice"
	if _, err := newCommandAdapter(cfg).Plan(context.Background(), rc); err == nil || !strings.Contains(err.Error(), "agent files already define /home/agent/a.md") {
		t.Errorf("duplicate path: %v", err)
	}
}
