// Package shadowreplay replays recorded production sessions
// (replayrec.Recording) against another model and compares, step by step,
// what the new model would have done. No tool is ever run: in trajectory
// mode every tool call is answered from the recording (replayrec.Tape), and
// a call the recording cannot answer ends the session. A request that holds
// redacted text is never sent. There is no fallback to a live endpoint or
// to a nearest recorded answer.
package shadowreplay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/replayrec"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Result states (exact strings).
const (
	StateEquivalent    = "equivalent_response"           // same tool calls (names and canonical arguments), or the same final text after Normalize, or a judge said equivalent (Basis "judge")
	StateDifferentText = "different_response"            // both answered in text and the texts differ
	StateDifferentTool = "different_tool_call"           // a tool call differs in name, arguments, number, or one side called a tool and the other did not
	StateMissingTool   = "missing_recorded_tool_result"  // trajectory: the new model asked a tool call the recording cannot answer; the session ends here
	StateRedacted      = "redacted_input"                // the recorded request holds redacted text, so it is not the real input; not sent
	StateInsufficient  = "insufficient_evidence"         // the recording lacks what is needed: no stored request, no tool definitions while the answer used tools, or the step limit was reached
	StateInfraFailure  = "replay_infrastructure_failure" // the gateway or the model call failed (timeout, 5xx, budget refused mid-session)
)

// Modes.
const (
	ModeFixed      = "fixed"
	ModeTrajectory = "trajectory"
)

// Note is on every run report.
const Note = "A replay comparison of recorded sessions. It is not a benchmark result: nothing was run for real and nothing was graded by a task's tests."

// JudgeSystem is the judge's instruction.
const JudgeSystem = "Two assistants answered the same conversation. Say whether the second answer gives the user the same information and takes the same action as the first. Reply as JSON {\"equivalent\": true|false, \"reason\": \"...\"}."

const judgeSchema = `{"type":"object","properties":{"equivalent":{"type":"boolean"},"reason":{"type":"string"}},"required":["equivalent","reason"]}`

// Plain sentences shown in a step.
const (
	textNoRequest   = "The recording has no stored request for this call."
	textNoToolDefs  = "The stored request has no tool definitions, so the model could not call the same tools."
	textRedacted    = "Not sent: the recorded input was cleaned of private data."
	textStepLimit   = "The step limit was reached before the new model finished."
	textBudget      = "The spending cap for this replay was reached."
	maxShown        = 400
	defaultMaxSteps = 30
	maxMaxSteps     = 60
)

// Options of one replay.
type Options struct {
	Candidate string // provider/model of the new model, written into each request
	MaxSteps  int    // trajectory: default 30, at most 60
}

// StepResult is one compared step.
type StepResult struct {
	Index             int      `json:"index"`
	State             string   `json:"state"`
	Basis             string   `json:"basis,omitempty"` // "exact" | "judge"
	Recorded          string   `json:"recorded"`        // text or "calls: a(...), b(...)", at most 400 chars
	New               string   `json:"new,omitempty"`
	JudgeNote         string   `json:"judge_note,omitempty"`
	CostUSD           float64  `json:"cost_usd"`
	LatencyMS         int64    `json:"latency_ms"`
	RecordedCostUSD   *float64 `json:"recorded_cost_usd,omitempty"`
	RecordedLatencyMS *float64 `json:"recorded_latency_ms,omitempty"`
}

// SessionResult is the replay of one recording.
type SessionResult struct {
	Recording       string       `json:"recording"`
	SessionID       string       `json:"session_id"`
	Steps           []StepResult `json:"steps"`
	Final           string       `json:"final"` // the state of the last compared step
	DivergedAt      *int         `json:"diverged_at,omitempty"`
	ToolAgreement   *float64     `json:"tool_agreement,omitempty"`  // equivalent steps with tool calls / recorded steps with tool calls
	WriteSetEqual   *bool        `json:"write_set_equal,omitempty"` // the set of write calls (name + canonical args) is the same
	CostUSD         float64      `json:"cost_usd"`
	RecordedCostUSD *float64     `json:"recorded_cost_usd,omitempty"`
	InboxItem       string       `json:"inbox_item,omitempty"`
}

