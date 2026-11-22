package voice

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Event is one line of the caller's timeline.jsonl. Fields not used by a
// given event stay zero.
type Event struct {
	TMS            float64         `json:"t_ms"`
	Ev             string          `json:"ev"`
	Dir            string          `json:"dir,omitempty"`
	Seq            int             `json:"seq,omitempty"`
	PlayMS         float64         `json:"play_ms,omitempty"`
	RecvMS         float64         `json:"recv_ms,omitempty"`
	MS             float64         `json:"ms,omitempty"`
	Voiced         bool            `json:"voiced,omitempty"`
	Turn           int             `json:"turn,omitempty"`
	Utt            int             `json:"utt,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	Final          bool            `json:"final,omitempty"`
	Text           *string         `json:"text,omitempty"`
	TTS            string          `json:"tts,omitempty"`
	STT            string          `json:"stt,omitempty"`
	Role           string          `json:"role,omitempty"`
	StartMS        float64         `json:"start_ms,omitempty"`
	EndMS          float64         `json:"end_ms,omitempty"`
	AudioMS        float64         `json:"audio_ms,omitempty"`
	CallerStartMS  float64         `json:"caller_start_ms,omitempty"`
	AgentStopMS    float64         `json:"agent_stop_ms,omitempty"`
	StoppedAfterMS float64         `json:"stopped_after_ms,omitempty"`
	DroppedMS      float64         `json:"dropped_ms,omitempty"`
	By             string          `json:"by,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	EndedBy        string          `json:"ended_by,omitempty"`
	DurationMS     float64         `json:"duration_ms,omitempty"`
	WallMS         int64           `json:"wall_ms,omitempty"`
	Interrupted    bool            `json:"interrupted,omitempty"`
	CallerAudio    *AudioRef       `json:"caller_audio,omitempty"`
	AgentAudio     *AudioRef       `json:"agent_audio,omitempty"`
	Probe          json.RawMessage `json:"probe,omitempty"`
	Error          string          `json:"error,omitempty"`
}

