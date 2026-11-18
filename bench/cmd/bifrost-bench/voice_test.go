package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

// X8 voice agents: voice check.

func TestVoiceSuiteRows(t *testing.T) {
	caller := schemas.ServiceSpec{ID: "customer", Role: schemas.ServiceRoleUser, Image: "img:1",
		Export: []schemas.StateExport{{ID: voice.TimelineExport}, {ID: voice.TranscriptExport}}}
	good := &schemas.Task{ID: "good", Voice: &schemas.VoiceSpec{Probes: []string{"noise"}}, Services: []schemas.ServiceSpec{caller}}
	bad := &schemas.Task{ID: "bad", Voice: &schemas.VoiceSpec{Transport: "sip"}, Services: []schemas.ServiceSpec{caller}}
	plain := &schemas.Task{ID: "plain"}
	s := &schemas.Suite{ID: "s", Tasks: []*schemas.Task{good, bad, plain}}
	inspected := 0
	rows := voiceSuiteRows(context.Background(), s, func(_ context.Context, image string) (string, bool) {
		inspected++
		return "probed " + image, true
	})
	if len(rows) != 3 || !rows[0].OK || rows[1].OK || rows[2].Check != "caller image img:1" || inspected != 1 {
		t.Fatalf("rows %+v", rows)
	}
	if !strings.Contains(rows[0].Detail, "probes: noise") || !strings.Contains(rows[1].Detail, "telephony") {
		t.Fatalf("details %q / %q", rows[0].Detail, rows[1].Detail)
	}
	if none := voiceSuiteRows(context.Background(), &schemas.Suite{Tasks: []*schemas.Task{plain}}, nil); len(none) != 1 || none[0].OK {
		t.Fatalf("no voice task: %+v", none)
	}
}

func TestCallerImageVerdict(t *testing.T) {
	all := map[string]any{"voicecaller.py": true, "tonecode.py": true, "wsproto.py": true, "voiceclient.py": true, "simuser.py": true,
		"piper": true, "whisper": true, "faster_whisper": true, "protocol": voice.Protocol}
	if d, ok := callerImageVerdict(all); !ok || !strings.Contains(d, "Piper and Whisper present") {
		t.Fatalf("full image: %q %v", d, ok)
	}
	all["whisper"] = false
	if d, ok := callerImageVerdict(all); !ok || !strings.Contains(d, "tone-coded agent audio only") {
		t.Fatalf("no whisper: %q %v", d, ok)
	}
	all["protocol"] = "heimdall-voice/0"
	if _, ok := callerImageVerdict(all); ok {
		t.Fatal("other protocol accepted")
	}
	all["protocol"], all["voicecaller.py"] = voice.Protocol, false
	if d, ok := callerImageVerdict(all); ok || !strings.Contains(d, "/srv/voicecaller.py") {
		t.Fatalf("missing caller: %q", d)
	}
}

func TestPrintVoiceAnalysis(t *testing.T) {
	ev, err := voice.ReadTimeline("../../voice/testdata/call-barge-handled.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	a, err := voice.Analyze(ev, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	printVoiceAnalysis(&b, a)
	for _, want := range []string{"time to first audio:", "turn latency: p50", "barge-in on utterance 2", "handled true", "caller 2 (interrupt"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, b.String())
		}
	}
}
