package faultproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

const testSarvamKey = "sk_test_sarvam_secret"

// fakeSarvam is Sarvam's REST API as the docs describe it: speech-to-text
// (multipart) and text-to-speech (JSON, base64 WAV), keyed by
// api-subscription-key. It records what it was sent.
type fakeSarvam struct {
	srv    *httptest.Server
	mu     sync.Mutex
	stt    []map[string]string // form fields, plus "file" (the bytes) and "filename"
	tts    []map[string]any
	status int // non-zero: answer every request with this status and a Sarvam error
}

func newFakeSarvam(t *testing.T) *fakeSarvam {
	f := &fakeSarvam{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-subscription-key") != testSarvamKey || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"message":"Invalid API key","code":"invalid_api_key_error"}}`)
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = io.WriteString(w, `{"error":{"request_id":"x","message":"Text is too long","code":"invalid_request_error"}}`)
			return
		}
		switch r.URL.Path {
		case "/speech-to-text":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(r.Body, params["boundary"])
			got := map[string]string{}
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(part)
				if part.FormName() == "file" {
					got["filename"] = part.FileName()
				}
				got[part.FormName()] = string(b)
			}
			f.mu.Lock()
			f.stt = append(f.stt, got)
			f.mu.Unlock()
			_, _ = io.WriteString(w, `{"request_id":"20261121_x","transcript":"Hello, I would like a refund for order B3001.","language_code":"en-IN"}`)
		case "/text-to-speech":
			var got map[string]any
			_ = json.NewDecoder(r.Body).Decode(&got)
			f.mu.Lock()
			f.tts = append(f.tts, got)
			f.mu.Unlock()
			wav := voice.WAV(make([]byte, 22050*2/10), 22050)
			_ = json.NewEncoder(w).Encode(map[string]any{"request_id": "y", "audios": []string{base64.StdEncoding.EncodeToString(wav)}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSarvam) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stt), len(f.tts)
}

// sarvamEnv is a proxy whose openai provider is a real upstream (a fake that
// counts hits) with Sarvam audio pointed at f.
func sarvamEnv(t *testing.T, f *fakeSarvam, keys map[string]string) (*testEnv, *int) {
	t.Helper()
	openaiHits := 0
	oa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openaiHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"from openai"}`)
	}))
	t.Cleanup(oa.Close)
	provs := DefaultProviders()
	for i := range provs {
		if provs[i].Name == ProviderOpenAI {
			provs[i].BaseURL = oa.URL
			provs[i].SarvamAudio = &SarvamAudio{BaseURL: f.srv.URL}
		}
	}
	env := newTestEnvConfig(t, Config{Providers: provs, Getenv: func(k string) string { return keys[k] }})
	return env, &openaiHits
}

func bothKeys() map[string]string {
	return map[string]string{"OPENAI_API_KEY": "sk-openai", "SARVAM_API_KEY": testSarvamKey}
}

func sttForm(t *testing.T, model, language string, wav []byte) (string, []byte) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range map[string]string{"model": model, "language": language, "response_format": "json"} {
		_ = w.WriteField(k, v)
	}
	fw, _ := w.CreateFormFile("file", "caller.wav")
	_, _ = fw.Write(wav)
	_ = w.Close()
	return w.FormDataContentType(), b.Bytes()
}

func TestSarvamTranscription(t *testing.T) {
	f := newFakeSarvam(t)
	env, openaiHits := sarvamEnv(t, f, bothKeys())
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	wav := voice.WAV(make([]byte, 16000*2*2), 16000) // 2 s
	ct, form := sttForm(t, "openai/saaras:v4", "en", wav)
	code, body := audioCall(t, env, "/v1/audio/transcriptions", ct, form)
	var tr struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Usage    struct {
			Seconds int `json:"seconds"`
		} `json:"usage"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &tr) != nil || tr.Text != "Hello, I would like a refund for order B3001." || tr.Language != "en-IN" || tr.Usage.Seconds != 2 {
		t.Fatalf("transcription %d %s", code, body)
	}
	got := f.stt[0]
	if got["model"] != "saaras:v4" || got["language_code"] != "en-IN" || got["file"] != string(wav) || got["filename"] != "caller.wav" {
		t.Fatalf("sarvam was sent model %q language %q filename %q file %d bytes", got["model"], got["language_code"], got["filename"], len(got["file"]))
	}
	if _, ok := got["response_format"]; ok {
		t.Fatal("OpenAI-only fields must not reach Sarvam")
	}
	if *openaiHits != 0 {
		t.Fatal("a Sarvam model must not reach the openai upstream")
	}
	es := env.entries(t)
	if len(es) != 1 || es[0].Kind != KindAudio || es[0].Action != ActionPass || es[0].Upstream != schemas.LedgerUpstreamReal || es[0].Status != 200 || es[0].Call != 1 || es[0].RunID != "r1" {
		t.Fatalf("ledger %+v", es)
	}
	raw, _ := os.ReadFile(env.ledger)
	if bytes.Contains(raw, []byte(testSarvamKey)) {
		t.Fatal("the Sarvam key is on the ledger")
	}
	// A plain text answer when asked for one.
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("model", "saaras:v4")
	_ = w.WriteField("response_format", "text")
	fw, _ := w.CreateFormFile("file", "a.wav")
	_, _ = fw.Write(wav)
	_ = w.Close()
	if code, body := audioCall(t, env, "/v1/audio/transcriptions", w.FormDataContentType(), b.Bytes()); code != 200 || string(body) != "Hello, I would like a refund for order B3001." {
		t.Fatalf("text transcription %d %s", code, body)
	}
	if f.stt[1]["language_code"] != "en-IN" {
		t.Fatalf("default language %q", f.stt[1]["language_code"])
	}
	// whisper-1 is not a Sarvam model: it goes to the openai upstream.
	ct, form = sttForm(t, "whisper-1", "en", wav)
	if code, body := audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != 200 || !strings.Contains(string(body), "from openai") || *openaiHits != 1 {
		t.Fatalf("whisper-1 %d %s hits %d", code, body, *openaiHits)
	}
}

