package adapters

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ParserMiniSWE is the name of the mini-swe-agent trajectory parser.
const ParserMiniSWE = "mini-swe-agent"

const miniSWEVersion = "1.0.0"

// The launch wrapper in agents/mini-swe-agent.yaml prints the trajectory file
// to stdout after the tool exits, between these two marker lines. The begin
// line carries the file's size, so a block cut by the output cap (or by
// anything else) is detected and never parsed as complete.
const (
	MiniSWETrajectoryBegin = "@@heimdall-mswea-trajectory begin bytes="
	MiniSWETrajectoryEnd   = "@@heimdall-mswea-trajectory end"
	// miniSWEMaxBlock bounds the bytes kept from one block. The stdout cap
	// (50 MiB) already bounds the whole stream.
	miniSWEMaxBlock = 32 << 20
)

// The trajectory format the parser reads: mini-swe-agent 2.4.6,
// trajectory_format "mini-swe-agent-1.1". Field origins in the tool's source:
//   - top level info, messages, trajectory_format: agents/default.py
//     DefaultAgent.serialize. info.model_stats.instance_cost and api_calls,
//     info.exit_status and info.submission (from the last message's extra),
//     info.mini_version. info.config.model.model_name is merged in by
//     models/litellm_model.py LitellmModel.serialize.
//   - system and user messages: {role, content}, DefaultAgent.run through
//     LitellmModel.format_message.
//   - assistant messages: litellm's choices[0].message.model_dump() (content,
//     role, tool_calls) plus extra{actions, response, cost, timestamp},
//     models/litellm_model.py LitellmModel.query. actions are
//     {command, tool_call_id}, models/utils/actions_toolcall.py
//     parse_toolcall_actions. extra.response is the provider response dump;
//     the parser reads only its model and usage (OpenAI chat completion
//     fields prompt_tokens, completion_tokens, prompt_tokens_details.cached_tokens).
//   - tool messages: {role "tool", tool_call_id, content, extra{raw_output,
//     returncode, timestamp, exception_info}},
//     actions_toolcall.py format_toolcall_observation_messages.
//   - format errors: {role "user", content, extra{interrupt_type
//     "FormatError"}}, parse_toolcall_actions.
//   - exit: {role "exit", content, extra{exit_status, submission}},
//     environments/local.py LocalEnvironment._check_finished ("Submitted"),
//     DefaultAgent.query (LimitsExceeded, TimeExceeded), DefaultAgent.run
//     (RepeatedFormatError), DefaultAgent.handle_uncaught_exception.
type miniTrajectory struct {
	Info             miniInfo          `json:"info"`
	Messages         []json.RawMessage `json:"messages"`
	TrajectoryFormat string            `json:"trajectory_format"`
}

type miniInfo struct {
	ModelStats struct {
		InstanceCost float64 `json:"instance_cost"`
		APICalls     int     `json:"api_calls"`
	} `json:"model_stats"`
	Config struct {
		Model struct {
			ModelName string `json:"model_name"`
		} `json:"model"`
	} `json:"config"`
	MiniVersion string `json:"mini_version"`
	ExitStatus  string `json:"exit_status"`
	Submission  string `json:"submission"`
}

type miniMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Extra      miniExtra       `json:"extra"`
}

