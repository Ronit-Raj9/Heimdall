package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/voice"
	"github.com/valyala/fasthttp"
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

// voiceSuites serves one suite whose task t01 has its own voice bounds.
type voiceSuites struct{ version, hash string }

// TaskHash is the task identity the loaded task hashes to now.
func (v voiceSuites) TaskHash(*schemas.Suite, *schemas.Task) (string, error) { return v.hash, nil }

func (voiceSuites) SuiteIDs(context.Context) ([]string, error) { return []string{"coding-core"}, nil }
func (v voiceSuites) LoadSuite(_ context.Context, id string) (*schemas.Suite, error) {
	return &schemas.Suite{ID: id, Version: v.version, Tasks: []*schemas.Task{
		{ID: "t01", Voice: &schemas.VoiceSpec{BargeInBoundMS: 1, DeadAirMS: 50}},
	}}, nil
}
func (voiceSuites) LockDrift(context.Context, *schemas.Suite) ([]schemas.SuiteDrift, error) {
	return nil, nil
}
func (voiceSuites) FingerprintDrift(context.Context, *schemas.Suite) ([]schemas.FingerprintDrift, error) {
	return nil, nil
}

// 6-C-11: the Voice tab classifies the call with the task's own bounds (as
// the sealed metrics do) when the frozen suite version loads, and says which.
func TestRunVoiceUsesTaskBounds(t *testing.T) {
	for _, tt := range []struct {
		name, version, recorded, now, source string
		bargeIn                              int
	}{
		{"frozen version", "1.0.0", "h1", "h1", VoiceBoundsTask, 1},
		{"suite changed since", "2.0.0", "h1", "h1", VoiceBoundsDefault, voice.DefaultBargeInBoundMS},
		// QC2-24-02: a same-version edit changes the task hash.
		{"task edited at the same version", "1.0.0", "h1", "h2", VoiceBoundsDefault, voice.DefaultBargeInBoundMS},
		{"no task hash recorded", "1.0.0", "", "h1", VoiceBoundsDefault, voice.DefaultBargeInBoundMS},
	} {
		t.Run(tt.name, func(t *testing.T) {
			idx := voiceFixture(t)
			idx.manifests[testExp].SuiteVersion = "1.0.0"
			for i := range idx.runs {
				if idx.runs[i].Run.RunID == "r_t01_A" {
					idx.runs[i].Slot.TaskHash = tt.recorded
				}
			}
			r := router.New()
			NewRunsHandler(testMode, idx).WithSuites(voiceSuites{version: tt.version, hash: tt.now}).RegisterRoutes(r)
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.Header.SetMethod("GET")
			ctx.Request.SetRequestURI(APIPrefix + "/runs/r_t01_A/voice")
			r.Handler(ctx)
			var out VoiceCall
			if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
				t.Fatalf("%v: %s", err, ctx.Response.Body())
			}
			if out.BoundsSource != tt.source || out.Analysis == nil || out.Analysis.BargeInBoundMS != tt.bargeIn {
				t.Fatalf("bounds %q analysis %+v", out.BoundsSource, out.Analysis)
			}
			if tt.source == VoiceBoundsTask && out.Analysis.BargeIns[0].Handled {
				t.Fatalf("a 1 ms bound cannot be met: %+v", out.Analysis.BargeIns[0])
			}
		})
	}
}
