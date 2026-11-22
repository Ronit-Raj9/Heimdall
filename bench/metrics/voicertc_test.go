package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
)

func TestAudioJitterMetric(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "voice", "testdata", "call-livekit-webrtc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rel := filepath.Join("state", "customer", voice.TimelineExport)
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{Dir: dir, Task: &schemas.Task{ID: "t", Voice: &schemas.VoiceSpec{}}}
	ev := &schemas.Evidence{State: []schemas.StateFile{{Service: "customer", ID: voice.TimelineExport, Path: rel, SHA256: utils.SHA256Bytes(raw)}}}
	m := newAudioJitterMetric()
	v, err := m.Compute(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := v.Float(); !ok || f <= 0 || f > 50 {
		t.Fatalf("jitter %+v", v)
	}
	ev.State[0].SHA256 = "00"
	if v, _ := m.Compute(context.Background(), rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("tampered timeline: %+v", v)
	}
	if v, _ := m.Compute(context.Background(), &schemas.RunContext{Task: &schemas.Task{ID: "x"}}, ev); v.State != schemas.ValueNotApplicable {
		t.Fatalf("text task: %+v", v)
	}
}
