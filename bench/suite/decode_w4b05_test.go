package suite

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// pullYAMLW4b05 is the exact text pullAgentYAML (ui/app/agents_/new/views/connectForm.tsx)
// writes for "Heimdall drives the chat" with "Coding tasks" on.
const pullYAMLW4b05 = `id: zz-test-p02-pull
adapter: pull
image: bench/coding-core-base:1
budgets:
  wall_s: 900
  cpus: 1
  mem_gb: 2
pull:
  name: zz-test-p02-pull
  repo: tarball
  conversation:
    driver: bench
    history: client
`

func TestAgentSchemaAcceptsPullAgent(t *testing.T) {
	var cfg schemas.AgentConfig
	if err := DecodeYAML([]byte(pullYAMLW4b05), "agents/zz-test-p02-pull.yaml", SchemaAgent, &cfg); err != nil {
		t.Fatalf("pull agent refused: %v", err)
	}
	if cfg.Pull == nil || cfg.Pull.Repo != "tarball" || cfg.Pull.Conversation == nil || cfg.Pull.Conversation.Driver != "bench" {
		t.Fatalf("pull block not decoded: %+v", cfg.Pull)
	}
	// Every field of schemas.PullAgent and its ConversationSpec.
	full := pullYAMLW4b05
	full = strings.Replace(full, "  repo: tarball\n", "  repo: tarball\n  timeout_s: 600\n  turn_timeout_s: 60\n  tools: off\n  max_tool_rounds: 5\n", 1)
	full += "    max_turns: 10\n    greeting: Hello\n"
	if err := DecodeYAML([]byte(full), "agents/full.yaml", SchemaAgent, &schemas.AgentConfig{}); err != nil {
		t.Fatalf("pull agent with every field refused: %v", err)
	}
}

func TestAgentSchemaRefusesUnknownPullField(t *testing.T) {
	y := "id: a\nadapter: pull\npull:\n  nope: 1\n"
	if err := DecodeYAML([]byte(y), "agents/a.yaml", SchemaAgent, &schemas.AgentConfig{}); err == nil {
		t.Fatal("unknown pull field accepted")
	}
}
