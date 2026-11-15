package traceimport

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// message is one chat message in any of the shapes the formats carry:
// OpenAI chat messages, OpenTelemetry GenAI messages with parts, and
// Anthropic-style content blocks.
type message struct {
	role    string
	text    string
	time    time.Time
	calls   []callPart   // tool calls an assistant message requested
	results []resultPart // tool results a tool message carried
	skipped []string     // kinds of content not imported (image, audio, file)
}

type callPart struct {
	id, name string
	args     any
}

type resultPart struct {
	id, name string
	result   any
	isErr    bool
}

// builder accumulates one Session. LLM spans and generations each carry the
// whole history so far, so addMessages appends only the messages it has not
// seen yet, counting repeats: a history that grows by one turn adds one turn.
type builder struct {
	s        *Session
	have     map[string]int
	callByID map[string]int
}

func newBuilder(id string) *builder {
	return &builder{s: &Session{ID: id}, have: map[string]int{}, callByID: map[string]int{}}
}

// addMessages appends the messages of one history that are new.
func (b *builder) addMessages(msgs []message) {
	seen := map[string]int{}
	for _, m := range msgs {
		k := m.key()
		seen[k]++
		if seen[k] > b.have[k] {
			b.have[k]++
			b.appendMessage(m)
		}
	}
}

// appendMessage adds one message as a turn and records its tool parts.
func (b *builder) appendMessage(m message) {
	if m.text != "" || (len(m.calls) == 0 && len(m.results) == 0) {
		if len(b.s.Turns) >= MaxTurns {
			b.skip("turns")
		} else {
			b.s.Turns = append(b.s.Turns, Turn{Role: m.role, Text: b.clip(m.text), Time: m.time})
		}
	}
	for _, k := range m.skipped {
		b.skip(k)
	}
	for _, c := range m.calls {
		b.requestCall(c, m.time)
	}
	for _, r := range m.results {
		b.resultCall(r, m.time)
	}
}

// requestCall records a tool call a model asked for.
func (b *builder) requestCall(c callPart, t time.Time) {
	if c.id != "" {
		if _, ok := b.callByID[c.id]; ok {
			return
		}
	}
	b.newCall(c.id, c.name, c.args, t)
}

// resultCall attaches a tool result to its call: by id, else the first call
// of the same name still without a result, else a new call.
func (b *builder) resultCall(r resultPart, t time.Time) {
	i := b.findOpen(r.id, r.name)
	if i < 0 {
		i = b.newCall(r.id, r.name, nil, t)
		if i < 0 {
			return
		}
	}
	b.setResult(i, r.result, "", r.isErr)
}

// toolSpan records a tool execution the trace observed directly (an
// execute_tool span, a Langfuse TOOL observation). It is the most reliable
// record of a call, so its arguments and result win over message parts.
func (b *builder) toolSpan(id, name string, args, result any, errText string, isErr bool, t time.Time) {
	i := b.findOpen(id, name)
	if i < 0 {
		if i = b.newCall(id, name, args, t); i < 0 {
			return
		}
	}
	c := &b.s.ToolCalls[i]
	if args != nil {
		c.Arguments = b.rawJSON(args)
	}
	if c.Name == "" || c.Name == "unknown" {
		c.Name = name
	}
	if c.Started.IsZero() {
		c.Started = t
	}
	b.setResult(i, result, errText, isErr)
}

func (b *builder) findOpen(id, name string) int {
	if id != "" {
		if i, ok := b.callByID[id]; ok {
			return i
		}
	}
	for i, c := range b.s.ToolCalls {
		if c.Result == "" && c.Error == "" && (name == "" || c.Name == name) && (id == "" || c.ID == "") {
			return i
		}
	}
	return -1
}

func (b *builder) newCall(id, name string, args any, t time.Time) int {
	if len(b.s.ToolCalls) >= MaxToolCalls {
		b.skip("tool calls")
		return -1
	}
	if name == "" {
		name = "unknown"
	}
	b.s.ToolCalls = append(b.s.ToolCalls, ToolCall{ID: id, Name: name, Arguments: b.rawJSON(args), Started: t})
	i := len(b.s.ToolCalls) - 1
	if id != "" {
		b.callByID[id] = i
	}
	return i
}

