package faultproxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

// Sarvam AI audio (docs/voice-agents.md, "Sarvam AI speech"). An
// OpenAI-format provider entry may carry a sarvam_audio block: its audio
// requests for a Sarvam model (saaras:v4, bulbul:v3, ...) are translated by
// the fault proxy to Sarvam's REST API and the answer back to the OpenAI
// shape, so the agent keeps calling /v1/audio/transcriptions and
// /v1/audio/speech on Bifrost with its run key and Bifrost logs every call.
// Only the fault proxy holds the Sarvam key. Fault plans, call numbers and
// ledger lines are the provider's, exactly as for any other audio request.
//
// Sarvam's API (docs.sarvam.ai, checked 21 Nov 2026):
//   - POST /speech-to-text, multipart: file, model, language_code (BCP-47,
//     e.g. en-IN); answers {"request_id","transcript","language_code"}.
//   - POST /text-to-speech, JSON: text, target_language_code, model, speaker,
//     pace, speech_sample_rate, output_audio_codec (wav by default, 22050 Hz);
//     answers {"request_id","audios":[base64]}.
//   - the key rides in the api-subscription-key header.

const (
	defaultSarvamBaseURL  = "https://api.sarvam.ai"
	defaultSarvamKeyEnv   = "SARVAM_API_KEY"
	defaultSarvamLanguage = "en-IN"
	sarvamAuthHeader      = "api-subscription-key"
	maxSarvamResponse     = 64 << 20 // base64 audio of a long answer
)

// defaultSarvamModels are Sarvam's speech model families.
var defaultSarvamModels = []string{"saaras:*", "saarika:*", "bulbul:*"}

// openAIVoices are OpenAI's speech voice names. Sarvam has none of them, so a
// request naming one gets the block's speaker (or Sarvam's default).
var openAIVoices = map[string]bool{
	"alloy": true, "ash": true, "ballad": true, "coral": true, "echo": true, "fable": true,
	"onyx": true, "nova": true, "sage": true, "shimmer": true, "verse": true, "marin": true, "cedar": true,
}

// SarvamAudio routes an OpenAI-format provider's audio requests for Sarvam
// models to Sarvam AI. Empty fields take the defaults above.
type SarvamAudio struct {
	BaseURL  string   `json:"base_url,omitempty" yaml:"base_url"`
	KeyEnv   string   `json:"key_env,omitempty" yaml:"key_env"`
	Models   []string `json:"models,omitempty" yaml:"models"`     // path.Match globs on the request model, provider prefix removed
	Language string   `json:"language,omitempty" yaml:"language"` // when the request names none
	Speaker  string   `json:"speaker,omitempty" yaml:"speaker"`   // for a request naming an OpenAI voice; empty: Sarvam's default
}

func (c *SarvamAudio) base() string {
	if c.BaseURL == "" {
		return defaultSarvamBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *SarvamAudio) keyEnv() string {
	if c.KeyEnv == "" {
		return defaultSarvamKeyEnv
	}
	return c.KeyEnv
}

func (c *SarvamAudio) models() []string {
	if len(c.Models) == 0 {
		return defaultSarvamModels
	}
	return c.Models
}

// validate checks the block of provider p.
func (c *SarvamAudio) validate(p ProviderSpec) error {
	var errs []error
	if p.Format != FormatOpenAI {
		errs = append(errs, errors.New("sarvam_audio needs format openai"))
	}
	if _, err := parseBaseURL(c.base()); err != nil {
		errs = append(errs, fmt.Errorf("sarvam_audio: %w", err))
	}
	for _, m := range c.models() {
		if _, err := path.Match(m, ""); err != nil || m == "" {
			errs = append(errs, fmt.Errorf("sarvam_audio: bad model pattern %q", m))
		}
	}
	return errors.Join(errs...)
}

// matches reports whether model (with or without a provider prefix) is one
// the block serves.
func (c *SarvamAudio) matches(model string) bool {
	m := bareModel(model)
	if m == "" {
		return false
	}
	for _, pat := range c.models() {
		if ok, err := path.Match(pat, m); err == nil && ok {
			return true
		}
	}
	return false
}

// bareModel drops a Bifrost provider prefix ("openai/saaras:v4").
func bareModel(model string) string {
	model = strings.TrimSpace(model)
	if _, rest, ok := strings.Cut(model, "/"); ok {
		return rest
	}
	return model
}

// sarvamLanguage maps a request language to Sarvam's BCP-47 code: "en"
// becomes "en-IN", a full code passes, empty takes the block's default.
func (c *SarvamAudio) language(lang string) string {
	lang = strings.TrimSpace(lang)
	switch {
	case lang == "":
		if c.Language != "" {
			return c.Language
		}
		return defaultSarvamLanguage
	case lang == "unknown" || strings.Contains(lang, "-"):
		return lang
	default:
		return strings.ToLower(lang) + "-IN"
	}
}

// sarvamRoute reports whether an audio request goes to Sarvam: the provider
// has a sarvam_audio block and the request's model matches it.
func (s *Server) sarvamRoute(p ProviderSpec, r *http.Request, kind string, body []byte) bool {
	if kind != KindAudio || p.SarvamAudio == nil || r.Method != http.MethodPost {
		return false
	}
	return p.SarvamAudio.matches(audioModel(r, body))
}

// serveSarvam answers one audio request through Sarvam's API.
func (s *Server) serveSarvam(w http.ResponseWriter, r *http.Request, p ProviderSpec, up upstream, body []byte, e *schemas.LedgerEntry) {
	if up.sarvamKey == "" {
		s.writeError(w, FormatOpenAI, http.StatusServiceUnavailable, errUpstream,
			fmt.Sprintf("fault proxy: no key configured for Sarvam audio of provider %s (set %s)", p.Name, p.SarvamAudio.keyEnv()))
		return
	}
	e.Upstream = schemas.LedgerUpstreamReal
	route := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(route, "/audio/transcriptions"):
		s.sarvamTranscribe(w, r, p, up, body, e)
	case strings.HasSuffix(route, "/audio/speech"):
		s.sarvamSpeak(w, r, p, up, body, e)
	default:
		s.writeError(w, FormatOpenAI, http.StatusNotFound, errNotFound, "Sarvam audio has no route for "+r.Method+" "+r.URL.Path)
	}
}

