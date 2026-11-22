package compat

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestVoicePlatformTaskOK(t *testing.T) {
	voiceTask := &schemas.Task{ID: "v", Voice: &schemas.VoiceSpec{}}
	textTask := &schemas.Task{ID: "x"}
	agent := func(p string) *schemas.AgentConfig {
		return &schemas.AgentConfig{ID: "a", Adapter: "voice_platform", VoicePlatform: &schemas.VoicePlatformAgent{Platform: p}}
	}
	if !ChecksTasks(agent("vapi")) {
		t.Fatal("a voice platform agent has task rules")
	}
	cases := []struct {
		p    string
		task *schemas.Task
		ok   bool
		why  string
	}{
		{"vapi", voiceTask, true, ""},
		{"elevenlabs", voiceTask, true, ""},
		{"websocket", voiceTask, true, ""},
		{"retell", voiceTask, false, "Retell agent can only be reached through its chat API"},
		{"websocket", textTask, false, "only takes calls"},
		{"vapi", textTask, false, VoicePlatformTextPending},
	}
	for _, c := range cases {
		ok, why := TaskOK(agent(c.p), nil, c.task)
		if ok != c.ok || !strings.Contains(why, c.why) {
			t.Errorf("%s on %s: %v %q", c.p, c.task.ID, ok, why)
		}
	}
	VoicePlatformText = true
	defer func() { VoicePlatformText = false }()
	answerTask := &schemas.Task{ID: "q", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}
	if ok, why := TaskOK(agent("retell"), nil, answerTask); !ok {
		t.Errorf("with the driver wired, text runs: %q", why)
	}
	if ok, why := TaskOK(agent("vapi"), nil, textTask); ok || why != VoicePlatformAnswerOnly {
		t.Errorf("a code change task: %v %q", ok, why)
	}
	if ok, why := TaskOK(agent("websocket"), nil, answerTask); ok || !strings.Contains(why, "only takes calls") {
		t.Errorf("websocket text: %v %q", ok, why)
	}
}