// RunReport is the result of one replay job.
type RunReport struct {
	ID        string          `json:"id"`
	Mode      string          `json:"mode"`
	Candidate string          `json:"candidate"` // provider/model
	Recorded  []string        `json:"recorded"`  // models seen in the recordings
	Sessions  []SessionResult `json:"sessions"`
	Counts    map[string]int  `json:"counts"` // state -> steps
	SpentUSD  float64         `json:"spent_usd"`
	Stopped   string          `json:"stopped,omitempty"`
	Note      string          `json:"note"`
	Stats     *Stats          `json:"stats,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// Run replays every recording in the mode and builds the report. A budget
// refusal stops the run ("budget"); a cancelled context stops it
// ("cancelled").
func Run(ctx context.Context, id, mode string, recs []*replayrec.Recording, c investigator.Caller, judge investigator.Caller, opt Options) *RunReport {
	rep := &RunReport{ID: id, Mode: mode, Candidate: opt.Candidate, Sessions: []SessionResult{}, Counts: map[string]int{}, Note: Note, CreatedAt: time.Now().UTC()}
	seen := map[string]bool{}
	for _, rec := range recs {
		if ctx.Err() != nil {
			rep.Stopped = "cancelled"
			break
		}
		if rec.Model != "" && !seen[rec.Provider+"/"+rec.Model] {
			seen[rec.Provider+"/"+rec.Model] = true
			rep.Recorded = append(rep.Recorded, strings.TrimPrefix(rec.Provider+"/"+rec.Model, "/"))
		}
		var res SessionResult
		var stop string
		if mode == ModeTrajectory {
			res, stop = Trajectory(ctx, c, judge, rec, opt)
		} else {
			res, stop = Fixed(ctx, c, judge, rec, opt)
		}
		rep.Sessions = append(rep.Sessions, res)
		for _, s := range res.Steps {
			rep.Counts[s.State]++
		}
		if stop != "" {
			rep.Stopped = stop
			break
		}
	}
	rep.SpentUSD = c.Spent()
	if judge != nil && judge != c {
		rep.SpentUSD += judge.Spent()
	}
	st := Summarize(rep)
	rep.Stats = &st
	return rep
}

// Fixed sends each recorded request unchanged (except model and stream)
// and compares the answer with the recorded one. The model's answer never
// feeds the next step. The second value is "budget" when the cap stopped
// the session.
func Fixed(ctx context.Context, c investigator.Caller, judge investigator.Caller, rec *replayrec.Recording, opt Options) (SessionResult, string) {
	res := SessionResult{Recording: rec.ID, SessionID: rec.SessionID, Steps: []StepResult{}}
	var recWrites, newWrites []replayrec.ToolCall
	stop := ""
	for _, call := range rec.Calls {
		st := StepResult{Index: call.Index, Recorded: shown(call.Answer), RecordedCostUSD: call.CostUSD, RecordedLatencyMS: call.LatencyMS}
		switch {
		case len(call.Request) == 0:
			st.State, st.New = StateInsufficient, textNoRequest
		case call.HasRedaction || bytes.Contains(call.Request, []byte(replayrec.RedactionMarker)):
			st.State, st.New = StateRedacted, textRedacted
		case len(call.Answer.ToolCalls) > 0 && !call.ToolsDefined:
			st.State, st.New = StateInsufficient, textNoToolDefs
		default:
			body, err := requestBody(call.Request, nil, opt.Candidate)
			if err != nil {
				st.State, st.New = StateInsufficient, textNoRequest
				break
			}
			ans, reply, err := send(ctx, c, body)
			st.CostUSD, st.LatencyMS = reply.CostUSD, reply.LatencyMS
			if err != nil {
				st.State, st.New = StateInfraFailure, failText(err)
				if errors.Is(err, investigator.ErrBudget) {
					stop, st.New = "budget", textBudget
				}
				break
			}
			st.New = shown(ans)
			st.State, st.Basis = compare(call.Answer, ans), "exact"
			if st.State == StateDifferentText && judge != nil {
				if err := judgeStep(ctx, judge, call.Answer.Text, ans.Text, &st); errors.Is(err, investigator.ErrBudget) {
					stop = "budget"
				}
			}
			recWrites = append(recWrites, call.Answer.ToolCalls...)
			newWrites = append(newWrites, ans.ToolCalls...)
		}
		res.Steps = append(res.Steps, st)
		if stop != "" {
			break
		}
	}
	finish(&res, rec, recWrites, newWrites)
	return res, stop
}

// Trajectory starts from recorded call 0's request and lets the new model
// carry on. Tool calls are answered only from the recording; a miss ends
// the session. Later user messages of the recording are given when the
// model answers in text.
func Trajectory(ctx context.Context, c investigator.Caller, judge investigator.Caller, rec *replayrec.Recording, opt Options) (SessionResult, string) {
	res := SessionResult{Recording: rec.ID, SessionID: rec.SessionID, Steps: []StepResult{}}
	maxSteps := opt.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	maxSteps = min(maxSteps, maxMaxSteps)
	end := func(st StepResult) (SessionResult, string) {
		res.Steps = append(res.Steps, st)
		finish(&res, rec, nil, nil)
		return res, ""
	}
	if len(rec.Calls) == 0 || len(rec.Calls[0].Request) == 0 {
		return end(StepResult{State: StateInsufficient, New: textNoRequest})
	}
	first := rec.Calls[0]
	if first.HasRedaction || bytes.Contains(first.Request, []byte(replayrec.RedactionMarker)) {
		return end(StepResult{State: StateRedacted, New: textRedacted, Recorded: shown(first.Answer)})
	}
	recordedUsesTools := false
	for _, k := range rec.Calls {
		if len(k.Answer.ToolCalls) > 0 {
			recordedUsesTools = true
		}
	}
	if recordedUsesTools && !first.ToolsDefined {
		return end(StepResult{State: StateInsufficient, New: textNoToolDefs, Recorded: shown(first.Answer)})
	}
	// Messages through the first user message; the later user messages of
	// the last recorded request come one at a time.
	var msgs []json.RawMessage
	for _, m := range rawMessages(first.Request) {
		msgs = append(msgs, m)
		if roleOf(m) == "user" {
			break
		}
	}
	var later []json.RawMessage
	users := 0
	for _, m := range rawMessages(rec.Calls[len(rec.Calls)-1].Request) {
		if roleOf(m) == "user" {
			users++
			if users > 1 {
				later = append(later, m)
			}
		}
	}
	tape := replayrec.NewTape(rec)
	var recWrites, newWrites []replayrec.ToolCall
	for _, k := range rec.Calls {
		recWrites = append(recWrites, k.Answer.ToolCalls...)
	}
	finalRecorded := rec.Calls[len(rec.Calls)-1].Answer
	stop := ""
	for step := 0; ; step++ {
		st := StepResult{Index: step}
		var recAt *replayrec.Call
		if step < len(rec.Calls) {
			recAt = &rec.Calls[step]
			st.Recorded, st.RecordedCostUSD, st.RecordedLatencyMS = shown(recAt.Answer), recAt.CostUSD, recAt.LatencyMS
		}
		if step >= maxSteps {
			st.State, st.New = StateInsufficient, textStepLimit
			res.Steps = append(res.Steps, st)
			break
		}
		body, err := requestBody(first.Request, msgs, opt.Candidate)
		if err != nil {
			st.State, st.New = StateInsufficient, textNoRequest
			res.Steps = append(res.Steps, st)
			break
		}
		if !clean(body) {
			st.State, st.New = StateRedacted, textRedacted
			res.Steps = append(res.Steps, st)
			break
		}
		ans, reply, err := send(ctx, c, body)
		st.CostUSD, st.LatencyMS = reply.CostUSD, reply.LatencyMS
		if err != nil {
			st.State, st.New = StateInfraFailure, failText(err)
			if errors.Is(err, investigator.ErrBudget) {
				stop, st.New = "budget", textBudget
			}
			res.Steps = append(res.Steps, st)
			break
		}
		st.New = shown(ans)
		newWrites = append(newWrites, ans.ToolCalls...)
		if len(ans.ToolCalls) > 0 {
			assistant, results, missed := answerTools(tape, ans)
			if missed != "" {
				st.State = StateMissingTool
				st.New = fmt.Sprintf("Stopped at step %d: %s called %s with arguments the recording has no answer for.", step+1, opt.Candidate, missed)
				res.Steps = append(res.Steps, st)
				break
			}
			st.State, st.Basis = StateInsufficient, "exact"
			if recAt != nil {
				st.State = compare(recAt.Answer, ans)
			}
			res.Steps = append(res.Steps, st)
			msgs = append(msgs, assistant)
			msgs = append(msgs, results...)
			continue
		}
		if len(later) > 0 {
			st.State, st.Basis = StateInsufficient, "exact"
			if recAt != nil {
				st.State = compare(recAt.Answer, ans)
			}
			res.Steps = append(res.Steps, st)
			am, _ := json.Marshal(map[string]any{"role": "assistant", "content": ans.Text})
			msgs = append(msgs, am, later[0])
			later = later[1:]
			continue
		}
		st.Recorded = shown(finalRecorded)
		st.State, st.Basis = compare(finalRecorded, ans), "exact"
		if st.State == StateDifferentText && judge != nil {
			if err := judgeStep(ctx, judge, finalRecorded.Text, ans.Text, &st); errors.Is(err, investigator.ErrBudget) {
				stop = "budget"
			}
		}
		res.Steps = append(res.Steps, st)
		break
	}
	finish(&res, rec, recWrites, newWrites)
	return res, stop
}

// answerTools answers every tool call of an answer from the tape. It
// returns the assistant message and the tool messages, or the name of the
// first tool call the recording cannot answer.
func answerTools(tape *replayrec.Tape, ans replayrec.Answer) (json.RawMessage, []json.RawMessage, string) {
	calls := make([]any, 0, len(ans.ToolCalls))
	var results []json.RawMessage
	for i, tc := range ans.ToolCalls {
		args, err := replayrec.Canonical(tc.Arguments)
		if err != nil {
			return nil, nil, tc.Name
		}
		r, ok := tape.Answer(tc.Name, args)
		if !ok {
			return nil, nil, tc.Name
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": tc.Arguments}})
		m, _ := json.Marshal(map[string]any{"role": "tool", "tool_call_id": id, "content": r.Content})
		results = append(results, m)
	}
	content := any(nil)
	if ans.Text != "" {
		content = ans.Text
	}
	am, _ := json.Marshal(map[string]any{"role": "assistant", "content": content, "tool_calls": calls})
	return am, results, ""
}

// requestBody is the recorded request with the candidate model, stream
// false, and, when msgs is not nil, those messages.
func requestBody(recorded json.RawMessage, msgs []json.RawMessage, candidate string) ([]byte, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(recorded, &body); err != nil || body == nil {
		return nil, errors.New("the recorded request is not a JSON object")
	}
	if msgs != nil {
		b, err := json.Marshal(msgs)
		if err != nil {
			return nil, err
		}
		body["messages"] = b
	}
	m, _ := json.Marshal(candidate)
	body["model"] = m
	body["stream"] = json.RawMessage("false")
	delete(body, "stream_options")
	return json.Marshal(body)
}

// clean says whether a body may be sent: it holds no redaction marker, and
// redacting it again changes nothing.
func clean(body []byte) bool {
	if bytes.Contains(body, []byte(replayrec.RedactionMarker)) {
		return false
	}
	again := traceimport.NewRedactor().JSON(body)
	return !bytes.Contains(again, []byte(replayrec.RedactionMarker))
}

// send makes one model call with a recorded body. A body that is not clean
// is never sent.
func send(ctx context.Context, c investigator.Caller, body []byte) (replayrec.Answer, investigator.Reply, error) {
	if !clean(body) {
		return replayrec.Answer{}, investigator.Reply{}, errors.New("the request holds redacted text and was not sent")
	}
	reply, err := c.Call(ctx, investigator.Request{Purpose: "replay_step", Raw: body})
	if err != nil {
		return replayrec.Answer{}, reply, err
	}
	raw := reply.Raw
	if len(raw) == 0 {
		raw = []byte(reply.Text)
	}
	ans, err := replayrec.ParseAnswer(raw)
	return ans, reply, err
}

// judgeStep asks the judge whether two text answers are the same. Texts
// with redacted parts are not sent.
func judgeStep(ctx context.Context, judge investigator.Caller, recorded, got string, st *StepResult) error {
	if strings.Contains(recorded, replayrec.RedactionMarker) || strings.Contains(got, replayrec.RedactionMarker) {
		return nil
	}
	rd := traceimport.NewRedactor()
	a, b := rd.Text(recorded), rd.Text(got)
	if a != recorded || b != got {
		return nil
	}
	reply, err := judge.Call(ctx, investigator.Request{Purpose: "replay_judge", Schema: judgeSchema, MaxTokens: 300, Messages: []investigator.Message{
		{Role: "system", Content: JudgeSystem},
		{Role: "user", Content: "First answer:\n" + a + "\n\nSecond answer:\n" + b},
	}})
	st.CostUSD += reply.CostUSD
	if err != nil {
		return err
	}
	var v struct {
		Equivalent bool   `json:"equivalent"`
		Reason     string `json:"reason"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(reply.Text)), &v) != nil {
		return nil
	}
	st.Basis, st.JudgeNote = "judge", cut(v.Reason)
	if v.Equivalent {
		st.State = StateEquivalent
	}
	return nil
}

