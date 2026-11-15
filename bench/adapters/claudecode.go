package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ParserClaudeCode is the name of the Claude Code stream-json parser.
const ParserClaudeCode = "claudecode"

const (
	claudeCodeVersion = "1.1.0"
	summaryMax        = 120
	targetMax         = 500
	claimTextMax      = 300
	syntheticModel    = "<synthetic>" // Claude Code's model id on locally built API-error messages
)

// targetKeys are the tool input fields that name what a call acts on, in
// priority order. They cover Claude Code's built-in tools (Read, Edit, Write:
// file_path; NotebookEdit: notebook_path; Glob, Grep: path and pattern; Bash:
// command; WebFetch: url).
var targetKeys = []string{"file_path", "notebook_path", "path", "command", "pattern", "url"}

type claudeCodeParser struct{}

func newClaudeCodeParser() *claudeCodeParser {
	return &claudeCodeParser{}
}

// GetName implements schemas.Part.
func (p *claudeCodeParser) GetName() string { return ParserClaudeCode }

// GetVersion implements schemas.Part.
func (p *claudeCodeParser) GetVersion() string { return claudeCodeVersion }

// ParseExecutionOutput implements schemas.AdapterResultParser.
func (p *claudeCodeParser) ParseExecutionOutput(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.Parse(ctx, rc, raw)
}

// CollectEvents implements schemas.AdapterEventSource.
func (p *claudeCodeParser) CollectEvents(ctx context.Context, rc *schemas.RunContext, raw []byte) ([]schemas.Event, error) {
	res, err := p.Parse(ctx, rc, raw)
	if res == nil {
		return nil, err
	}
	return res.Events, err
}

// Parse reads `claude -p --output-format stream-json --verbose` output.
//
// The format is the Agent SDK's SDKMessage union, one JSON object per line
// (the same lines Bifrost's tests/e2e/clis claudeStreamJSON driver reads):
// assistant lines carry an Anthropic Messages API message, user lines carry
// tool_result blocks, system lines carry init, api_retry and
// compact_boundary, and one result line ends the turn. While a response
// streams, Claude Code emits one assistant line per content block, all
// sharing message.id, so a model turn is a distinct message id, not a line.
// Lines that are not JSON or not understood are skipped: the parser adds
// detail and must never fail a run.
func (p *claudeCodeParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	return p.ParseReader(ctx, rc, bytes.NewReader(raw))
}

func (p *claudeCodeParser) ParseReader(ctx context.Context, rc *schemas.RunContext, raw io.Reader) (*schemas.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st := newStreamState(rc)
	st.parserVersion = claudeCodeVersion
	d, scanErr := scanReader(ctx, raw, func(line []byte) parsedLine {
		if len(line) == 0 || line[0] != '{' {
			return parsedLine{}
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(line, &obj); err != nil {
			return parsedLine{malformed: true}
		}
		typ, _ := rawString(obj["type"])
		recognized := typ == "assistant" || typ == "user" || typ == "system" || typ == "result"
		if typ == "assistant" || typ == "user" {
			var message map[string]json.RawMessage
			if json.Unmarshal(obj["message"], &message) != nil || message == nil {
				return parsedLine{recognized: true, malformed: true}
			}
		}
		before := len(st.events)
		st.handle(obj)
		return parsedLine{recognized: recognized, emitted: len(st.events) - before}
	})
	d.Source, d.Parser, d.ParserVersion = schemas.TraceSourceNative, p.GetName(), p.GetVersion()
	d.Quality = traceQuality(d)
	return &schemas.ParseResult{Events: st.events, Claim: st.claim(), Diagnostics: &d}, scanErr
}

// ClaimFromText decides whether free text asserts success, for agents whose
// claim can be read only from the tail of stdout (evidence level outcome).
// It returns nil for empty text.
func ClaimFromText(s string) *schemas.Claim {
	return claimFrom(s, schemas.EvidenceOutcome)
}

// streamState accumulates events across lines.
type streamState struct {
	runID, slotID, pairID string
	provider, modelReq    string
	parserVersion         string

	events    []schemas.Event
	seq       int
	turn      int
	ts        time.Time
	turnIndex map[string]int // message id -> index of its model_response event
	calls     map[string]int // tool_use id -> index of its tool_call event

	lastMsgID  string
	lastText   strings.Builder // text of the latest top-level assistant message
	resultText string
	recognized bool // at least one line of a known type was read
}

func newStreamState(rc *schemas.RunContext) *streamState {
	st := &streamState{turnIndex: map[string]int{}, calls: map[string]int{}}
	if rc == nil {
		return st
	}
	if rc.Run != nil {
		st.runID, st.slotID, st.pairID = rc.Run.RunID, rc.Run.SlotID, rc.Run.PairID
	}
	if rc.Slot != nil {
		st.slotID, st.pairID = rc.Slot.SlotID, rc.Slot.PairID
	}
	if rc.Agent != nil {
		st.provider, st.modelReq = rc.Agent.Gateway.Provider, rc.Agent.Model
	}
	if rc.Exec != nil {
		st.ts = rc.Exec.Started
	}
	return st
}

func (st *streamState) handle(obj map[string]json.RawMessage) {
	if ts, ok := rawString(obj["timestamp"]); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			st.ts = t
		}
	}
	typ, _ := rawString(obj["type"])
	switch typ {
	case "assistant", "user", "system", "result":
		st.recognized = true
	}
	switch typ {
	case "assistant":
		st.assistant(obj)
	case "user":
		st.user(obj)
	case "system":
		st.system(obj)
	case "result":
		st.result(obj)
	}
}