func (b *builder) setResult(i int, result any, errText string, isErr bool) {
	c := &b.s.ToolCalls[i]
	text, flagged := resultText(result)
	if text != "" {
		c.Result = b.clip(text)
	}
	if errText != "" {
		c.Error = b.clip(errText)
	}
	if isErr || flagged || errText != "" {
		c.IsError = true
	}
}

func (b *builder) skip(kind string) {
	if b.s.Skipped == nil {
		b.s.Skipped = map[string]int{}
	}
	b.s.Skipped[kind]++
}

// clip cuts a text field at MaxFieldBytes on a rune boundary.
func (b *builder) clip(s string) string {
	if len(s) <= MaxFieldBytes {
		return s
	}
	cut := MaxFieldBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	b.s.Truncated++
	return s[:cut] + fmt.Sprintf("\n[truncated: %d bytes not imported]", len(s)-cut)
}

// rawJSON turns tool arguments into valid JSON: objects and arrays as they
// are, a string that holds JSON as that JSON, any other text as a string.
func (b *builder) rawJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("{}")
	}
	if s, ok := v.(string); ok {
		t := strings.TrimSpace(s)
		if t == "" {
			return json.RawMessage("{}")
		}
		var parsed any
		if json.Unmarshal([]byte(t), &parsed) == nil {
			v = parsed
		}
	}
	out, err := json.Marshal(v)
	if err != nil {
		out, _ = json.Marshal(fmt.Sprint(v))
	}
	if len(out) > MaxFieldBytes {
		out, _ = json.Marshal(b.clip(string(out)))
	}
	return out
}

// key identifies a message for addMessages.
func (m message) key() string {
	var sb strings.Builder
	sb.WriteString(m.role)
	sb.WriteByte(0)
	sb.WriteString(m.text)
	for _, c := range m.calls {
		sb.WriteString("\x00c:" + c.id + ":" + c.name)
	}
	for _, r := range m.results {
		sb.WriteString("\x00r:" + r.id + ":" + r.name)
	}
	return sb.String()
}

// parseMessages reads a list of messages: an array, or an object holding one
// under "messages". Content the bench cannot import (images, audio, files) is
// skipped and counted when the message is appended.
func parseMessages(v any) []message {
	list := asArray(v)
	if list == nil {
		if m := asMap(v); m != nil {
			list = asArray(m["messages"])
		}
	}
	var out []message
	for _, item := range list {
		if m, ok := parseMessage(item); ok {
			out = append(out, m)
		}
	}
	return out
}

// parseMessage reads one message in any supported shape.
func parseMessage(v any) (message, bool) {
	obj := asMap(v)
	if obj == nil {
		return message{}, false
	}
	m := message{role: normRole(asString(obj["role"])), time: parseTime(firstOf(obj, "timestamp", "created_at", "time"))}
	if m.role == "" {
		return message{}, false
	}
	var texts []string
	addParts := func(parts []any) {
		for _, p := range parts {
			texts = append(texts, readPart(p, &m)...)
		}
	}
	switch c := obj["content"].(type) {
	case string:
		texts = append(texts, c)
	case []any:
		addParts(c)
	case map[string]any:
		addParts([]any{c})
	}
	addParts(asArray(obj["parts"]))
	for _, tc := range asArray(obj["tool_calls"]) {
		if call, ok := openAICall(tc); ok {
			m.calls = append(m.calls, call)
		}
	}
	if fc := asMap(obj["function_call"]); fc != nil {
		m.calls = append(m.calls, callPart{name: asString(fc["name"]), args: fc["arguments"]})
	}
	switch m.role {
	case "tool":
		id := asString(firstOf(obj, "tool_call_id", "tool_use_id", "id"))
		if len(m.results) == 0 {
			m.results = append(m.results, resultPart{id: id, name: asString(obj["name"]), result: strings.Join(texts, "\n"), isErr: asBool(obj["is_error"])})
			texts = nil
		}
	case "function":
		m.role = "tool"
		m.results = append(m.results, resultPart{name: asString(obj["name"]), result: strings.Join(texts, "\n")})
		texts = nil
	}
	m.text = strings.TrimSpace(strings.Join(texts, "\n"))
	return m, true
}

