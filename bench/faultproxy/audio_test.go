package faultproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

// audioCall sends one audio request on the openai slot oa-1.
func audioCall(t *testing.T, env *testEnv, path, ctype string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, env.data.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer oa-auth-1")
	req.Header.Set("Content-Type", ctype)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func transcriptionForm(t *testing.T, model string, wav []byte) (string, []byte) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, _ := w.CreateFormFile("file", "caller.wav")
	_, _ = fw.Write(wav)
	_ = w.WriteField("model", model)
	_ = w.Close()
	return w.FormDataContentType(), b.Bytes()
}

func TestScriptedAudio(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	code, body := audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"gpt-4o-mini-tts","input":"I have refunded 45.00","voice":"alloy","response_format":"wav"}`))
	if code != http.StatusOK {
		t.Fatalf("speech %d %s", code, body)
	}
	pcm, rate, err := voice.ReadWAV(body)
	if err != nil || rate != 24000 {
		t.Fatalf("speech wav: rate %d err %v", rate, err)
	}
	if got, ok := voice.ToneDecode(voice.Resample(pcm, 24000, 16000)); !ok || got != "I have refunded 45.00" {
		t.Fatalf("speech audio decodes to %q", got)
	}
	if code, _ := audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"tts-1","input":"x"}`)); code != http.StatusBadRequest {
		t.Fatalf("mp3 must be refused, got %d", code)
	}
	ct, form := transcriptionForm(t, "whisper-1", voice.WAV(voice.ToneEncode("my order is B-3001", 100, 100), 16000))
	code, body = audioCall(t, env, "/v1/audio/transcriptions", ct, form)
	var tr struct {
		Text  string `json:"text"`
		Usage struct {
			Type    string `json:"type"`
			Seconds int    `json:"seconds"`
		} `json:"usage"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &tr) != nil || tr.Text != "my order is B-3001" || tr.Usage.Type != "duration" || tr.Usage.Seconds < 1 {
		t.Fatalf("transcription %d %s", code, body)
	}
	// Speech that is not tone code transcribes to nothing: the scripted model is not a speech model.
	ct, form = transcriptionForm(t, "whisper-1", voice.WAV(make([]byte, 32000), 16000))
	if code, body = audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != http.StatusOK || !bytes.Contains(body, []byte(`"text":""`)) {
		t.Fatalf("silence %d %s", code, body)
	}
	for _, e := range env.entries(t) {
		if e.Kind != KindAudio || e.Action != ActionPass || e.Upstream != schemas.LedgerUpstreamScripted || e.Call == 0 {
			t.Fatalf("ledger line %+v", e)
		}
	}
}

// TestSTTOutagePlan: a plan with model whisper-1 fails transcription calls
// only; chat calls on the same slot pass and keep their own numbering, and a
// plan without a model never counts audio.
func TestSTTOutagePlan(t *testing.T) {
	env := newTestEnvConfig(t, Config{})
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1", Mode: ModePersistent, FromCall: 1, Status: 503, Model: "whisper-1"})
	ct, form := transcriptionForm(t, "whisper-1", voice.WAV(voice.ToneEncode("hello", 0, 0), 16000))
	if code, body := audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != 503 {
		t.Fatalf("transcription under the outage: %d %s", code, body)
	}
	if code, _ := audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"gpt-4o-mini-tts","input":"hi","response_format":"wav"}`)); code != http.StatusOK {
		t.Fatalf("speech must not be failed by a whisper-1 plan: %d", code)
	}
	if code, _ := audioCall(t, env, "/v1/chat/completions", "application/json", []byte(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)); code != http.StatusOK {
		t.Fatalf("chat must not be failed by a whisper-1 plan: %d", code)
	}
	var inject, chat int
	for _, e := range env.entries(t) {
		if e.Action == ActionInject {
			inject++
			if e.Kind != KindAudio || e.Status != 503 {
				t.Fatalf("injected line %+v", e)
			}
		}
		if e.Kind == KindMessage {
			chat++
			if e.Call != 1 {
				t.Fatalf("chat call number %d: audio must not count among message calls", e.Call)
			}
		}
	}
	if inject != 1 || chat != 1 {
		t.Fatalf("inject %d chat %d", inject, chat)
	}
	// A plain model outage (no model) leaves audio alone.
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r2", Mode: ModePersistent, FromCall: 1, Status: 503})
	if code, _ := audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != http.StatusOK {
		t.Fatalf("a plan without a model must not fail audio: %d", code)
	}
}

func TestAudioModel(t *testing.T) {
	ct, form := transcriptionForm(t, "  whisper-1 ", []byte("RIFF...."))
	r, _ := http.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	r.Header.Set("Content-Type", ct)
	if got := audioModel(r, form); got != "whisper-1" {
		t.Fatalf("multipart model %q", got)
	}
	r.Header.Set("Content-Type", "application/json")
	if got := audioModel(r, []byte(`{"model":"tts-1"}`)); got != "tts-1" {
		t.Fatalf("json model %q", got)
	}
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if got := audioModel(r, []byte("garbage")); got != "" {
		t.Fatalf("broken form %q", got)
	}
}
