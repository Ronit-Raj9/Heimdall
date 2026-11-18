package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// X8 voice agents: GET /runs/{id}/voice and /runs/{id}/voice/audio/{track}.

type voiceIndex struct{ *fakeIndex }

func (v *voiceIndex) RunVoiceFile(runID, service, name string) ([]byte, error) {
	if data, ok := v.files[runID]["state/"+service+"/"+name]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("run %s voice %s/%s: %w", runID, service, name, schemas.ErrNotFound)
}

func voiceFixture(t *testing.T) *voiceIndex {
	t.Helper()
	timeline, err := os.ReadFile(filepath.Join("..", "voice", "testdata", "call-barge-handled.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	wav := []byte("RIFF....WAVEfmt agent audio, longer than a header")
	idx := testIndex()
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t02_A" {
			idx.runs[i].Sealed, idx.runs[i].SealHash = false, ""
		}
	}
	ev := schemas.Evidence{State: []schemas.StateFile{
		{Service: "customer", ID: "timeline.jsonl", Path: "state/customer/timeline.jsonl", SHA256: utils.SHA256Bytes(timeline), Bytes: int64(len(timeline))},
		{Service: "customer", ID: "agent.wav", Path: "state/customer/agent.wav", SHA256: utils.SHA256Bytes(wav), Bytes: int64(len(wav))},
		{Service: "customer", ID: "caller.wav", Path: "state/customer/caller.wav", SHA256: utils.SHA256Bytes([]byte("RIFF")), Bytes: 44},
	}}
	e, _ := json.Marshal(ev)
	seal, _ := json.Marshal(schemas.SealRecord{RunID: "r_t01_A", Files: map[string]string{
		"state/customer/timeline.jsonl": utils.SHA256Bytes(timeline), "state/customer/agent.wav": utils.SHA256Bytes([]byte("tampered"))}})
	for _, runID := range []string{"r_t01_A", "r_t02_A"} {
		idx.files[runID][FileEvidence] = e
		idx.files[runID][FileSeal] = seal
		idx.files[runID]["state/customer/timeline.jsonl"] = timeline
		idx.files[runID]["state/customer/agent.wav"] = wav
	}
	return &voiceIndex{fakeIndex: idx}
}

func TestRunVoice(t *testing.T) {
	d := fullDeps()
	d.index = voiceFixture(t)
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/voice", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	var out VoiceCall
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Called || out.Caller != "customer" || out.Analysis == nil || len(out.Analysis.BargeIns) != 1 || !out.Analysis.BargeIns[0].Handled {
		t.Fatalf("voice call %+v", out)
	}
	// Only the agent track is kept (the caller track is a bare header).
	if len(out.Audio) != 1 || out.Audio[0].Track != "agent" || out.Audio[0].URL != APIPrefix+"/runs/r_t01_A/voice/audio/agent" {
		t.Fatalf("audio %+v", out.Audio)
	}
	// The agent track does not match the seal, so it is not served.
	if resp := c.do(t, "GET", out.Audio[0].URL, nil, nil); resp.StatusCode() != 409 {
		t.Fatalf("tampered audio: %d %s", resp.StatusCode(), resp.Body())
	}
	// An unsealed run is read for its timeline but never serves audio.
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t02_A/voice", nil, nil); resp.StatusCode() != 200 {
		t.Fatalf("unsealed timeline: %d", resp.StatusCode())
	}
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t02_A/voice/audio/agent", nil, nil); resp.StatusCode() != 409 {
		t.Fatalf("unsealed audio: %d", resp.StatusCode())
	}
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/voice/audio/other", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("other track: %d", resp.StatusCode())
	}
}

func TestRunVoiceServesSealedAudio(t *testing.T) {
	d := fullDeps()
	idx := voiceFixture(t)
	wav := idx.files["r_t01_A"]["state/customer/agent.wav"]
	seal, _ := json.Marshal(schemas.SealRecord{RunID: "r_t01_A", Files: map[string]string{"state/customer/agent.wav": utils.SHA256Bytes(wav)}})
	idx.files["r_t01_A"][FileSeal] = seal
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/voice/audio/agent", nil, nil)
	if resp.StatusCode() != 200 || !bytes.Equal(resp.Body(), wav) || string(resp.Header.Peek("Content-Type")) != "audio/wav" ||
		string(resp.Header.Peek("X-Content-Type-Options")) != "nosniff" {
		t.Fatalf("audio %d %q", resp.StatusCode(), resp.Header.Peek("Content-Type"))
	}
}

func TestRunVoiceNoCall(t *testing.T) {
	d := fullDeps()
	idx := voiceFixture(t)
	nc := []byte(`{"ev": "no_call", "t_ms": 0}` + "\n")
	ev, _ := json.Marshal(schemas.Evidence{State: []schemas.StateFile{{Service: "customer", ID: "timeline.jsonl", Path: "state/customer/timeline.jsonl", SHA256: utils.SHA256Bytes(nc)}}})
	idx.files["r_t02_A"][FileEvidence] = ev
	idx.files["r_t02_A"]["state/customer/timeline.jsonl"] = nc
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t02_A/voice", nil, nil)
	var out VoiceCall
	_ = json.Unmarshal(resp.Body(), &out)
	if resp.StatusCode() != 200 || out.Called || out.Reason == "" {
		t.Fatalf("no call: %d %+v", resp.StatusCode(), out)
	}
	// A run without a caller has no voice call.
	d.index = &voiceIndex{fakeIndex: testIndex()}
	c = newTestClient(t, d)
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/voice", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("plain run: %d", resp.StatusCode())
	}
}
