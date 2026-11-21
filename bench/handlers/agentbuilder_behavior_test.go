package handlers

import (
	"slices"
	"strings"
	"testing"
)

// The Setup form's behavior fields round-trip through the YAML and clear
// cleanly; a nil Behavior (an older client) leaves the keys alone.
func TestAgentBehaviorFieldsRoundTrip(t *testing.T) {
	doc := []byte("# my agent\nid: support-bot\nadapter: command\nimage: support-bot:latest\nargv: [run]\nmodel: gpt-x\nbudgets:\n  wall_s: 600\n  cpus: 2\n  mem_gb: 4\ngateway:\n  provider: openai\n")
	cfg, err := decodeAgent(doc, "agents/support-bot.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f := abFieldsOf(cfg, doc)
	if f.Behavior == nil || f.Behavior.MemoryPaths == nil || f.Behavior.Capabilities == nil || f.Behavior.Fallbacks == nil {
		t.Fatalf("empty behavior must carry arrays: %+v", f.Behavior)
	}
	f.Behavior.NotesProjectFile = "AGENTS.md"
	f.Behavior.NotesUserFile = "prompts/system.md"
	f.Behavior.NotesUserPath = "/root/.agent/instructions.md"
	f.Behavior.MemoryPaths = []string{"/root/.memory", " ", "/tmp/notes"}
	f.Behavior.MaxRetries = 3
	f.Behavior.MaxToolCalls = 40
	f.Behavior.ProviderRetries = 2
	f.Behavior.Fallbacks = []string{"gpt-y"}
	f.Behavior.Capabilities = []string{"shell", "tools"}
	out, err := abApplyFields(doc, f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeAgent(out, "agents/support-bot.yaml")
	if err != nil {
		t.Fatalf("written YAML does not validate: %v\n%s", err, out)
	}
	b := abFieldsOf(got, out).Behavior
	if b.NotesProjectFile != "AGENTS.md" || b.NotesUserFile != "prompts/system.md" || b.NotesUserPath != "/root/.agent/instructions.md" ||
		!slices.Equal(b.MemoryPaths, []string{"/root/.memory", "/tmp/notes"}) || b.MaxRetries != 3 || b.MaxToolCalls != 40 ||
		b.ProviderRetries != 2 || !slices.Equal(b.Fallbacks, []string{"gpt-y"}) || !slices.Equal(b.Capabilities, []string{"shell", "tools"}) {
		t.Fatalf("round trip = %+v\n%s", b, out)
	}
	if got.Budgets.WallS != 600 || !strings.Contains(string(out), "# my agent") {
		t.Fatalf("other keys or the comment were lost:\n%s", out)
	}

	// Clearing every field removes the keys and the empty mappings.
	f2 := abFieldsOf(got, out)
	f2.Behavior = &AgentBehaviorFields{}
	cleared, err := abApplyFields(out, f2)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"notes:", "memory:", "max_retries", "max_tool_calls", "fallbacks", "capabilities"} {
		if strings.Contains(string(cleared), key) {
			t.Errorf("%s left after clearing:\n%s", key, cleared)
		}
	}

	// An older client sends no behavior: nothing it does not know about changes.
	f3 := abFieldsOf(got, out)
	f3.Behavior = nil
	kept, err := abApplyFields(out, f3)
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := decodeAgent(kept, "agents/support-bot.yaml"); k == nil || k.Notes.ProjectFile != "AGENTS.md" || k.Budgets.MaxRetries != 3 {
		t.Fatalf("nil behavior changed the file:\n%s", kept)
	}

	// An unknown capability is refused by the schema-checked decoder.
	f4 := abFieldsOf(got, out)
	f4.Behavior.Capabilities = []string{"teleport"}
	bad, err := abApplyFields(out, f4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAgent(bad, "agents/support-bot.yaml"); err == nil {
		t.Fatalf("unknown capability accepted:\n%s", bad)
	}
}