// newEvent appends an event and returns its index.
func (st *streamState) newEvent(typ schemas.EventType, turn int) int {
	st.seq++
	st.events = append(st.events, schemas.Event{
		EventVersion:  schemas.EventSchemaVersion,
		EventID:       fmt.Sprintf("%s:%s:%d", st.runID, schemas.SourceAgentStream, st.seq),
		RunID:         st.runID,
		SlotID:        st.slotID,
		PairID:        st.pairID,
		Seq:           st.seq,
		Sequence:      st.seq,
		TurnIndex:     turn,
		TS:            st.ts,
		Timestamp:     st.ts,
		Type:          typ,
		Source:        schemas.SourceAgentStream,
		Evidence:      schemas.EvidenceNative,
		EvidenceLevel: schemas.EvidenceNative,
		Status:        schemas.EventStatusUnknown,
		ParserVersion: st.parserVersion,
		Provider:      st.provider,
		ModelReq:      st.modelReq,
	})
	return len(st.events) - 1
}

// apiMessage is the Anthropic Messages API message inside a line.
type apiMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason *string         `json:"stop_reason"`
	Usage      *apiUsage       `json:"usage"`
}

type apiUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// assistant handles one assistant line. A line with an `error` field, or
// from the synthetic model, is Claude Code reporting a failed API call: an
// error event, not a model turn.
func (st *streamState) assistant(obj map[string]json.RawMessage) {
	var msg apiMessage
	if err := json.Unmarshal(obj["message"], &msg); err != nil {
		return
	}
	blocks := decodeBlocks(msg.Content)
	if kind := rawErrorText(obj["error"]); kind != "" || msg.Model == syntheticModel {
		i := st.newEvent(schemas.EventError, st.turn)
		st.events[i].IsError = true
		st.events[i].Summary = clip(strings.TrimSpace("api error "+kind+": "+blocksText(blocks)), summaryMax)
		return
	}
	subagent := isSubagent(obj)
	id := msg.ID
	if id == "" {
		id = fmt.Sprintf("line-%d", st.seq+1)
	}
	idx, seen := st.turnIndex[id]
	if !seen {
		st.turn++
		idx = st.newEvent(schemas.EventModelResponse, st.turn)
		st.turnIndex[id] = idx
		if subagent {
			st.events[idx].Summary = "subagent"
		}
	}
	ev := &st.events[idx]
	if msg.Model != "" {
		ev.ModelServed = msg.Model
	}
	if msg.Usage != nil && msg.StopReason != nil {
		// Streamed lines carry the usage of message_start and no stop reason;
		// their counts are not final (Claude Code puts the totals on the
		// result line). Only a complete message's usage is recorded, else the
		// turn's tokens stay unobserved and gateway rows supply them.
		ev.Tokens = &schemas.Tokens{
			Fresh:      msg.Usage.InputTokens,
			CacheRead:  msg.Usage.CacheReadInputTokens,
			CacheWrite: msg.Usage.CacheCreationInputTokens,
			Out:        msg.Usage.OutputTokens,
		}
	}
	if !subagent && id != st.lastMsgID {
		st.lastMsgID = id
		st.lastText.Reset()
	}
	turn, parentID := ev.TurnIndex, ev.EventID
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if !subagent {
				st.lastText.WriteString(b.Text)
			}
			if st.events[idx].Summary == "" {
				st.events[idx].Summary = clip(oneLine(b.Text), summaryMax)
			}
		case "tool_use":
			st.toolCall(b, turn, parentID)
		}
	}
}