// readPart reads one content part and returns its text, if any. Tool call
// and tool result parts are added to m.
func readPart(p any, m *message) []string {
	if s, ok := p.(string); ok {
		return []string{s}
	}
	obj := asMap(p)
	if obj == nil {
		return nil
	}
	switch typ := asString(obj["type"]); typ {
	case "text", "input_text", "output_text", "":
		if s := asString(firstOf(obj, "text", "content")); s != "" {
			return []string{s}
		}
	case "tool_call", "function_call", "tool_use":
		m.calls = append(m.calls, callPart{id: asString(firstOf(obj, "id", "call_id")), name: asString(obj["name"]), args: firstOf(obj, "arguments", "input")})
	case "tool_call_response", "function_call_output", "tool_result":
		m.results = append(m.results, resultPart{id: asString(firstOf(obj, "id", "call_id", "tool_use_id")), name: asString(obj["name"]),
			result: firstOf(obj, "response", "result", "output", "content"), isErr: asBool(obj["is_error"])})
	case "image", "image_url", "input_image", "blob", "uri":
		m.skipped = append(m.skipped, "image")
	case "input_audio", "audio":
		m.skipped = append(m.skipped, "audio")
	case "file", "input_file", "document":
		m.skipped = append(m.skipped, "file")
	case "reasoning", "thinking":
		// Model reasoning is not part of the conversation the task replays.
	default:
		m.skipped = append(m.skipped, typ)
	}
	return nil
}

// openAICall reads one entry of an OpenAI tool_calls list.
func openAICall(v any) (callPart, bool) {
	obj := asMap(v)
	if obj == nil {
		return callPart{}, false
	}
	if fn := asMap(obj["function"]); fn != nil {
		return callPart{id: asString(obj["id"]), name: asString(fn["name"]), args: fn["arguments"]}, true
	}
	return callPart{id: asString(obj["id"]), name: asString(obj["name"]), args: firstOf(obj, "arguments", "input", "args")}, true
}

// resultText renders a tool result as text and reports whether the result
// itself says it failed: an MCP result with isError, an object with an error
// field, or text starting with "Error:".
func resultText(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		t := strings.TrimSpace(x)
		if strings.HasPrefix(t, "{") {
			var obj map[string]any
			if json.Unmarshal([]byte(t), &obj) == nil {
				// Keep the recorded text unless it is an MCP result whose
				// text has to be taken out of its content list.
				text, flagged := resultText(obj)
				if _, mcp := obj["isError"]; mcp {
					return text, flagged
				}
				return x, flagged
			}
		}
		return x, strings.HasPrefix(strings.ToLower(t), "error:")
	case map[string]any:
		content := asArray(x["content"])
		if _, mcp := x["isError"]; mcp || (len(content) > 0 && asString(asMap(content[0])["type"]) == "text") {
			var parts []string
			for _, c := range content {
				if s := asString(asMap(c)["text"]); s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, "\n"), asBool(x["isError"])
		}
		e := x["error"]
		return compactJSON(x), e != nil && e != false && e != ""
	default:
		return textOf(v), false
	}
}

// textOf renders any decoded JSON value as text.
func textOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		return compactJSON(v)
	}
}

func compactJSON(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(out)
}

func normRole(r string) string {
	switch r = strings.ToLower(strings.TrimSpace(r)); r {
	case "human":
		return "user"
	case "ai", "model", "bot":
		return "assistant"
	case "developer":
		return "system"
	}
	return r
}

// parseTime reads an RFC 3339 string, or a number of seconds, milliseconds
// or nanoseconds since the epoch (told apart by size). Anything else is the
// zero time.
func parseTime(v any) time.Time {
	switch x := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t.UTC()
		}
		if n, err := strconv.ParseInt(x, 10, 64); err == nil {
			return epoch(n)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return epoch(n)
		}
		if f, err := x.Float64(); err == nil {
			return time.Unix(0, int64(f*1e9)).UTC()
		}
	}
	return time.Time{}
}

func epoch(n int64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n < 1e11:
		return time.Unix(n, 0).UTC()
	case n < 1e14:
		return time.UnixMilli(n).UTC()
	case n < 1e17:
		return time.UnixMicro(n).UTC()
	}
	return time.Unix(0, n).UTC()
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asArray(v any) []any {
	a, _ := v.([]any)
	return a
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	}
	return false
}

// firstOf returns the first key of obj that holds a non-null value.
func firstOf(obj map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := obj[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// kindOf names the JSON type of a decoded value, for error messages.
func kindOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	}
	return "object"
}
