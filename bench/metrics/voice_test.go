package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
)

// voiceRun builds a run folder holding a recorded call timeline (from
// bench/voice/testdata) as the caller's export, and optional raw gateway rows.
func voiceRun(t *testing.T, fixture, rawRows string) (*schemas.RunContext, *schemas.Evidence) {
	t.Helper()
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("..", "voice", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "state", "customer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "customer", "timeline.jsonl"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if rawRows != "" {
		if err := os.WriteFile(filepath.Join(dir, "gateway.raw.jsonl"), []byte(rawRows), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rc := &schemas.RunContext{Dir: dir, Task: &schemas.Task{ID: "t", Voice: &schemas.VoiceSpec{}}}
	ev := &schemas.Evidence{State: []schemas.StateFile{{Service: "customer", ID: "timeline.jsonl", Path: "state/customer/timeline.jsonl",
		SHA256: utils.SHA256Bytes(raw), Bytes: int64(len(raw))}}}
	return rc, ev
}

func voiceValue(t *testing.T, name string, rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
	t.Helper()
	v, err := newVoiceMetric(name).Compute(context.Background(), rc, ev)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVoiceMetricsGoodCall(t *testing.T) {
	rc, ev := voiceRun(t, "call-good.jsonl", "")
	for _, name := range []string{TimeToFirstAudioMS, TurnLatencyP50MS, TurnLatencyP95MS, DeadAirS} {
		v := voiceValue(t, name, rc, ev)
		if v.State != schemas.ValueObserved || v.Evidence != schemas.EvidenceBoundary {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	if v, _ := voiceValue(t, DeadAirS, rc, ev).Float(); v != 0 {
		t.Fatalf("dead air %v", v)
	}
	if v := voiceValue(t, BargeInHandled, rc, ev); v.State != schemas.ValueNotApplicable {
		t.Fatalf("barge-in without an interruption: %+v", v)
	}
	// The agent reported its transcript of the opening: WER from it, labelled.
	w := voiceValue(t, STTWER, rc, ev)
	if f, _ := w.Float(); f != 0 || w.Source != schemas.EvidenceSourceAgentReported || w.Evidence != schemas.EvidenceNative {
		t.Fatalf("wer %+v", w)
	}
	if v := voiceValue(t, VoiceCostPerMinute, rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("cost without gateway rows: %+v", v)
	}
	one := 0.002
	ev.Requests = &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "r1", Kind: "audio"}}}
	ev.Cost = []schemas.CostRow{{RequestID: "r1", USD: schemas.Observed(one, schemas.EvidenceGateway)}}
	c := voiceValue(t, VoiceCostPerMinute, rc, ev)
	f, ok := c.Float()
	if !ok || f <= one {
		t.Fatalf("cost per minute of a call under a minute: %+v", c)
	}
	ev.Cost = append(ev.Cost, schemas.CostRow{RequestID: "r2", USD: schemas.Unknown("no price", schemas.EvidenceGateway)})
	if v := voiceValue(t, VoiceCostPerMinute, rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("an unpriced row must make it unknown: %+v", v)
	}
}

func TestVoiceMetricsGatewayWER(t *testing.T) {
	rc, ev := voiceRun(t, "call-good.jsonl", "")
	// Place a transcription row 500 ms after the caller's first line ended.
	raw, _ := os.ReadFile(filepath.Join(rc.Dir, "state", "customer", "timeline.jsonl"))
	events, err := parseTimelineForTest(raw)
	if err != nil {
		t.Fatal(err)
	}
	wall, opening := events[0], 0.0
	for _, e := range events {
		if e.Ev == "caller_speech_end" {
			opening = e.TMS
			break
		}
	}
	at := time.UnixMilli(wall.WallMS + int64(opening) + 500).UTC().Format(time.RFC3339Nano) // the agent transcribes once the caller's opening ended
	rows := `{"request_id":"r1","raw_available":true,"raw_detail":{"id":"r1","object":"transcription","timestamp":"` + at + `","model":"whisper-1","status":"success","cost":0.0001,"transcription_output":{"text":"Hi my grinder from order B-3001 arrived cracked I would like a refund"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(rc.Dir, "gateway.raw.jsonl"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	w := voiceValue(t, STTWER, rc, ev)
	if f, _ := w.Float(); f != 0 || w.Source != schemas.EvidenceSourceBifrostLog || w.Evidence != schemas.EvidenceGateway {
		t.Fatalf("gateway wer %+v", w)
	}
}

func TestVoiceMetricsBargeInAndSilence(t *testing.T) {
	rc, ev := voiceRun(t, "call-barge-handled.jsonl", "")
	if v, _ := voiceValue(t, BargeInHandled, rc, ev).Float(); v != 1 {
		t.Fatalf("handled barge-in: %v", v)
	}
	rc, ev = voiceRun(t, "call-barge-ignored.jsonl", "")
	if v := voiceValue(t, BargeInHandled, rc, ev); v.State != schemas.ValueObserved || *v.V != 0 {
		t.Fatalf("ignored barge-in: %+v", v)
	}
	rc, ev = voiceRun(t, "call-silent-agent.jsonl", "")
	if v := voiceValue(t, TimeToFirstAudioMS, rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("silent agent first audio: %+v", v)
	}
	if v, _ := voiceValue(t, DeadAirS, rc, ev).Float(); v < 3 {
		t.Fatalf("silent agent dead air: %v", v)
	}
	if v := voiceValue(t, TurnLatencyP50MS, rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("silent agent latency: %+v", v)
	}
}

func TestVoiceMetricsNotApplicableAndUnknown(t *testing.T) {
	v, _ := newVoiceMetric(DeadAirS).Compute(context.Background(), &schemas.RunContext{Task: &schemas.Task{ID: "plain"}}, &schemas.Evidence{})
	if v.State != schemas.ValueNotApplicable {
		t.Fatalf("plain task: %+v", v)
	}
	rc, ev := voiceRun(t, "call-good.jsonl", "")
	ev.State = nil
	if v := voiceValue(t, DeadAirS, rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("no export: %+v", v)
	}
	rc, ev = voiceRun(t, "call-good.jsonl", "")
	ev.State[0].SHA256 = "0000"
	if v := voiceValue(t, DeadAirS, rc, ev); v.State != schemas.ValueUnknown {
		t.Fatalf("tampered timeline: %+v", v)
	}
	// An agent that never dialled: the caller's export says no call.
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "state", "customer"), 0o755)
	nc := []byte(`{"ev": "no_call", "t_ms": 0}` + "\n")
	_ = os.WriteFile(filepath.Join(dir, "state", "customer", "timeline.jsonl"), nc, 0o644)
	rc = &schemas.RunContext{Dir: dir, Task: &schemas.Task{ID: "t", Voice: &schemas.VoiceSpec{}}}
	ev = &schemas.Evidence{State: []schemas.StateFile{{Service: "customer", ID: "timeline.jsonl", Path: "state/customer/timeline.jsonl", SHA256: utils.SHA256Bytes(nc)}}}
	if v := voiceValue(t, TimeToFirstAudioMS, rc, ev); v.State != schemas.ValueUnknown || v.Reason == "" {
		t.Fatalf("no call: %+v", v)
	}
}

func parseTimelineForTest(raw []byte) ([]voice.Event, error) { return voice.ParseTimeline(raw) }