func (st *streamState) toolCall(b contentBlock, turn int, parentID string) {
	i := st.newEvent(schemas.EventToolCall, turn)
	ev := &st.events[i]
	ev.ToolCallID = b.ID
	ev.ToolName = b.Name
	ev.ParentEventID = parentID
	ev.Target = toolTarget(b.Input)
	ev.ArgsHash = hashRaw(b.Input)
	ev.Summary = clip(oneLine(strings.TrimSpace(b.Name+" "+ev.Target)), summaryMax)
	if b.ID != "" {
		st.calls[b.ID] = i
	}
}

// user handles tool results. User lines with plain prompt content are the
// conversation's input, not events.
func (st *streamState) user(obj map[string]json.RawMessage) {
	var msg apiMessage
	if err := json.Unmarshal(obj["message"], &msg); err != nil {
		return
	}
	for _, b := range decodeBlocks(msg.Content) {
		if b.Type != "tool_result" {
			continue
		}
		turn := st.turn
		var parentID, name, target string
		if ci, ok := st.calls[b.ToolUseID]; ok {
			call := st.events[ci]
			turn, parentID, name, target = call.TurnIndex, call.EventID, call.ToolName, call.Target
		}
		i := st.newEvent(schemas.EventToolResult, turn)
		ev := &st.events[i]
		ev.ToolCallID = b.ToolUseID
		ev.ParentEventID = parentID
		ev.ToolName = name
		ev.Target = target
		ev.IsError = b.IsError
		ev.ResultHash = hashRaw(b.Content)
		ev.Summary = clip(oneLine(resultText(b.Content)), summaryMax)
	}
}

func (st *streamState) system(obj map[string]json.RawMessage) {
	sub, _ := rawString(obj["subtype"])
	switch sub {
	case "api_retry":
		i := st.newEvent(schemas.EventRetry, st.turn)
		ev := &st.events[i]
		ev.IsError = true
		attempt, _ := rawInt(obj["attempt"])
		maxRetries, _ := rawInt(obj["max_retries"])
		if attempt > 0 {
			ev.AttemptNo = &attempt
		}
		summary := fmt.Sprintf("api_retry %d/%d: %s", attempt, maxRetries, rawErrorText(obj["error"]))
		if status, ok := rawInt(obj["error_status"]); ok {
			summary += fmt.Sprintf(" (%d)", status)
		}
		ev.Summary = clip(summary, summaryMax)
	case "compact_boundary":
		i := st.newEvent(schemas.EventCompaction, st.turn)
		var meta struct {
			Trigger   string `json:"trigger"`
			PreTokens int64  `json:"pre_tokens"`
		}
		if json.Unmarshal(obj["compact_metadata"], &meta) == nil && meta.Trigger != "" {
			st.events[i].Summary = fmt.Sprintf("compaction: %s at %d tokens", meta.Trigger, meta.PreTokens)
		} else {
			st.events[i].Summary = "compaction"
		}
	}
}

// result records the final text and turns an error result into an event.
// An error result's text is Claude Code's error message (for example
// "API Error: 529 Overloaded..."), not the agent's words, so it never feeds
// the claim.
func (st *streamState) result(obj map[string]json.RawMessage) {
	text, _ := rawString(obj["result"])
	isErr, _ := rawBool(obj["is_error"])
	if !isErr {
		st.resultText = text
		return
	}
	sub, _ := rawString(obj["subtype"])
	detail := text
	var errs []string
	if json.Unmarshal(obj["errors"], &errs) == nil && len(errs) > 0 {
		detail = strings.Join(errs, "; ")
	}
	i := st.newEvent(schemas.EventError, st.turn)
	st.events[i].IsError = true
	st.events[i].Summary = clip(oneLine(strings.TrimSpace("result "+sub+": "+detail)), summaryMax)
}