// AudioRef is a call track the caller kept (or only hashed).
type AudioRef struct {
	File      string `json:"file,omitempty"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ErrNoCall is the timeline of a caller the agent never dialled.
var ErrNoCall = errors.New("the agent never called the voice caller")

// MaxTimelineBytes bounds what ReadTimeline reads (the export cap is 16 MiB).
const MaxTimelineBytes = 16 << 20

// ReadTimeline parses a timeline file.
func ReadTimeline(path string) ([]Event, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseTimeline(raw)
}

// ParseTimeline parses timeline JSONL; a line that does not parse is an error.
func ParseTimeline(raw []byte) ([]Event, error) {
	if len(raw) > MaxTimelineBytes {
		return nil, fmt.Errorf("timeline is %d bytes, over %d", len(raw), MaxTimelineBytes)
	}
	var out []Event
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("timeline line %d: %w", n, err)
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 && out[0].Ev == "no_call" {
		return nil, ErrNoCall
	}
	if len(out) == 0 || out[0].Ev != "call_start" {
		return nil, errors.New("timeline does not start with call_start")
	}
	return out, nil
}

// Turn is one moment the floor passed to the agent: the call's start (the
// greeting, Kind "greeting") or the end of a caller line the agent should
// answer, and when the agent's answer started.
type Turn struct {
	Kind         string   `json:"kind"`
	CallerTurn   int      `json:"caller_turn,omitempty"`
	FloorMS      float64  `json:"floor_ms"`                 // when the agent's floor began
	AgentStartMS *float64 `json:"agent_start_ms,omitempty"` // nil: no answer before the next caller line or the end
	GapMS        float64  `json:"gap_ms"`                   // silence on the agent's floor
	LatencyMS    *float64 `json:"latency_ms,omitempty"`     // set when the agent answered (not on the greeting)
	DeadAir      bool     `json:"dead_air"`
}

// BargeIn is one time the caller talked over the agent.
type BargeIn struct {
	Utt            int     `json:"utt"`
	CallerStartMS  float64 `json:"caller_start_ms"`
	AgentStopMS    float64 `json:"agent_stop_ms"`
	StoppedAfterMS float64 `json:"stopped_after_ms"`
	By             string  `json:"by"` // clear | audio_ended
	Handled        bool    `json:"handled"`
}

// Line is one caller line: what the caller said (reference text) and when.
type Line struct {
	Turn    int     `json:"turn"`
	Kind    string  `json:"kind"`
	Text    string  `json:"text"`
	StartMS float64 `json:"start_ms"`
	EndMS   float64 `json:"end_ms"`
	TTS     string  `json:"tts"`
	Final   bool    `json:"final,omitempty"`
}

// Utterance is one agent utterance as the caller heard it.
type Utterance struct {
	Utt         int     `json:"utt"`
	StartMS     float64 `json:"start_ms"`
	EndMS       float64 `json:"end_ms"`
	Text        *string `json:"text,omitempty"` // the caller's transcript; nil when nothing could read it
	STT         string  `json:"stt,omitempty"`
	Interrupted bool    `json:"interrupted,omitempty"`
}

// Reported is a transcript line the agent sent about itself.
type Reported struct {
	TMS  float64 `json:"t_ms"`
	Role string  `json:"role"`
	Text string  `json:"text"`
}

// Analysis is what the voice metrics and the run page read from a timeline.
type Analysis struct {
	Protocol     string      `json:"protocol"`
	WallStartMS  int64       `json:"wall_start_ms"`
	CallMS       float64     `json:"call_ms"`
	EndedBy      string      `json:"ended_by"`
	FirstAudioMS *float64    `json:"first_audio_ms,omitempty"`
	Turns        []Turn      `json:"turns"`
	BargeIns     []BargeIn   `json:"barge_ins"`
	DeadAirMS    float64     `json:"dead_air_ms"`
	Lines        []Line      `json:"lines"`
	Utterances   []Utterance `json:"utterances"`
	Reported     []Reported  `json:"reported,omitempty"`
	CallerFrames int         `json:"caller_frames"`
	AgentFrames  int         `json:"agent_frames"`
	Clears       int         `json:"clears"`
	// TalkOvers counts the times the agent started speaking while the
	// caller was talking (the caller's overlap events, by agent).
	TalkOvers      int       `json:"talk_overs"`
	ProtocolErrors int       `json:"protocol_errors"`
	CallerAudio    *AudioRef `json:"caller_audio,omitempty"`
	AgentAudio     *AudioRef `json:"agent_audio,omitempty"`
	BargeInBoundMS int       `json:"barge_in_bound_ms"`
	DeadAirBoundMS int       `json:"dead_air_bound_ms"`
	Complete       bool      `json:"complete"` // the timeline has its call_end
}

// Analyze reads a timeline under spec's thresholds.
func Analyze(events []Event, spec *schemas.VoiceSpec) (*Analysis, error) {
	spec = Effective(spec)
	if spec == nil {
		spec = Effective(&schemas.VoiceSpec{})
	}
	if len(events) == 0 || events[0].Ev != "call_start" {
		return nil, errors.New("timeline does not start with call_start")
	}
	a := &Analysis{WallStartMS: events[0].WallMS, BargeInBoundMS: spec.BargeInBoundMS, DeadAirBoundMS: spec.DeadAirMS,
		Turns: []Turn{}, BargeIns: []BargeIn{}, Lines: []Line{}, Utterances: []Utterance{}}
	a.Protocol = Protocol
	var agentStarts []float64
	open := map[int]*Line{}
	utt := map[int]*Utterance{}
	for _, e := range events {
		switch e.Ev {
		case "frame":
			if e.Dir == "caller" {
				a.CallerFrames++
			} else {
				a.AgentFrames++
			}
		case "first_agent_audio":
			v := e.TMS
			a.FirstAudioMS = &v
		case "agent_speech_start":
			agentStarts = append(agentStarts, e.TMS)
			u := &Utterance{Utt: e.Utt, StartMS: e.TMS}
			utt[e.Utt] = u
			a.Utterances = append(a.Utterances, *u)
		case "agent_speech_end":
			if u := utt[e.Utt]; u != nil {
				u.StartMS, u.EndMS = e.StartMS, e.EndMS
			}
		case "agent_transcript":
			if u := utt[e.Utt]; u != nil {
				u.Text, u.STT, u.Interrupted = e.Text, e.STT, e.Interrupted
			}
		case "caller_speech_start":
			l := &Line{Turn: e.Turn, Kind: e.Kind, StartMS: e.TMS, TTS: e.TTS, Final: e.Final}
			if e.Text != nil {
				l.Text = *e.Text
			}
			open[e.Turn] = l
		case "caller_speech_end":
			if l := open[e.Turn]; l != nil {
				l.EndMS = e.TMS
				a.Lines = append(a.Lines, *l)
				delete(open, e.Turn)
			}
		case "barge_in":
			a.BargeIns = append(a.BargeIns, BargeIn{Utt: e.Utt, CallerStartMS: e.CallerStartMS, AgentStopMS: e.AgentStopMS,
				StoppedAfterMS: e.StoppedAfterMS, By: e.By, Handled: e.StoppedAfterMS <= float64(spec.BargeInBoundMS)})
		case "agent_clear":
			a.Clears++
		case "overlap":
			if e.By == "agent" {
				a.TalkOvers++
			}
		case "protocol_error":
			a.ProtocolErrors++
		case "agent_reported":
			if e.Text != nil && (e.Role == "caller" || e.Role == "user") {
				a.Reported = append(a.Reported, Reported{TMS: e.TMS, Role: e.Role, Text: *e.Text})
			}
		case "call_end":
			a.Complete = true
			a.CallMS = e.DurationMS
			a.EndedBy = e.EndedBy
			a.CallerAudio, a.AgentAudio = e.CallerAudio, e.AgentAudio
		}
	}
	for i := range a.Utterances {
		if u := utt[a.Utterances[i].Utt]; u != nil {
			a.Utterances[i] = *u
		}
	}
	// agent_speech_start is logged on the caller's 10 ms tick; the utterance's
	// own start (from agent_speech_end) is the play time of its first voiced
	// frame, so it wins when the utterance ended.
	agentStarts = agentStarts[:0]
	for _, u := range a.Utterances {
		agentStarts = append(agentStarts, u.StartMS)
	}
	if len(a.Utterances) > 0 && a.FirstAudioMS != nil {
		v := a.Utterances[0].StartMS
		a.FirstAudioMS = &v
	}
	if !a.Complete {
		a.CallMS = events[len(events)-1].TMS
	}
	sort.Slice(a.Lines, func(i, j int) bool { return a.Lines[i].StartMS < a.Lines[j].StartMS })
	a.Turns = turns(a, agentStarts, spec.DeadAirMS)
	for _, t := range a.Turns {
		if t.DeadAir {
			a.DeadAirMS += t.GapMS
		}
	}
	return a, nil
}

// turns finds every moment the floor passed to the agent and how long the
// agent took to start talking. The floor passes at the call's start and at
// the end of every caller line except an interruption (the agent is already
// talking) and the caller's last line (it hangs up). A turn ends at the
// agent's next speech start, or with no answer at the caller's next line or
// the end of the call.
func turns(a *Analysis, agentStarts []float64, deadAirMS int) []Turn {
	out := []Turn{}
	nextCaller := func(after float64) float64 {
		for _, l := range a.Lines {
			if l.StartMS > after {
				return l.StartMS
			}
		}
		return a.CallMS
	}
	nextAgent := func(after, before float64) *float64 {
		for _, s := range agentStarts {
			if s >= after && s < before {
				v := s
				return &v
			}
		}
		return nil
	}
	add := func(t Turn) {
		limit := nextCaller(t.FloorMS)
		t.AgentStartMS = nextAgent(t.FloorMS, limit)
		if t.AgentStartMS != nil {
			t.GapMS = *t.AgentStartMS - t.FloorMS
			if t.Kind != "greeting" {
				l := t.GapMS
				t.LatencyMS = &l
			}
		} else {
			t.GapMS = limit - t.FloorMS
		}
		t.GapMS = math.Max(0, t.GapMS)
		t.DeadAir = t.GapMS > float64(deadAirMS)
		out = append(out, t)
	}
	add(Turn{Kind: "greeting", FloorMS: 0})
	for _, l := range a.Lines {
		if l.Kind == "interrupt" || l.Kind == "closing" || l.Final {
			continue
		}
		// An agent that was already talking when the line ended did not wait for it.
		if talkingAt(a, l.EndMS) {
			continue
		}
		add(Turn{Kind: l.Kind, CallerTurn: l.Turn, FloorMS: l.EndMS})
	}
	return out
}

func talkingAt(a *Analysis, t float64) bool {
	for _, u := range a.Utterances {
		if u.StartMS < t && (u.EndMS == 0 || u.EndMS > t) {
			return true
		}
	}
	return false
}

// Latencies are the answered turns' latencies, in time order.
func (a *Analysis) Latencies() []float64 {
	var out []float64
	for _, t := range a.Turns {
		if t.LatencyMS != nil {
			out = append(out, *t.LatencyMS)
		}
	}
	return out
}

// Percentile is the p-th percentile (0 to 100) of xs by linear
// interpolation between order statistics; NaN for no values.
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	r := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(r))
	hi := int(math.Ceil(r))
	return s[lo] + (s[hi]-s[lo])*(r-float64(lo))
}

// ---- speech-to-text error rate ----

var nonWord = regexp.MustCompile(`[^a-z0-9']+`)

// Words normalizes text for word error rate: lower case, everything but
// letters, digits and apostrophes is a separator. Number words are not
// rewritten ("forty five" and "45" differ), which the docs say.
func Words(s string) []string {
	return strings.Fields(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// EditDistance is the word-level Levenshtein distance.
func EditDistance(ref, hyp []string) int {
	prev := make([]int, len(hyp)+1)
	cur := make([]int, len(hyp)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ref); i++ {
		cur[0] = i
		for j := 1; j <= len(hyp); j++ {
			c := prev[j-1]
			if ref[i-1] != hyp[j-1] {
				c++
			}
			cur[j] = min(c, prev[j]+1, cur[j-1]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(hyp)]
}

// Hypothesis is one transcript of caller speech the agent produced, at a
// time on the call clock.
type Hypothesis struct {
	TMS  float64
	Text string
}

// WERResult is a word error rate over the caller lines that got a transcript.
type WERResult struct {
	WER        float64 `json:"wer"`
	Lines      int     `json:"lines"`      // caller lines with at least one transcript
	RefWords   int     `json:"ref_words"`  // words in those lines
	Edits      int     `json:"edits"`      // substitutions, insertions and deletions
	Unassigned int     `json:"unassigned"` // transcripts before the first caller line
	Source     string  `json:"source"`     // gateway | agent_reported
}

// WER scores the agent's transcripts against what the caller said. Each
// caller line belongs to the first transcript made at or after the line
// ended (an agent transcribes once the caller stops); a transcript that
// covers several lines (an agent that was not listening while the caller
// spoke twice) is scored against all of them, joined. Lines no transcript
// followed are not scored; transcripts no line precedes are counted as
// unassigned and not scored. ok is false when no line was scored.
func WER(lines []Line, hyps []Hypothesis) (WERResult, bool) {
	var r WERResult
	hs := append([]Hypothesis(nil), hyps...)
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].TMS < hs[j].TMS })
	ls := append([]Line(nil), lines...)
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].EndMS < ls[j].EndMS })
	next := 0
	for _, h := range hs {
		var ref []string
		scored := 0
		for next < len(ls) && ls[next].EndMS <= h.TMS {
			if w := Words(ls[next].Text); len(w) > 0 {
				ref = append(ref, w...)
				scored++
			}
			next++
		}
		if scored == 0 {
			r.Unassigned++
			continue
		}
		r.Lines += scored
		r.RefWords += len(ref)
		r.Edits += EditDistance(ref, Words(h.Text))
	}
	if r.Lines == 0 || r.RefWords == 0 {
		return r, false
	}
	r.WER = float64(r.Edits) / float64(r.RefWords)
	return r, true
}

