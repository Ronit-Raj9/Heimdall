package suite

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestAgentSchemaAcceptsFilesRawAndInstructions(t *testing.T) {
	doc := "id: a\nadapter: command\nimage: img\nargv: [run]\nmodel: m\nfiles_raw:\n  /home/agent/skills/x/SKILL.md: \"keep {{model}}\"\ninstructions: Always run the tests.\n"
	var cfg schemas.AgentConfig
	if err := DecodeYAML([]byte(doc), "agent.yaml", SchemaAgent, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.FilesRaw["/home/agent/skills/x/SKILL.md"] != "keep {{model}}" || cfg.Instructions != "Always run the tests." {
		t.Errorf("decoded %+v", cfg)
	}
}