// compare says how a new answer relates to the recorded one.
func compare(rec, got replayrec.Answer) string {
	if len(rec.ToolCalls) > 0 || len(got.ToolCalls) > 0 {
		if sameCalls(rec.ToolCalls, got.ToolCalls) {
			return StateEquivalent
		}
		return StateDifferentTool
	}
	if replayrec.NormalizeText(rec.Text) == replayrec.NormalizeText(got.Text) {
		return StateEquivalent
	}
	return StateDifferentText
}

// sameCalls compares names as a multiset, then canonical arguments per name
// in order.
func sameCalls(a, b []replayrec.ToolCall) bool {
	if len(a) != len(b) {
		return false
	}
	group := func(cs []replayrec.ToolCall) map[string][]string {
		m := map[string][]string{}
		for _, c := range cs {
			args, err := replayrec.Canonical(c.Arguments)
			if err != nil {
				args = c.Arguments
			}
			m[c.Name] = append(m[c.Name], args)
		}
		return m
	}
	ga, gb := group(a), group(b)
	if len(ga) != len(gb) {
		return false
	}
	for name, xs := range ga {
		ys := gb[name]
		if len(xs) != len(ys) {
			return false
		}
		for i := range xs {
			if xs[i] != ys[i] {
				return false
			}
		}
	}
	return true
}

