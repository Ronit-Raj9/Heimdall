package faultproxy

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

// X8 voice agents (docs/voice-agents.md): the audio routes of the OpenAI
// format (the Realtime WebSocket is relayed in realtime.go). A gateway probe with a model (an STT outage on whisper-1) fails
// them through the plan (plan.go KindAudio); the scripted model answers
// them with the bench's tone code, so a cascaded voice agent runs through
// the real gateway at $0. Tone code is a test codec, not speech: a scripted
// transcription reads tone-coded audio exactly and returns empty text for
// anything else, which the docs say.

const (
	openAIAudioRate     = 24000    // the rate OpenAI's speech endpoint answers wav and pcm in
	maxAudioModelField  = 256      // bytes read from a multipart model field
	maxAudioUploadBytes = 25 << 20 // OpenAI's transcription upload limit
)

// audioModel reads the model of a speech (JSON) or transcription
// (multipart form) request from its body, which the proxy already holds
// under maxRequestBody. Only the model field is read; file parts are
// skipped without being buffered again.
func audioModel(r *http.Request, body []byte) string {
	if r.Method == http.MethodGet { // a Realtime session names its model in the query
		return r.URL.Query().Get("model")
	}
	ct := r.Header.Get("Content-Type")
	mt, params, err := mime.ParseMediaType(ct)
	if err == nil && strings.HasPrefix(mt, "multipart/") && params["boundary"] != "" {
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				return ""
			}
			if part.FormName() == "model" && part.FileName() == "" {
				b, _ := io.ReadAll(io.LimitReader(part, maxAudioModelField))
				return strings.TrimSpace(string(b))
			}
		}
	}
	var req struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	return req.Model
}

// serveScriptedAudio answers an audio request from the scripted model.
func (s *Server) serveScriptedAudio(w http.ResponseWriter, r *http.Request, body []byte, e *schemas.LedgerEntry) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasSuffix(path, "/audio/speech"):
		var req struct {
			Model  string `json:"model"`
			Input  string `json:"input"`
			Format string `json:"response_format"`
		}
		if json.Unmarshal(body, &req) != nil || req.Input == "" {
			s.writeError(w, FormatOpenAI, http.StatusBadRequest, errInvalid, "speech needs a JSON body with input")
			return
		}
		pcm := voice.Resample(voice.ToneEncode(req.Input, 40, 120), voice.SampleRate, openAIAudioRate)
		switch req.Format {
		case "wav":
			w.Header().Set("Content-Type", "audio/wav")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(voice.WAV(pcm, openAIAudioRate))
		case "pcm":
			w.Header().Set("Content-Type", "audio/pcm")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pcm)
		default:
			s.writeError(w, FormatOpenAI, http.StatusBadRequest, errInvalid, "the scripted model speaks wav and pcm only, not "+orMP3(req.Format))
		}
	case strings.HasSuffix(path, "/audio/transcriptions"):
		text, seconds, ok := scriptedTranscript(r, body)
		if !ok {
			s.writeError(w, FormatOpenAI, http.StatusBadRequest, errInvalid, "transcription needs a multipart form with a WAV file")
			return
		}
		if seconds < 1 {
			seconds = 1
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"text": text, "usage": map[string]any{"type": "duration", "seconds": seconds}})
	case strings.HasSuffix(path, "/realtime"):
		s.writeError(w, FormatOpenAI, http.StatusNotImplemented, errUpstream, "the scripted model has no Realtime session; run a Realtime agent against a real or fake upstream")
	default:
		s.writeError(w, FormatOpenAI, http.StatusNotFound, errNotFound, "no scripted route for "+r.Method+" "+r.URL.Path)
	}
	_ = e // audio rows carry no token usage; the ledger line keeps path, status and timing
}

func orMP3(f string) string {
	if f == "" {
		return "mp3 (the default)"
	}
	return f
}

// scriptedTranscript decodes the tone code in a transcription upload: its
// text (empty for audio that is not tone-coded) and whole seconds of audio.
func scriptedTranscript(r *http.Request, body []byte) (string, int, bool) {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mt, "multipart/") || params["boundary"] == "" {
		return "", 0, false
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			return "", 0, false
		}
		if part.FormName() != "file" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxAudioUploadBytes+1))
		if err != nil || len(data) > maxAudioUploadBytes {
			return "", 0, false
		}
		pcm, rate, err := voice.ReadWAV(data)
		if err != nil {
			return "", 0, false
		}
		pcm = voice.Resample(pcm, rate, voice.SampleRate)
		text, _ := voice.ToneDecode(pcm)
		return text, (len(pcm)/2 + voice.SampleRate - 1) / voice.SampleRate, true
	}
}