// ---- gateway rows ----

// AudioRow is one Bifrost row of an audio route or a realtime turn, read
// from the run's raw gateway rows (gateway.raw.jsonl).
type AudioRow struct {
	ID         string    `json:"id"`
	Object     string    `json:"object"` // speech | transcription | realtime.turn
	Timestamp  time.Time `json:"timestamp"`
	Model      string    `json:"model"`
	Status     string    `json:"status"`
	Text       string    `json:"text,omitempty"` // transcription output text
	BifrostUSD *float64  `json:"bifrost_usd,omitempty"`
	LatencyMS  float64   `json:"latency_ms"`
}

// AudioObjects are the Bifrost v2.5.1 row objects of audio calls
// (docs/voice-agents.md, verified on a throwaway stack 18 Nov 2026).
var AudioObjects = map[string]bool{"speech": true, "speech_stream": true, "transcription": true, "transcription_stream": true, "realtime.turn": true}

// RawRowsFile is the run folder file the evidence stage writes with each
// gateway row's raw detail (evidence/gateway.go).
const RawRowsFile = "gateway.raw.jsonl"

// ReadAudioRows reads the audio rows of a run folder's raw gateway rows; a
// missing file is reported as an error so the caller can say unknown.
func ReadAudioRows(path string) ([]AudioRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []AudioRow
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	for sc.Scan() {
		var line struct {
			RequestID string          `json:"request_id"`
			Detail    json.RawMessage `json:"raw_detail"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || len(line.Detail) == 0 {
			continue
		}
		if r, ok := parseAudioRow(line.Detail); ok {
			if r.ID == "" {
				r.ID = line.RequestID
			}
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

func parseAudioRow(detail []byte) (AudioRow, bool) {
	var d struct {
		ID                  string          `json:"id"`
		Object              string          `json:"object"`
		Timestamp           time.Time       `json:"timestamp"`
		Model               string          `json:"model"`
		Status              string          `json:"status"`
		Cost                *float64        `json:"cost"`
		Latency             float64         `json:"latency"`
		ContentSummary      string          `json:"content_summary"`
		TranscriptionOutput json.RawMessage `json:"transcription_output"`
		Log                 json.RawMessage `json:"log"`
	}
	if json.Unmarshal(detail, &d) != nil {
		return AudioRow{}, false
	}
	if d.Object == "" && len(d.Log) > 0 {
		return parseAudioRow(d.Log)
	}
	if !AudioObjects[d.Object] {
		return AudioRow{}, false
	}
	r := AudioRow{ID: d.ID, Object: d.Object, Timestamp: d.Timestamp, Model: d.Model, Status: d.Status, BifrostUSD: d.Cost, LatencyMS: d.Latency}
	if strings.HasPrefix(d.Object, "transcription") {
		var out struct {
			Text string `json:"text"`
		}
		if len(d.TranscriptionOutput) > 0 && json.Unmarshal(d.TranscriptionOutput, &out) == nil && out.Text != "" {
			r.Text = out.Text
		} else {
			r.Text = d.ContentSummary
		}
	}
	return r, true
}

// GatewayHypotheses turns successful transcription rows into hypotheses on
// the call clock (row time minus the call's wall-clock start).
func GatewayHypotheses(rows []AudioRow, wallStartMS int64) []Hypothesis {
	var out []Hypothesis
	for _, r := range rows {
		if !strings.HasPrefix(r.Object, "transcription") || r.Status != "success" || r.Timestamp.IsZero() {
			continue
		}
		out = append(out, Hypothesis{TMS: float64(r.Timestamp.UnixMilli() - wallStartMS), Text: r.Text})
	}
	return out
}

// ReportedHypotheses are the agent's own transcripts of the caller.
func ReportedHypotheses(a *Analysis) []Hypothesis {
	var out []Hypothesis
	for _, r := range a.Reported {
		out = append(out, Hypothesis{TMS: r.TMS, Text: r.Text})
	}
	return out
}
