package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/voice"
)

// X8 voice agents: the voice template's sample task is a call with a voice
// caller beside the helpdesk, and the suite carries the caller's files.
func checkVoiceShape(t *testing.T, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	if s.Track != schemas.TrackVoice || s.Output == nil || s.Output.Kind != schemas.OutputState || s.Simulator == nil || s.Simulator.Mode != schemas.SimulatorScripted {
		t.Errorf("suite track %s output %+v simulator %+v", s.Track, s.Output, s.Simulator)
	}
	if task.Voice == nil || len(task.Services) != 2 {
		t.Fatalf("voice %+v services %+v", task.Voice, task.Services)
	}
	if err := voice.CheckTask(s, task); err != nil {
		t.Errorf("voice task: %v", err)
	}
	desk, caller := task.Services[0], task.Services[1]
	if desk.ID != "helpdesk" || desk.Role != schemas.ServiceRoleMCP || desk.Image != "bench/demo-voicedesk:1" {
		t.Errorf("helpdesk = %+v", desk)
	}
	if caller.ID != "customer" || caller.Role != schemas.ServiceRoleUser || caller.Image != desk.Image || caller.Files["/sim/user.json"] != "user.json" || len(caller.Export) != 4 {
		t.Errorf("caller = %+v", caller)
	}
}

func TestVoiceTemplateFiles(t *testing.T) {
	_, dir := newSuiteDir(t, "demo", TemplateVoice)
	svc := filepath.Join(dir, "services", "helpdesk")
	for name, want := range voice.Files(voice.CallerFiles) {
		got, err := os.ReadFile(filepath.Join(svc, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: not the bench's copy (%v)", name, err)
		}
	}
	for name, want := range simuser.Files() {
		if got, err := os.ReadFile(filepath.Join(svc, name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: not the bench's copy (%v)", name, err)
		}
	}
	df, err := os.ReadFile(filepath.Join(svc, "Dockerfile"))
	if err != nil || !bytes.Contains(df, []byte("COPY server.py simuser.py voicecaller.py tonecode.py wsproto.py voiceclient.py /srv/")) {
		t.Errorf("Dockerfile does not copy the caller's files: %v", err)
	}
}