// claim reads the result text, or the last top-level assistant text when the
// run ended without one (killed at the wall budget, or an error result). A
// stream that was read but holds no text is an observed absence of a claim;
// output with no recognizable line at all gives no claim (unknown).
func (st *streamState) claim() *schemas.Claim {
	text := st.resultText
	if strings.TrimSpace(text) == "" {
		text = st.lastText.String()
	}
	if c := claimFrom(text, schemas.EvidenceNative); c != nil {
		return c
	}
	if st.recognized {
		return &schemas.Claim{Made: false, Source: schemas.EvidenceNative}
	}
	return nil
}

// Claim detection. A sentence claims success when it contains a success
// phrase and no negation; phrases are tried strongest first so the recorded
// match is the most specific one.
var (
	claimPhrases = []*regexp.Regexp{
		regexp.MustCompile(`\ball (?:of )?(?:the )?(?:\d+ )?tests (?:now )?pass(?:ed|es|ing)?\b`),
		regexp.MustCompile(`\btests? (?:now )?pass(?:ed|es|ing)?\b`),
		regexp.MustCompile(`\bfixed\b`),
		regexp.MustCompile(`\bdone\b`),
		regexp.MustCompile(`\bcompleted?\b`),
		regexp.MustCompile(`\bimplemented\b`),
		regexp.MustCompile(`\bsuccessfully\b`),
	}
	negation      = regexp.MustCompile(`\b(?:not|never|cannot|can't|couldn't|didn't|doesn't|don't|isn't|wasn't|won't|haven't|hasn't|unable|fail|failed|unavailable)\b`)
	sentenceBreak = regexp.MustCompile(`[.!?]+(?:\s+|$)|\n+`)
)

func claimFrom(text string, source schemas.EvidenceLevel) *schemas.Claim {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var sentences []string
	for _, s := range sentenceBreak.Split(text, -1) {
		if s = strings.TrimSpace(s); s != "" {
			sentences = append(sentences, s)
		}
	}
	for _, phrase := range claimPhrases {
		for _, s := range sentences {
			norm := strings.ToLower(strings.ReplaceAll(s, "’", "'"))
			m := phrase.FindString(norm)
			if m == "" || negation.MatchString(norm) {
				continue
			}
			return &schemas.Claim{Made: true, Text: clip(oneLine(s), claimTextMax), Source: source, Matched: m}
		}
	}
	return &schemas.Claim{Made: false, Text: clipTail(oneLine(text), claimTextMax), Source: source}
}

// toolTarget returns the first string input field that names what the call
// acts on.
func toolTarget(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range targetKeys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return clip(strings.TrimSpace(s), targetMax)
		}
	}
	return ""
}

// hashRaw hashes the canonical JSON of a value (sorted keys, no HTML
// escaping), so the same tool input hashes the same whatever its key order or
// whitespace in the stream.
func hashRaw(raw json.RawMessage) string {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return utils.SHA256Bytes(raw)
	}
	h, err := utils.HashJSON(v)
	if err != nil {
		return utils.SHA256Bytes(raw)
	}
	return h
}

// decodeBlocks accepts content as a block array or a plain string.
func decodeBlocks(raw json.RawMessage) []contentBlock {
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks
	}
	if s, ok := rawString(raw); ok {
		return []contentBlock{{Type: "text", Text: s}}
	}
	return nil
}

func blocksText(blocks []contentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return oneLine(sb.String())
}

// resultText flattens tool_result content, a string or text blocks.
func resultText(raw json.RawMessage) string {
	if s, ok := rawString(raw); ok {
		return s
	}
	return blocksText(decodeBlocks(raw))
}

func isSubagent(obj map[string]json.RawMessage) bool {
	s, ok := rawString(obj["parent_tool_use_id"])
	return ok && s != ""
}

// rawErrorText reads an error field that is a string (SDKAssistantMessageError)
// or an object with a message.
func rawErrorText(raw json.RawMessage) string {
	if s, ok := rawString(raw); ok {
		return s
	}
	var obj struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Type + " " + obj.Message)
	}
	return ""
}

func rawString(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func rawInt(raw json.RawMessage) (int, bool) {
	var n int
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil {
		return 0, false
	}
	return n, true
}

func rawBool(raw json.RawMessage) (bool, bool) {
	var b bool
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip keeps the first n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}

// clipTail keeps the last n runes: a final message's conclusion is at its end.
func clipTail(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return "..." + string(r[len(r)-(n-3):])
}