func TestSarvamSpeech(t *testing.T) {
	f := newFakeSarvam(t)
	env, openaiHits := sarvamEnv(t, f, bothKeys())
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	code, body := audioCall(t, env, "/v1/audio/speech", "application/json",
		[]byte(`{"model":"bulbul:v3","input":"Your refund of 45 pounds is done.","voice":"shubh","response_format":"wav","speed":1.1}`))
	if code != 200 {
		t.Fatalf("speech %d %s", code, body)
	}
	if _, rate, err := voice.ReadWAV(body); err != nil || rate != 22050 {
		t.Fatalf("speech answer is not the decoded WAV: rate %d err %v", rate, err)
	}
	got := f.tts[0]
	want := map[string]any{"text": "Your refund of 45 pounds is done.", "target_language_code": "en-IN", "model": "bulbul:v3", "speaker": "shubh", "output_audio_codec": "wav", "pace": 1.1}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("sarvam %s = %v, want %v (sent %v)", k, got[k], v, got)
		}
	}
	// An OpenAI voice name is not a Sarvam speaker: Sarvam's default speaks.
	if code, _ := audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"bulbul:v3","input":"hi","voice":"alloy","response_format":"pcm"}`)); code != 200 {
		t.Fatalf("pcm speech %d", code)
	}
	if _, ok := f.tts[1]["speaker"]; ok || f.tts[1]["output_audio_codec"] != "linear16" || f.tts[1]["speech_sample_rate"] != float64(24000) {
		t.Fatalf("pcm request %v", f.tts[1])
	}
	if code, _ := audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"bulbul:v3","input":"hi","response_format":"ogg"}`)); code != http.StatusBadRequest {
		t.Fatalf("an unknown format must be refused, got %d", code)
	}
	if *openaiHits != 0 {
		t.Fatal("Sarvam speech reached the openai upstream")
	}
	// A Sarvam error keeps its status, in the OpenAI envelope.
	f.status = http.StatusBadRequest
	code, body = audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"bulbul:v3","input":"hi","response_format":"wav"}`))
	if code != http.StatusBadRequest || !strings.Contains(string(body), `"message":"Sarvam: Text is too long"`) {
		t.Fatalf("relayed error %d %s", code, body)
	}
}

// TestSarvamSTTOutage: a plan naming the Sarvam model fails its
// transcriptions without calling Sarvam; speech and chat pass.
func TestSarvamSTTOutage(t *testing.T) {
	f := newFakeSarvam(t)
	env, _ := sarvamEnv(t, f, bothKeys())
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1", Nonce: "n1", Mode: ModePersistent, FromCall: 1, Status: 503, Model: "saaras:v4"})
	ct, form := sttForm(t, "saaras:v4", "en", voice.WAV(make([]byte, 3200), 16000))
	if code, body := audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != 503 {
		t.Fatalf("transcription under the outage %d %s", code, body)
	}
	if code, _ := audioCall(t, env, "/v1/audio/speech", "application/json", []byte(`{"model":"bulbul:v3","input":"hi","response_format":"wav"}`)); code != 200 {
		t.Fatalf("speech under the STT outage %d", code)
	}
	if stt, tts := f.calls(); stt != 0 || tts != 1 {
		t.Fatalf("sarvam calls stt %d tts %d", stt, tts)
	}
	es := env.entries(t)
	if es[0].Action != ActionInject || es[0].Nonce != "n1" || es[0].Kind != KindAudio || es[1].Action != ActionPass {
		t.Fatalf("ledger %+v", es)
	}
}

func TestSarvamWithoutKey(t *testing.T) {
	f := newFakeSarvam(t)
	// Real openai, no Sarvam key: a loud 503, never a call to the openai upstream.
	env, hits := sarvamEnv(t, f, map[string]string{"OPENAI_API_KEY": "sk-openai"})
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r1"})
	ct, form := sttForm(t, "saaras:v4", "en", voice.WAV(make([]byte, 3200), 16000))
	if code, body := audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != 503 || !strings.Contains(string(body), "SARVAM_API_KEY") || *hits != 0 {
		t.Fatalf("no key %d %s hits %d", code, body, *hits)
	}
	// No keys at all: the scripted model answers, as for any audio model.
	env, _ = sarvamEnv(t, f, map[string]string{})
	env.setPlan(t, "oa-1", schemas.FaultPlan{RunID: "r2"})
	ct, form = sttForm(t, "saaras:v4", "en", voice.WAV(voice.ToneEncode("hello", 0, 0), 16000))
	if code, body := audioCall(t, env, "/v1/audio/transcriptions", ct, form); code != 200 || !strings.Contains(string(body), `"text":"hello"`) {
		t.Fatalf("scripted %d %s", code, body)
	}
	if es := env.entries(t); es[0].Upstream != schemas.LedgerUpstreamScripted {
		t.Fatalf("scripted ledger %+v", es[0])
	}
	if stt, tts := f.calls(); stt != 0 || tts != 0 {
		t.Fatal("Sarvam was called without a key")
	}
}

func TestSarvamConfig(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "providers.yaml")
	_ = os.WriteFile(file, []byte(`providers:
  - name: openai
    base_url: https://xcodebest.com
    key_env: OPENAI_API_KEY
    auth: {header: Authorization, prefix: "Bearer "}
    format: openai
    message: ["POST /v1/chat/completions"]
    audio: ["POST /v1/audio/transcriptions", "POST /v1/audio/speech"]
    sarvam_audio: {speaker: anand, models: ["saaras:v4", "bulbul:*"]}