type miniExtra struct {
	Actions []struct {
		Command    string `json:"command"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"actions"`
	Response *struct {
		Model string `json:"model"`
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
	Cost          *float64 `json:"cost"`
	Timestamp     *float64 `json:"timestamp"`
	RawOutput     string   `json:"raw_output"`
	ReturnCode    *int     `json:"returncode"`
	ExceptionInfo string   `json:"exception_info"`
	InterruptType string   `json:"interrupt_type"`
	ExitStatus    string   `json:"exit_status"`
	Submission    string   `json:"submission"`
}

type miniSWEParser struct{}

func newMiniSWEParser() *miniSWEParser { return &miniSWEParser{} }

// GetName implements schemas.Part.
func (p *miniSWEParser) GetName() string { return ParserMiniSWE }

// GetVersion implements schemas.Part.
func (p *miniSWEParser) GetVersion() string { return miniSWEVersion }

// ParseExecutionOutput implements schemas.AdapterResultParser.
func (p *miniSWEParser) ParseExecutionOutput(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.Parse(ctx, rc, raw)
}

// Parse reads the agent's stdout: the trajectory block, nothing else (the
// tool's console goes to stderr). Any other line is counted as damage.
func (p *miniSWEParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.ParseReader(ctx, rc, bytes.NewReader(raw))
}

// ParseReader finds the last trajectory block, checks it is whole against its
// declared size and turns its messages into events. A cut block still yields
// the messages decoded before the cut, marked truncated (a partial trace).
func (p *miniSWEParser) ParseReader(ctx context.Context, rc *schemas.RunContext, raw io.Reader) (*schemas.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := schemas.ParseDiagnostics{Source: schemas.TraceSourceNative, Parser: p.GetName(), ParserVersion: p.GetVersion()}
	block, declared, ended, total, readErr := readMiniBlock(ctx, raw, &d)
	d.Bytes = total
	st := newMiniState(rc)
	switch {
	case block == nil:
		d.Reason = "no trajectory block in agent stdout"
	default:
		whole := ended && declared >= 0 && int64(len(block)) == declared
		if !whole {
			d.Truncated = true
			d.Reason = "trajectory block truncated"
		}
		st.decode(block, &d)
		if d.Malformed > 0 && d.Reason == "" {
			d.Reason = "trajectory block is not valid JSON"
		}
	}
	if d.Reason == "" && d.Malformed+d.Oversized+d.Unrecognized > 0 {
		d.Reason = "some stdout lines or trajectory messages were not understood"
	}
	if readErr != nil {
		d.Reason = "agent stream could not be fully read"
	}
	d.Emitted = len(st.events)
	d.Quality = traceQuality(d)
	return &schemas.ParseResult{Events: st.events, Claim: st.claim(), Diagnostics: &d}, readErr
}

// readMiniBlock returns the bytes of the last block (nil when none began),
// the size its begin line declared (-1 when unreadable), whether its end
// line was seen, and the stdout bytes read. Lines outside a block are
// drained without being kept.
func readMiniBlock(ctx context.Context, raw io.Reader, d *schemas.ParseDiagnostics) (block []byte, declared int64, ended bool, total int64, err error) {
	r := bufio.NewReaderSize(raw, 64<<10)
	var cur []byte
	in := false
	var line []byte
	var lineLen int
	for {
		if err := ctx.Err(); err != nil {
			return block, declared, ended, total, err
		}
		frag, rerr := r.ReadSlice('\n')
		total += int64(len(frag))
		if in {
			cur = appendCapped(cur, frag)
		}
		// Marker lines are short; only the first 256 bytes of a line are
		// kept for the marker check.
		lineLen += len(frag)
		if !in && lineLen <= maxParserLineBytes+1 {
			line = append(line, frag...) // a stray line is checked whole
		} else if len(line) < 256 {
			line = append(line, frag[:min(len(frag), 256-len(line))]...)
		}
		if errors.Is(rerr, bufio.ErrBufferFull) {
			continue
		}
		text := strings.TrimRight(string(line), "\r\n")
		wasIn, n := in, lineLen
		line, lineLen = line[:0], 0
		if !wasIn && !strings.HasPrefix(text, MiniSWETrajectoryBegin) {
			strayLine(d, text, n)
		}
		switch {
		case strings.HasPrefix(text, MiniSWETrajectoryBegin):
			in, ended, cur = true, false, []byte{}
			block = nil
			declared = -1
			if n, perr := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(text, MiniSWETrajectoryBegin)), 10, 64); perr == nil {
				declared = n
			}
		case in && text == MiniSWETrajectoryEnd:
			// The end line, and the newline the wrapper printed before it,
			// are not part of the file.
			body := cur
			if len(body) >= len(frag) {
				body = body[:len(body)-len(frag)]
			}
			body = bytes.TrimSuffix(body, []byte("\n"))
			block, ended, in = body, true, false
		}
		if rerr != nil {
			if in {
				block = cur
			}
			if errors.Is(rerr, io.EOF) {
				return block, declared, ended, total, nil
			}
			return block, declared, ended, total, rerr
		}
	}
}

