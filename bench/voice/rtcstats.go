package voice

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// Real-time call statistics read from the caller's timeline
// (docs/voice-realtime.md): how the agent's audio arrived (jitter), which
// persona the caller played, how often it talked over the agent, and
// whether the call went over a real-time bridge (WebRTC or SIP through the
// voice gateway). All of it is the caller's own record of what it played.

// RTCReady is the event name the real-time bridge reports once the agent
// is in the room (rtcbridge.py).
const RTCReady = "rtc_ready"

// jitterGapMS is a pause in the agent's audio long enough to be a pause in
// its speech, not network jitter.
const jitterGapMS = 200

// CallStats is what the real-time metrics and the report card read.
type CallStats struct {
	// JitterMS lists, for each agent audio frame inside an utterance after
	// the first, how far its arrival strayed from the previous frame's
	// arrival plus that frame's length.
	JitterMS []float64 `json:"-"`
	// JitterP95MS is the 95th percentile of JitterMS; nil without frames.
	JitterP95MS   *float64 `json:"jitter_p95_ms,omitempty"`
	Persona       string   `json:"persona,omitempty"`
	PersonaVoice  string   `json:"persona_voice,omitempty"` // piper, default or not_applied (tone code)
	PersonaBarges int      `json:"persona_barges"`
	ProbeBarges   int      `json:"probe_barges"`
	RealTime      bool     `json:"real_time"` // the call went through the real-time bridge
	RTCNote       string   `json:"rtc_note,omitempty"`
	VAD           string   `json:"vad,omitempty"`
}

type statsEvent struct {
	Ev      string   `json:"ev"`
	Dir     string   `json:"dir"`
	RecvMS  *float64 `json:"recv_ms"`
	MS      float64  `json:"ms"`
	Voiced  bool     `json:"voiced"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Text    *string  `json:"text"`
	By      string   `json:"by"`
	VAD     string   `json:"vad"`
	Persona *struct {
		ID           string `json:"id"`
		VoiceApplied string `json:"voice_applied"`
	} `json:"persona"`
}

// ParseCallStats reads the real-time statistics of a timeline.
func ParseCallStats(raw []byte) (CallStats, error) {
	var st CallStats
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	speaking := false
	var lastRecv, lastMS float64
	haveLast := false
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		var e statsEvent
		if err := json.Unmarshal(b, &e); err != nil {
			return st, fmt.Errorf("timeline line %d: %w", line, err)
		}
		switch e.Ev {
		case "call_start":
			if e.Persona != nil {
				st.Persona, st.PersonaVoice = e.Persona.ID, e.Persona.VoiceApplied
			}
			st.VAD = e.VAD
		case "agent_speech_start":
			speaking, haveLast = true, false
		case "agent_speech_end":
			speaking, haveLast = false, false
		case "barge_in_start":
			if e.By == "persona" {
				st.PersonaBarges++
			} else {
				st.ProbeBarges++
			}
		case "agent_reported":
			if e.Kind == "event" && e.Name == RTCReady {
				st.RealTime = true
				if e.Text != nil {
					st.RTCNote = *e.Text
				}
			}
		case "frame":
			if e.Dir != "agent" || e.RecvMS == nil {
				continue
			}
			if speaking && haveLast {
				gap := *e.RecvMS - (lastRecv + lastMS)
				if math.Abs(gap) < jitterGapMS {
					st.JitterMS = append(st.JitterMS, math.Abs(gap))
				}
			}
			lastRecv, lastMS, haveLast = *e.RecvMS, e.MS, speaking
		}
	}
	if err := sc.Err(); err != nil {
		return st, err
	}
	if len(st.JitterMS) > 0 {
		p := math.Round(Percentile(st.JitterMS, 95)*10) / 10
		st.JitterP95MS = &p
	}
	return st, nil
}
