package voice

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The fixtures in testdata are timelines voice_test.py recorded from real
// calls between voicecaller.py and a tone-code agent (VOICE_WRITE_FIXTURES).

func analyze(t *testing.T, name string, spec *schemas.VoiceSpec) *Analysis {
	t.Helper()
	ev, err := ReadTimeline(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Analyze(ev, spec)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAnalyzeGoodCall(t *testing.T) {
	a := analyze(t, "call-good.jsonl", nil)
	if !a.Complete || a.EndedBy != "caller_done" || a.CallMS <= 0 {
		t.Fatalf("call: complete %v ended %q ms %v", a.Complete, a.EndedBy, a.CallMS)
	}
	if a.FirstAudioMS == nil || *a.FirstAudioMS > 500 {
		t.Fatalf("first audio %v", a.FirstAudioMS)
	}
	if len(a.Lines) != 2 || len(a.Utterances) != 2 {
		t.Fatalf("lines %d utterances %d", len(a.Lines), len(a.Utterances))
	}
	// Greeting, then the caller's opening; its last line ends the call and opens no turn.
	if len(a.Turns) != 2 || a.Turns[0].Kind != "greeting" || a.Turns[1].LatencyMS == nil {
		t.Fatalf("turns %+v", a.Turns)
	}
	if l := *a.Turns[1].LatencyMS; l <= 0 || l > 5000 {
		t.Fatalf("latency %v", l)
	}
	if a.DeadAirMS != 0 || len(a.BargeIns) != 0 {
		t.Fatalf("dead air %v barge-ins %d", a.DeadAirMS, len(a.BargeIns))
	}
	if a.CallerAudio == nil || len(a.CallerAudio.SHA256) != 64 || a.AgentAudio == nil {
		t.Fatalf("audio refs %+v %+v", a.CallerAudio, a.AgentAudio)
	}
	if a.CallerFrames == 0 || a.AgentFrames == 0 {
		t.Fatalf("frames %d %d", a.CallerFrames, a.AgentFrames)
	}
	if len(a.Reported) != 1 {
		t.Fatalf("reported %+v", a.Reported)
	}
	r, ok := WER(a.Lines, ReportedHypotheses(a))
	if !ok || r.WER != 0 || r.Lines != 1 {
		t.Fatalf("wer of an exact transcript: %+v %v", r, ok)
	}
}

func TestAnalyzeBargeIn(t *testing.T) {
	h := analyze(t, "call-barge-handled.jsonl", nil)
	if len(h.BargeIns) != 1 || !h.BargeIns[0].Handled || h.BargeIns[0].By != "clear" || h.Clears == 0 {
		t.Fatalf("handled: %+v clears %d", h.BargeIns, h.Clears)
	}
	g := analyze(t, "call-barge-ignored.jsonl", nil)
	if len(g.BargeIns) != 1 || g.BargeIns[0].Handled || g.BargeIns[0].StoppedAfterMS < 1000 {
		t.Fatalf("ignored: %+v", g.BargeIns)
	}
	// A looser bound changes the verdict on the same call.
	if loose := analyze(t, "call-barge-ignored.jsonl", &schemas.VoiceSpec{BargeInBoundMS: 10000}); !loose.BargeIns[0].Handled {
		t.Fatal("bound not applied")
	}
	// The interruption opens no turn: the agent was talking.
	for _, tr := range g.Turns {
		if tr.Kind == "interrupt" {
			t.Fatalf("interrupt opened a turn: %+v", g.Turns)
		}
	}
}

func TestAnalyzeSilentAgent(t *testing.T) {
	a := analyze(t, "call-silent-agent.jsonl", nil)
	if a.FirstAudioMS != nil || a.EndedBy != "agent_silent" {
		t.Fatalf("first audio %v ended %q", a.FirstAudioMS, a.EndedBy)
	}
	if len(a.Latencies()) != 0 {
		t.Fatalf("latencies %v", a.Latencies())
	}
	// The greeting floor (until the caller opened) and the opening's floor
	// (until the reprompt) are both longer than the default 2 s.
	if a.DeadAirMS < 3000 {
		t.Fatalf("dead air %v, turns %+v", a.DeadAirMS, a.Turns)
	}
	lax := analyze(t, "call-silent-agent.jsonl", &schemas.VoiceSpec{DeadAirMS: 60000})
	if lax.DeadAirMS != 0 {
		t.Fatalf("dead air above the bound: %v", lax.DeadAirMS)
	}
}

func TestParseTimelineRefusals(t *testing.T) {
	if _, err := ParseTimeline([]byte(`{"ev": "no_call", "t_ms": 0}` + "\n")); err != ErrNoCall {
		t.Fatalf("no call: %v", err)
	}
	if _, err := ParseTimeline([]byte(`{"ev": "frame"}` + "\n")); err == nil {
		t.Fatal("timeline without call_start accepted")
	}
	if _, err := ParseTimeline([]byte(`{"ev": "call_start"}` + "\nnot json\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("bad line: %v", err)
	}
	// A call cut short (no call_end) is analysed but marked incomplete.
	ev, err := ParseTimeline([]byte(`{"ev": "call_start", "t_ms": 0}` + "\n" + `{"ev": "caller_speech_start", "t_ms": 100, "turn": 1, "kind": "opening", "text": "hi"}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Analyze(ev, nil)
	if err != nil || a.Complete {
		t.Fatalf("incomplete call: %v %+v", err, a)
	}
}

func TestPercentile(t *testing.T) {
	xs := []float64{400, 100, 300, 200}
	if Percentile(xs, 50) != 250 || Percentile(xs, 95) != 385 || Percentile(xs, 0) != 100 || Percentile([]float64{7}, 95) != 7 {
		t.Fatalf("percentiles %v %v", Percentile(xs, 50), Percentile(xs, 95))
	}
	if !math.IsNaN(Percentile(nil, 50)) {
		t.Fatal("empty percentile")
	}
}

func TestWER(t *testing.T) {
	lines := []Line{{Turn: 1, Text: "Hi, my order is B-3001.", StartMS: 1000, EndMS: 2500}, {Turn: 2, Text: "Great, thank you!", StartMS: 5000, EndMS: 6000}}
	hyps := []Hypothesis{{TMS: 500, Text: "early"}, {TMS: 3000, Text: "hi my order is b 3,001"}, {TMS: 6500, Text: "great thank you"}}
	r, ok := WER(lines, hyps)
	// ref words: hi my order is b 3001 (6) + great thank you (3); "3,001" splits into "3" "001": 1 substitution, 1 insertion.
	if !ok || r.Lines != 2 || r.RefWords != 9 || r.Edits != 2 || r.Unassigned != 1 {
		t.Fatalf("wer %+v", r)
	}
	if _, ok := WER(lines, nil); ok {
		t.Fatal("no hypothesis must give no WER")
	}
	// One transcript after both lines (the agent was not listening in between) is scored against both, joined.
	merged, ok := WER(lines, []Hypothesis{{TMS: 7000, Text: "hi my order is b 3001 great thank you"}})
	if !ok || merged.Lines != 2 || merged.Edits != 0 {
		t.Fatalf("merged %+v", merged)
	}
	if EditDistance(Words("a b c"), Words("a x c d")) != 2 {
		t.Fatal("edit distance")
	}
}

func TestAudioRows(t *testing.T) {
	dir := t.TempDir()
	// Rows as Bifrost v2.5.1 logged them on the hd-x8 stack (abridged), one
	// chat row that is not audio, and a line without detail.
	raw := `{"request_id":"r1","raw_available":true,"raw_detail":{"id":"r1","object":"transcription","timestamp":"2026-11-18T02:18:21.688561Z","model":"whisper-1","status":"success","cost":0.0001,"latency":53.2,"content_summary":"my order is B-3001","transcription_output":{"text":"my order is B-3001","usage":{"type":"duration","seconds":1}}}}
{"request_id":"r2","raw_available":true,"raw_detail":{"id":"r2","object":"speech","timestamp":"2026-11-18T02:18:21.48Z","model":"gpt-4o-mini-tts","status":"success","cost":0.000018,"latency":86.2,"content_summary":"Hello"}}
{"request_id":"r3","raw_available":true,"raw_detail":{"id":"r3","object":"chat_completion","timestamp":"2026-11-18T02:18:21.9Z","model":"gpt-4o-mini","status":"success","cost":0.00001}}
{"request_id":"r4","raw_available":true,"raw_detail":{"id":"r4","object":"realtime.turn","timestamp":"2026-11-18T02:18:34.88Z","model":"gpt-realtime","status":"success","cost":0.000136}}
{"request_id":"r5","raw_available":false,"raw_detail":null}
`
	p := filepath.Join(dir, RawRowsFile)
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadAudioRows(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Text != "my order is B-3001" || rows[1].Object != "speech" || *rows[2].BifrostUSD != 0.000136 {
		t.Fatalf("rows %+v", rows)
	}
	wall := time.Date(2026, 11, 18, 2, 18, 20, 0, time.UTC).UnixMilli()
	h := GatewayHypotheses(rows, wall)
	if len(h) != 1 || h[0].Text != "my order is B-3001" || h[0].TMS < 1600 || h[0].TMS > 1700 {
		t.Fatalf("hypotheses %+v", h)
	}
}