// strayLine counts a stdout line outside the block. The wrapper sends the
// tool's console to stderr, so stdout should hold only the block: anything
// else is damage, counted the way scanReader counts it.
func strayLine(d *schemas.ParseDiagnostics, text string, n int) {
	text = strings.TrimSpace(text)
	switch {
	case n > maxParserLineBytes+1:
		d.Lines++
		d.Oversized++
	case text == "":
	case strings.HasPrefix(text, "{") && !json.Valid([]byte(text)):
		d.Lines++
		d.Malformed++
	default:
		d.Lines++
		d.Unrecognized++
	}
}

func appendCapped(dst, p []byte) []byte {
	room := miniSWEMaxBlock + 1 - len(dst)
	if room <= 0 {
		return dst
	}
	return append(dst, p[:min(len(p), room)]...)
}

type miniState struct {
	*streamState
	info     miniInfo
	exit     string
	turnEv   int
	calls    map[string]int
	gotInfo  bool
	lastText string
}

func newMiniState(rc *schemas.RunContext) *miniState {
	st := newStreamState(rc)
	st.parserVersion = miniSWEVersion
	return &miniState{streamState: st, turnEv: -1, calls: map[string]int{}}
}

// decode walks the trajectory object token by token, so a cut file still
// gives every message decoded before the cut.
func (st *miniState) decode(block []byte, d *schemas.ParseDiagnostics) {
	dec := json.NewDecoder(bytes.NewReader(block))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		d.Malformed++
		return
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			d.Malformed++
			return
		}
		key, _ := keyTok.(string)
		switch key {
		case "info":
			if err := dec.Decode(&st.info); err != nil {
				d.Malformed++
				return
			}
			st.gotInfo = true
		case "messages":
			if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
				d.Malformed++
				return
			}
			for dec.More() {
				var m miniMessage
				if err := dec.Decode(&m); err != nil {
					if !d.Truncated {
						d.Malformed++
					}
					return
				}
				d.Lines++
				if st.message(m) {
					d.Recognized++
				} else {
					d.Unrecognized++
				}
			}
			if _, err := dec.Token(); err != nil {
				return
			}
		case "trajectory_format":
			var f string
			if err := dec.Decode(&f); err != nil {
				return
			}
			if !strings.HasPrefix(f, "mini-swe-agent-1.") {
				d.Reason = "unknown trajectory format " + clip(f, 40)
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return
			}
		}
	}
}

// message turns one trajectory message into events. It reports whether the
// role was understood.
func (st *miniState) message(m miniMessage) bool {
	if m.Extra.Timestamp != nil && *m.Extra.Timestamp > 0 {
		sec, frac := math.Modf(*m.Extra.Timestamp)
		st.ts = time.Unix(int64(sec), int64(frac*1e9)).UTC()
	}
	switch m.Role {
	case "system":
		return true
	case "user":
		if m.Extra.InterruptType == "FormatError" {
			i := st.newEvent(schemas.EventError, st.turn)
			st.events[i].IsError = true
			st.events[i].Summary = clip(oneLine("format error: "+miniText(m.Content)), summaryMax)
		}
		return true
	case "assistant":
		st.assistant(m)
		return true
	case "tool":
		st.toolResult(m)
		return true
	case "exit":
		st.exit = m.Extra.ExitStatus
		if st.exit != "" && st.exit != "Submitted" {
			i := st.newEvent(schemas.EventError, st.turn)
			st.events[i].IsError = true
			st.events[i].Summary = clip(oneLine("exit: "+st.exit+" "+miniText(m.Content)), summaryMax)
		}
		return true
	}
	return false
}

