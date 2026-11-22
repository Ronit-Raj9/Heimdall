package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestChatTaskErrs(t *testing.T) {
	task := &schemas.Task{ID: "t1", Chat: &schemas.ChatInput{File: "chat.json", UserTurns: 2}}
	arms := map[string]schemas.ArmManifest{
		"a": {Config: schemas.AgentConfig{Adapter: "claude_code"}},
		"b": {Config: schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: "https://x"}}},
		"c": {Config: schemas.AgentConfig{Adapter: "openai_agent", OpenAI: &schemas.OpenAIAgent{}}},
		"d": {Config: schemas.AgentConfig{Adapter: "scripted"}},
	}
	errs := chatTaskErrs(task, arms, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "arm a") {
		t.Fatalf("errs = %v", errs)
	}
	if errs := chatTaskErrs(&schemas.Task{ID: "t2", Chat: &schemas.ChatInput{File: "chat.json"}}, arms, nil); len(errs) != 0 {
		t.Fatalf("a conversation without scripted turns runs on any agent: %v", errs)
	}
	compat := &schemas.ArmManifest{Config: schemas.AgentConfig{Adapter: "codex"}}
	if errs := chatTaskErrs(task, nil, compat); len(errs) != 1 {
		t.Fatalf("compat: %v", errs)
	}
}

func TestTaskMetadataFrozen(t *testing.T) {
	suite := &schemas.Suite{ID: "s", Tasks: []*schemas.Task{
		{ID: "a", Metadata: map[string]string{"tier": "pro"}}, {ID: "b"},
	}}
	got := taskMetadata(suite, []condPlan{{tasks: []string{"a", "b"}}})
	if len(got) != 1 || got["a"]["tier"] != "pro" {
		t.Fatalf("metadata = %v", got)
	}
	suite.Tasks[0].Metadata["tier"] = "free"
	if got["a"]["tier"] != "pro" {
		t.Fatal("metadata not copied")
	}
	if taskMetadata(suite, []condPlan{{tasks: []string{"b"}}}) != nil {
		t.Fatal("no metadata must stay nil")
	}
}
