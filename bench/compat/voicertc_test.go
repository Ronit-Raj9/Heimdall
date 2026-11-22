package compat

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestVoiceRTCTasks(t *testing.T) {
	agent := &schemas.AgentConfig{ID: "lk", Adapter: "voice_rtc", VoiceRTC: &schemas.VoiceRTCAgent{Mode: "livekit"}}
	if !ChecksTasks(agent) {
		t.Fatal("voice_rtc agents have task rules")
	}
	if ok, why := voiceRTCTaskOK(&schemas.Task{ID: "text"}); ok || why != VoiceRTCTextReason {
		t.Fatalf("text task: %v %q", ok, why)
	}
	if ok, _ := voiceRTCTaskOK(&schemas.Task{ID: "call", Voice: &schemas.VoiceSpec{}}); !ok {
		t.Fatal("voice task refused")
	}
}