func (st *miniState) assistant(m miniMessage) {
	st.turn++
	i := st.newEvent(schemas.EventModelResponse, st.turn)
	st.turnEv = i
	ev := &st.events[i]
	text := miniText(m.Content)
	ev.Summary = clip(oneLine(text), summaryMax)
	if strings.TrimSpace(text) != "" {
		st.lastText = text
	}
	if r := m.Extra.Response; r != nil {
		ev.ModelServed = r.Model
		if u := r.Usage; u != nil {
			cached := int64(0)
			if u.PromptTokensDetails != nil {
				cached = u.PromptTokensDetails.CachedTokens
			}
			ev.Tokens = &schemas.Tokens{Fresh: max(u.PromptTokens-cached, 0), CacheRead: cached, Out: u.CompletionTokens}
		}
	}
	// litellm prices a call only when it knows the model; 0 means not priced,
	// and the gateway rows supply cost.
	if m.Extra.Cost != nil && *m.Extra.Cost > 0 {
		v := schemas.Observed(*m.Extra.Cost, schemas.EvidenceNative)
		v.Source = schemas.EvidenceSourceAgentStream
		ev.USD = &v
	}
	parentID, turn := ev.EventID, ev.TurnIndex
	for _, a := range m.Extra.Actions {
		j := st.newEvent(schemas.EventToolCall, turn)
		call := &st.events[j]
		call.ToolCallID = a.ToolCallID
		call.ToolName = "bash"
		call.ParentEventID = parentID
		call.Target = clip(strings.TrimSpace(a.Command), targetMax)
		call.ArgsHash = hashValue(map[string]any{"command": a.Command})
		call.Summary = clip(oneLine("bash "+a.Command), summaryMax)
		if a.ToolCallID != "" {
			st.calls[a.ToolCallID] = j
		}
	}
}

func (st *miniState) toolResult(m miniMessage) {
	i := st.newEvent(schemas.EventToolResult, st.turn)
	ev := &st.events[i]
	ev.ToolCallID = m.ToolCallID
	ev.ToolName = "bash"
	if ci, ok := st.calls[m.ToolCallID]; ok {
		call := st.events[ci]
		ev.ParentEventID, ev.Target, ev.TurnIndex = call.EventID, call.Target, call.TurnIndex
	}
	ev.ExitStatus = m.Extra.ReturnCode
	ev.IsError = m.Extra.ExceptionInfo != "" || (m.Extra.ReturnCode != nil && *m.Extra.ReturnCode != 0)
	ev.ResultHash = hashValue(map[string]any{"returncode": m.Extra.ReturnCode, "output": m.Extra.RawOutput})
	code := "?"
	if m.Extra.ReturnCode != nil {
		code = strconv.Itoa(*m.Extra.ReturnCode)
	}
	out := lastLine(m.Extra.RawOutput)
	if m.Extra.ExceptionInfo != "" {
		out = m.Extra.ExceptionInfo
	}
	ev.Summary = clip(oneLine("exit "+code+": "+out), summaryMax)
}

// claim: the tool submits by running its submit command, which ends the run
// with exit_status Submitted. Any other exit status is no claim. Without an
// exit message (a cut trajectory) the claim is left to the stdout tail.
func (st *miniState) claim() *schemas.Claim {
	status := st.exit
	if status == "" && st.gotInfo {
		status = st.info.ExitStatus
	}
	if status == "" {
		return nil
	}
	c := &schemas.Claim{Made: status == "Submitted", Source: schemas.EvidenceNative, Text: clipTail(st.lastText, 500)}
	if c.Made {
		c.Matched = "exit_status: Submitted"
	}
	return c
}

// miniText reads a message's content: a string, or the list of parts that
// multimodal expansion produces (text parts only).
func miniText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}
