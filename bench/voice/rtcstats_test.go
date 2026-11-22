package voice

import (
	"os"
	"path/filepath"
	"testing"
)

// call-livekit-webrtc.jsonl is the first nine seconds of a real call: the
// caller (tone code) through rtcbridge.py and rtcgateway.py into a LiveKit
// v1.13.10 room, with a webhook-started bot answering in tone code over
// WebRTC (Opus both ways), plus its call_end.
func TestParseCallStatsLiveKit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "call-livekit-webrtc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseCallStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !st.RealTime || st.RTCNote == "" {
		t.Fatalf("real time %v note %q", st.RealTime, st.RTCNote)
	}
	if st.JitterP95MS == nil || len(st.JitterMS) < 100 || *st.JitterP95MS > 50 {
		t.Fatalf("jitter %v over %d frames", st.JitterP95MS, len(st.JitterMS))
	}
	events, err := ParseTimeline(raw)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Analyze(events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.FirstAudioMS == nil || len(a.Latencies()) == 0 || len(a.Utterances) < 2 || a.Utterances[0].Text == nil || *a.Utterances[0].Text != "Hello, thanks for calling Acme. How can I help?" {
		t.Fatalf("analysis first %v latencies %v utterances %+v", a.FirstAudioMS, a.Latencies(), a.Utterances)
	}
}

func TestParseCallStatsPersona(t *testing.T) {
	raw := []byte(`{"ev":"call_start","t_ms":0,"persona":{"id":"hurried","voice_applied":"not_applied"},"vad":"energy"}
{"ev":"agent_speech_start","t_ms":10,"utt":1}
{"ev":"frame","dir":"agent","seq":1,"recv_ms":10,"play_ms":10,"ms":20,"voiced":true,"t_ms":10}
{"ev":"frame","dir":"agent","seq":2,"recv_ms":34,"play_ms":30,"ms":20,"voiced":true,"t_ms":34}
{"ev":"frame","dir":"agent","seq":3,"recv_ms":50,"play_ms":50,"ms":20,"voiced":true,"t_ms":50}
{"ev":"frame","dir":"agent","seq":4,"recv_ms":900,"play_ms":900,"ms":20,"voiced":true,"t_ms":900}
{"ev":"barge_in_start","t_ms":60,"by":"persona"}
{"ev":"barge_in_start","t_ms":70,"by":"probe"}
{"ev":"agent_speech_end","t_ms":1000,"utt":1}
`)
	st, err := ParseCallStats(raw)
	if err != nil {
		t.Fatal(err)
	}
	if st.Persona != "hurried" || st.PersonaVoice != "not_applied" || st.PersonaBarges != 1 || st.ProbeBarges != 1 || st.RealTime || st.VAD != "energy" {
		t.Fatalf("stats %+v", st)
	}
	// Gaps: 34-(10+20)=4, 50-(34+20)=-4, the 830 ms pause is speech, not jitter.
	if len(st.JitterMS) != 2 || st.JitterMS[0] != 4 || st.JitterMS[1] != 4 {
		t.Fatalf("jitter %v", st.JitterMS)
	}
	if _, err := ParseCallStats([]byte("{not json\n")); err == nil {
		t.Fatal("bad line accepted")
	}
}
