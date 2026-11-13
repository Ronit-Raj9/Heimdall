package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ParserOpenCode is the name of the OpenCode JSON event parser.
const ParserOpenCode = "opencode"

const openCodeVersion = "1.0.0"

// openCodeTargetKeys are the tool input fields that name what a call acts on,
// in priority order. OpenCode's built-in tools use camelCase: read, edit and
// write take filePath; bash takes command; glob and grep take pattern (and an
// optional path); webfetch takes url.
var openCodeTargetKeys = []string{"filePath", "path", "command", "pattern", "url"}

type openCodeParser struct{}

func newOpenCodeParser() *openCodeParser {
	return &openCodeParser{}
}

// GetName implements schemas.Part.
func (p *openCodeParser) GetName() string { return ParserOpenCode }

// GetVersion implements schemas.Part.
func (p *openCodeParser) GetVersion() string { return openCodeVersion }

// Parse reads `opencode run --format json` output.
//
// The format is one JSON object per line with a type, a millisecond
// timestamp and a message part (OpenCode 1.19): step_start and step_finish
// bracket one model call, so a model turn is a step, not a message;
// step_finish carries that call's tokens, already split into disjoint buckets
// (input excludes cache reads and writes, output excludes reasoning). A
// tool_use line is printed once the tool has finished and holds both the call
// and its result. text lines are the assistant's words; error lines are a
// failed API call or session error. Lines that are not JSON or not understood
// are skipped: the parser adds detail and must never fail a run.
func (p *openCodeParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	st := &openCodeStream{streamState: newStreamState(rc), step: -1}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev openCodeEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		st.handle(&ev)
	}
	return &schemas.ParseResult{Events: st.events, Claim: st.claim()}, nil
}

// openCodeEvent is one line of OpenCode's JSON output.
type openCodeEvent struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Part      openCodePart    `json:"part"`
	Error     json.RawMessage `json:"error"`
}

type openCodePart struct {
	MessageID string            `json:"messageID"`
	Text      string            `json:"text"`
	Tool      string            `json:"tool"`
	CallID    string            `json:"callID"`
	State     openCodeToolState `json:"state"`
	Tokens    *openCodeTokens   `json:"tokens"`
}

// openCodeToolState is a tool part's state.
type openCodeToolState struct {
	Status string          `json:"status"` // completed | error
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error"`
}

type openCodeTokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// openCodeStream accumulates events across lines on top of the shared stream
// state. step is the index of the current step's model_response event.
type openCodeStream struct {
	*streamState
	step int
}

func (st *openCodeStream) handle(ev *openCodeEvent) {
	if ev.Timestamp > 0 {
		st.ts = time.UnixMilli(ev.Timestamp).UTC()
	}
	switch ev.Type {
	case "step_start", "step_finish", "tool_use", "text", "reasoning", "error":
		st.recognized = true
	}
	switch ev.Type {
	case "step_start":
		st.turn++
		st.step = st.newEvent(schemas.EventModelResponse, st.turn)
	case "step_finish":
		if st.step >= 0 && ev.Part.Tokens != nil {
			t := ev.Part.Tokens
			st.events[st.step].Tokens = &schemas.Tokens{
				Fresh: t.Input, CacheRead: t.Cache.Read, CacheWrite: t.Cache.Write, Out: t.Output + t.Reasoning,
			}
		}
	case "tool_use":
		st.tool(&ev.Part)
	case "text":
		st.text(&ev.Part)
	case "error":
		i := st.newEvent(schemas.EventError, st.turn)
		st.events[i].IsError = true
		st.events[i].Summary = clip(oneLine("api error "+openCodeErrorText(ev.Error)), summaryMax)
	}
}

// tool records the call and its result from one finished tool part.
func (st *openCodeStream) tool(p *openCodePart) {
	turn, parentID := st.turn, ""
	if st.step >= 0 {
		parentID = st.events[st.step].EventID
	}
	ci := st.newEvent(schemas.EventToolCall, turn)
	call := &st.events[ci]
	call.ToolCallID = p.CallID
	call.ToolName = p.Tool
	call.ParentEventID = parentID
	call.Target = inputTarget(p.State.Input, openCodeTargetKeys)
	call.ArgsHash = hashRaw(p.State.Input)
	call.Summary = clip(oneLine(strings.TrimSpace(p.Tool+" "+call.Target)), summaryMax)
	callID, target := call.EventID, call.Target

	ri := st.newEvent(schemas.EventToolResult, turn)
	res := &st.events[ri]
	res.ToolCallID = p.CallID
	res.ToolName = p.Tool
	res.ParentEventID = callID
	res.Target = target
	res.IsError = p.State.Status == "error"
	out := p.State.Output
	if res.IsError && p.State.Error != "" {
		out, _ = json.Marshal(p.State.Error)
	}
	res.ResultHash = hashRaw(out)
	res.Summary = clip(oneLine(resultText(out)), summaryMax)
}

// text keeps the latest assistant message's text for the claim. A new message
// id starts a new final message.
func (st *openCodeStream) text(p *openCodePart) {
	if p.MessageID != st.lastMsgID {
		st.lastMsgID = p.MessageID
		st.lastText.Reset()
	}
	st.lastText.WriteString(p.Text)
	if st.step >= 0 && st.events[st.step].Summary == "" {
		st.events[st.step].Summary = clip(oneLine(p.Text), summaryMax)
	}
}

// openCodeErrorText reads an error object {name, data: {message,
// statusCode}}, or a plain string.
func openCodeErrorText(raw json.RawMessage) string {
	if s, ok := rawString(raw); ok {
		return s
	}
	var e struct {
		Name string `json:"name"`
		Data struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	s := strings.TrimSpace(e.Name + ": " + e.Data.Message)
	if e.Data.StatusCode > 0 {
		s += fmt.Sprintf(" (%d)", e.Data.StatusCode)
	}
	return s
}

// inputTarget returns the first non-empty string field of a tool input among
// keys.
func inputTarget(input json.RawMessage, keys []string) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return clip(strings.TrimSpace(s), targetMax)
		}
	}
	return ""
}