// writeSetEqual says whether the write calls (name + canonical arguments)
// of both sides are the same set.
func writeSetEqual(a, b []replayrec.ToolCall) bool {
	set := func(cs []replayrec.ToolCall) map[string]bool {
		m := map[string]bool{}
		for _, c := range cs {
			if !(c.Write || replayrec.WriteTool(c.Name)) {
				continue
			}
			args, err := replayrec.Canonical(c.Arguments)
			if err != nil {
				args = c.Arguments
			}
			m[c.Name+"\x00"+args] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

// finish fills the session's summary fields from its steps.
func finish(res *SessionResult, rec *replayrec.Recording, recCalls, newCalls []replayrec.ToolCall) {
	compared, toolSteps, toolSame := 0, 0, 0
	var recCost float64
	haveRecCost := false
	for i, s := range res.Steps {
		res.CostUSD += s.CostUSD
		if s.RecordedCostUSD != nil {
			recCost += *s.RecordedCostUSD
			haveRecCost = true
		}
		switch s.State {
		case StateDifferentText, StateDifferentTool, StateMissingTool:
			if res.DivergedAt == nil {
				d := res.Steps[i].Index
				res.DivergedAt = &d
			}
		}
		if s.State != StateRedacted && s.State != StateInsufficient {
			compared++
		}
		if idx := s.Index; idx < len(rec.Calls) && len(rec.Calls[idx].Answer.ToolCalls) > 0 && s.State != StateRedacted && s.State != StateInsufficient && s.State != StateInfraFailure {
			toolSteps++
			if s.State == StateEquivalent {
				toolSame++
			}
		}
	}
	if n := len(res.Steps); n > 0 {
		res.Final = res.Steps[n-1].State
	}
	if haveRecCost {
		res.RecordedCostUSD = &recCost
	}
	if toolSteps > 0 {
		v := float64(toolSame) / float64(toolSteps)
		res.ToolAgreement = &v
	}
	if compared > 0 {
		eq := writeSetEqual(recCalls, newCalls)
		res.WriteSetEqual = &eq
	}
}

func shown(a replayrec.Answer) string {
	if len(a.ToolCalls) == 0 {
		return cut(a.Text)
	}
	parts := make([]string, 0, len(a.ToolCalls))
	for _, c := range a.ToolCalls {
		parts = append(parts, c.Name+"("+c.Arguments+")")
	}
	return cut("calls: " + strings.Join(parts, ", "))
}

func cut(s string) string {
	r := []rune(s)
	if len(r) > maxShown {
		return string(r[:maxShown])
	}
	return s
}

func failText(err error) string {
	return cut("The model call failed: " + err.Error())
}

func rawMessages(req json.RawMessage) []json.RawMessage {
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	_ = json.Unmarshal(req, &body)
	return body.Messages
}

func roleOf(m json.RawMessage) string {
	var v struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(m, &v)
	return v.Role
}
