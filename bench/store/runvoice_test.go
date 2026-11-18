package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X8 voice agents: RunVoiceFile serves only state/<caller>/{timeline.jsonl,caller.wav,agent.wav}.
func TestIndexRunVoiceFile(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_voice")
	run := mustNewRun(t, s, m, "s_a1", "r_voice", 1, schemas.OutcomePass)
	mustWrite(t, s, run, "state/customer/timeline.jsonl", `{"ev":"call_start"}`)
	mustWrite(t, s, run, "state/customer/agent.wav", "wav-bytes")
	mustWrite(t, s, run, "state/customer/transcript.json", "{}")
	dir := s.runDir(run)
	if err := os.Symlink(filepath.Join(dir, "state", "customer", "agent.wav"), filepath.Join(dir, "state", "customer", "caller.wav")); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(s)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := ix.RunVoiceFile("r_voice", "customer", "agent.wav")
	if err != nil || !bytes.Equal(b, []byte("wav-bytes")) {
		t.Fatalf("RunVoiceFile = %q, %v", b, err)
	}
	for _, c := range [][2]string{{"customer", "transcript.json"}, {"customer", "caller.wav"}, {"customer", "../../seal.json"}, {"../customer", "agent.wav"}, {"helpdesk", "timeline.jsonl"}} {
		if _, err := ix.RunVoiceFile("r_voice", c[0], c[1]); !errors.Is(err, schemas.ErrNotFound) {
			t.Errorf("RunVoiceFile(%q, %q) = %v, want not found", c[0], c[1], err)
		}
	}
	if _, err := ix.RunVoiceFile("r_none", "customer", "agent.wav"); !errors.Is(err, schemas.ErrNotFound) {
		t.Errorf("unknown run: %v", err)
	}
}