// sarvamTranscribe turns an OpenAI transcription form into Sarvam's
// speech-to-text form and the transcript back into {"text": ...}.
func (s *Server) sarvamTranscribe(w http.ResponseWriter, r *http.Request, p ProviderSpec, up upstream, body []byte, e *schemas.LedgerEntry) {
	in, err := readForm(r, body)
	if err != nil || len(in.file) == 0 {
		s.writeError(w, FormatOpenAI, http.StatusBadRequest, errInvalid, "transcription needs a multipart form with a file")
		return
	}
	var out bytes.Buffer
	mw := multipart.NewWriter(&out)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, in.filename))
	h.Set("Content-Type", in.fileType)
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(in.file)
	_ = mw.WriteField("model", bareModel(in.fields["model"]))
	_ = mw.WriteField("language_code", p.SarvamAudio.language(in.fields["language"]))
	_ = mw.Close()

	raw, status, ok := s.sarvamCall(w, r, p, up, "/speech-to-text", mw.FormDataContentType(), out.Bytes(), e)
	if !ok {
		return
	}
	if status != http.StatusOK {
		s.writeSarvamError(w, status, raw)
		return
	}
	var resp struct {
		Transcript   string  `json:"transcript"`
		LanguageCode *string `json:"language_code"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		s.writeError(w, FormatOpenAI, http.StatusBadGateway, errUpstream, "Sarvam speech-to-text answered something that is not its JSON")
		return
	}
	if in.fields["response_format"] == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, resp.Transcript)
		return
	}
	answer := map[string]any{"text": resp.Transcript}
	if resp.LanguageCode != nil && *resp.LanguageCode != "" {
		answer["language"] = *resp.LanguageCode
	}
	if pcm, rate, err := voice.ReadWAV(in.file); err == nil && rate > 0 {
		answer["usage"] = map[string]any{"type": "duration", "seconds": (len(pcm)/2 + rate - 1) / rate}
	}
	s.writeJSON(w, http.StatusOK, answer)
}

// sarvamCodecs maps an OpenAI speech response_format to Sarvam's
// output_audio_codec, the sample rate to ask for (0: Sarvam's default) and the
// Content-Type of the answer. OpenAI's pcm and opus are 24 kHz.
var sarvamCodecs = map[string]struct {
	codec string
	rate  int
	ctype string
}{
	"wav":  {"wav", 0, "audio/wav"},
	"mp3":  {"mp3", 0, "audio/mpeg"},
	"flac": {"flac", 0, "audio/flac"},
	"aac":  {"aac", 0, "audio/aac"},
	"opus": {"opus", 24000, "audio/opus"},
	"pcm":  {"linear16", 24000, "audio/pcm"},
}

// sarvamSpeak turns an OpenAI speech request into Sarvam's text-to-speech
// request and the base64 audio back into the audio bytes.
func (s *Server) sarvamSpeak(w http.ResponseWriter, r *http.Request, p ProviderSpec, up upstream, body []byte, e *schemas.LedgerEntry) {
	var req struct {
		Model        string   `json:"model"`
		Input        string   `json:"input"`
		Voice        string   `json:"voice"`
		Format       string   `json:"response_format"`
		Speed        *float64 `json:"speed"`
		LanguageCode string   `json:"language_code"`
	}
	if json.Unmarshal(body, &req) != nil || strings.TrimSpace(req.Input) == "" {
		s.writeError(w, FormatOpenAI, http.StatusBadRequest, errInvalid, "speech needs a JSON body with input")
		return
	}
	format := req.Format
	if format == "" {
		format = "mp3" // OpenAI's default
	}
	codec, ok := sarvamCodecs[format]
	if !ok {
		s.writeError(w, FormatOpenAI, http.StatusBadRequest, errInvalid, "Sarvam speech has no response_format "+format)
		return
	}
	out := map[string]any{
		"text":                 req.Input,
		"target_language_code": p.SarvamAudio.language(req.LanguageCode),
		"model":                bareModel(req.Model),
		"output_audio_codec":   codec.codec,
	}
	if speaker := req.Voice; speaker != "" && !openAIVoices[strings.ToLower(speaker)] {
		out["speaker"] = speaker
	} else if p.SarvamAudio.Speaker != "" {
		out["speaker"] = p.SarvamAudio.Speaker
	}
	if req.Speed != nil {
		out["pace"] = *req.Speed
	}
	if codec.rate > 0 {
		out["speech_sample_rate"] = codec.rate
	}
	payload, _ := json.Marshal(out)

	raw, status, ok := s.sarvamCall(w, r, p, up, "/text-to-speech", "application/json", payload, e)
	if !ok {
		return
	}
	if status != http.StatusOK {
		s.writeSarvamError(w, status, raw)
		return
	}
	var resp struct {
		Audios []string `json:"audios"`
	}
	if json.Unmarshal(raw, &resp) != nil || len(resp.Audios) == 0 || resp.Audios[0] == "" {
		s.writeError(w, FormatOpenAI, http.StatusBadGateway, errUpstream, "Sarvam text-to-speech answered no audio")
		return
	}
	audio, err := base64.StdEncoding.DecodeString(resp.Audios[0])
	if err != nil {
		s.writeError(w, FormatOpenAI, http.StatusBadGateway, errUpstream, "Sarvam text-to-speech audio is not base64")
		return
	}
	w.Header().Set("Content-Type", codec.ctype)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

// sarvamCall posts one request to Sarvam with the real key and returns the
// body and status. A transport failure is answered (502) and recorded on the
// ledger line like forward does; ok is false then.
func (s *Server) sarvamCall(w http.ResponseWriter, r *http.Request, p ProviderSpec, up upstream, route, ctype string, payload []byte, e *schemas.LedgerEntry) ([]byte, int, bool) {
	out, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.SarvamAudio.base()+route, bytes.NewReader(payload))
	if err != nil {
		e.UpstreamError = "build request: " + redactURLError(err)
		s.writeError(w, FormatOpenAI, http.StatusBadGateway, errUpstream, "fault proxy could not build the Sarvam request")
		return nil, 0, false
	}
	out.Header.Set("Content-Type", ctype)
	out.Header.Set("Accept", "application/json")
	out.Header.Set(sarvamAuthHeader, up.sarvamKey)
	resp, err := s.client.Do(out)
	if err != nil {
		msg := redactURLError(err)
		s.logger.Warn("sarvam request failed", "slot", e.Slot, "provider", p.Name, "path", route, "error", msg)
		if r.Context().Err() == nil {
			e.UpstreamError = msg
		}
		s.writeError(w, FormatOpenAI, http.StatusBadGateway, errUpstream, "fault proxy could not reach Sarvam")
		return nil, 0, false
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSarvamResponse+1))
	if err != nil || len(raw) > maxSarvamResponse {
		if r.Context().Err() == nil {
			e.UpstreamError = "read Sarvam response"
		}
		s.writeError(w, FormatOpenAI, http.StatusBadGateway, errUpstream, "fault proxy could not read the Sarvam response")
		return nil, 0, false
	}
	return raw, resp.StatusCode, true
}

// writeSarvamError relays a Sarvam error with its status in the OpenAI
// envelope, so Bifrost's openai provider parses it as a provider error.
func (s *Server) writeSarvamError(w http.ResponseWriter, status int, raw []byte) {
	var env struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	msg := "Sarvam answered " + strconv.Itoa(status)
	if json.Unmarshal(raw, &env) == nil {
		switch {
		case env.Error != nil && env.Error.Message != "":
			msg = "Sarvam: " + env.Error.Message
		case len(env.Detail) > 0:
			msg = "Sarvam: " + string(env.Detail)
		}
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	cat := errUpstream
	if status >= 400 && status < 500 {
		cat = errInvalid
	}
	s.writeError(w, FormatOpenAI, status, cat, msg)
}

// form is a parsed multipart transcription request.
type form struct {
	fields   map[string]string
	file     []byte
	filename string
	fileType string
}

func readForm(r *http.Request, body []byte) (form, error) {
	f := form{fields: map[string]string{}}
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return f, errors.New("not a multipart form")
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return f, nil
		}
		if err != nil {
			return f, err
		}
		if part.FileName() != "" || part.FormName() == "file" {
			data, err := io.ReadAll(io.LimitReader(part, maxAudioUploadBytes+1))
			if err != nil || len(data) > maxAudioUploadBytes {
				return f, errors.New("file part too large or unreadable")
			}
			f.file, f.filename, f.fileType = data, part.FileName(), part.Header.Get("Content-Type")
			if f.filename == "" {
				f.filename = "audio.wav"
			}
			if f.fileType == "" {
				f.fileType = "application/octet-stream"
			}
			continue
		}
		v, _ := io.ReadAll(io.LimitReader(part, maxAudioModelField))
		f.fields[part.FormName()] = strings.TrimSpace(string(v))
	}
}