`), 0o600)
	provs, err := LoadProviders(file)
	if err != nil {
		t.Fatal(err)
	}
	var sa *SarvamAudio
	for _, p := range provs {
		if p.Name == ProviderOpenAI {
			sa = p.SarvamAudio
		}
	}
	if sa == nil || sa.Speaker != "anand" || sa.base() != defaultSarvamBaseURL || sa.keyEnv() != "SARVAM_API_KEY" {
		t.Fatalf("parsed %+v", sa)
	}
	if !sa.matches("openai/saaras:v4") || sa.matches("saaras:v3") || !sa.matches("bulbul:v3") || sa.matches("whisper-1") {
		t.Fatal("model patterns")
	}
	for in, want := range map[string]string{"": "en-IN", "en": "en-IN", "HI": "hi-IN", "ta-IN": "ta-IN", "unknown": "unknown"} {
		if got := sa.language(in); got != want {
			t.Errorf("language(%q) = %q, want %q", in, got, want)
		}
	}
	bad := ProviderSpec{Name: "g", BaseURL: "https://x", KeyEnv: "K", Auth: AuthGoogKey, Format: FormatGemini, Message: []string{"POST /m"}, SarvamAudio: &SarvamAudio{}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "sarvam_audio needs format openai") {
		t.Fatalf("gemini with sarvam_audio: %v", err)
	}
}
