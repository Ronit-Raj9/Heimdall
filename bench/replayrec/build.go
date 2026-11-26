package replayrec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/maximhq/bifrost/bench/replay"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Row is the subset of a Bifrost detail row the builder reads (the JSON
// names of gateways/bifrost/detail.go detailRow and traceinbox bifrostRow).
type Row struct {
	ID                    string            `json:"id"`
	Timestamp             time.Time         `json:"timestamp"`
	Status                string            `json:"status"`
	Provider              string            `json:"provider"`
	Model                 string            `json:"model"`
	SessionID             *string           `json:"session_id"`
	FallbackIndex         int               `json:"fallback_index"`
	RawRequest            string            `json:"raw_request"`
	RawResponse           string            `json:"raw_response"`
	InputHistory          []json.RawMessage `json:"input_history"`
	OutputMessage         json.RawMessage   `json:"output_message"`
	ResponsesInputHistory []json.RawMessage `json:"responses_input_history"`
	ResponsesOutput       []json.RawMessage `json:"responses_output"`
	Cost                  *float64          `json:"cost"`
	Latency               *float64          `json:"latency"`
	TokenUsage            *struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"token_usage"`
}

// RecordingID names the recording of one session of one source.
func RecordingID(source, sessionID string) string {
	sum := sha256.Sum256([]byte(source + "\x1f" + sessionID))
	return "rec_" + hex.EncodeToString(sum[:])[:12]
}

// Build makes a recording from the rows of one session, any order. Rows
// with FallbackIndex > 0 are dropped (the fallback of a call is the same
// call). The request is raw_request when present (it has the tool
// definitions), else {"messages": input_history} with ToolsDefined false,
// else the Responses rows through traceimport.ResponsesToChat. Tool results
// are the role "tool" messages of call k+1's request that answer a tool
// call of call k's answer, matched by tool_call_id. Everything is redacted
// with one traceimport.Redactor before it is stored.
func Build(source string, rows []Row) (*Recording, error) {
	var kept []Row
	for _, r := range rows {
		if r.FallbackIndex > 0 {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == 0 {
		return nil, errors.New("the session has no model calls")
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Timestamp.Before(kept[j].Timestamp) })
	rec := &Recording{Version: FormatVersion, Source: source, FetchedAt: time.Now().UTC()}
	for _, r := range kept {
		if r.SessionID != nil && *r.SessionID != "" {
			rec.SessionID = *r.SessionID
			break
		}
	}
	if rec.SessionID == "" {
		rec.SessionID = kept[0].ID
	}
	rec.ID = RecordingID(source, rec.SessionID)
	if len(kept) > MaxCalls {
		rec.Gaps = append(rec.Gaps, fmt.Sprintf("the session has %d calls; only the first %d are kept", len(kept), MaxCalls))
		kept = kept[:MaxCalls]
	}
	rd := traceimport.NewRedactor()
	for i, r := range kept {
		c := Call{Index: i, RowID: r.ID, At: r.Timestamp, Provider: r.Provider, Model: r.Model, Status: r.Status,
			CostUSD: r.Cost, LatencyMS: r.Latency}
		if c.Status == "" {
			c.Status = "success"
		}
		if r.TokenUsage != nil {
			c.Tokens = r.TokenUsage.TotalTokens
		}
		req, from, tools := requestOf(r)
		if req == nil {
			rec.Gaps = append(rec.Gaps, fmt.Sprintf("call %d has no stored request", i))
		} else {
			req = rd.JSON(req)
			c.Request, c.RequestFrom, c.ToolsDefined = req, from, tools
			c.RequestHash = replay.RequestHash(req)
			c.HasRedaction = bytes.Contains(req, []byte(RedactionMarker))
		}
		ans, err := answerOf(r)
		if err != nil {
			rec.Gaps = append(rec.Gaps, fmt.Sprintf("call %d has no stored answer", i))
		}
		ans.Text = rd.Text(ans.Text)
		for j := range ans.ToolCalls {
			tc := &ans.ToolCalls[j]
			tc.Arguments = redactArgs(rd, tc.Arguments)
			tc.ArgsHash = argsHash(tc.Arguments)
			tc.Write = WriteTool(tc.Name)
		}
		if c.Status == "error" && ans.Finish == "" {
			ans.Finish = "error"
		}
		c.Answer = ans
		if len(ans.ToolCalls) > 0 && !c.ToolsDefined {
			rec.Gaps = append(rec.Gaps, fmt.Sprintf("call %d used tools, but its stored request has no tool definitions", i))
		}
		rec.Calls = append(rec.Calls, c)
	}
	// Tool results: the tool messages of call k+1 that answer call k.
	for k := 0; k+1 < len(rec.Calls); k++ {
		asked := map[string]ToolCall{}
		for _, tc := range rec.Calls[k].Answer.ToolCalls {
			asked[tc.ID] = tc
		}
		if len(asked) == 0 {
			continue
		}
		for _, m := range Messages(rec.Calls[k+1].Request) {
			if m.Role != "tool" {
				continue
			}
			tc, ok := asked[m.ToolCallID]
			if !ok {
				continue
			}
			delete(asked, m.ToolCallID)
			content, isErr := toolContent(m.Content)
			if len(content) > MaxToolContent {
				content = content[:MaxToolContent]
			}
			rec.Tools = append(rec.Tools, ToolResult{CallID: tc.ID, Name: tc.Name, ArgsHash: tc.ArgsHash, Content: content,
				IsError: isErr, AskedAt: k, SeenAt: k + 1})
		}
	}
	rec.Provider, rec.Model, rec.Started = rec.Calls[0].Provider, rec.Calls[0].Model, rec.Calls[0].At
	if rep := rd.Report(); rep.Total() > 0 {
		rec.Redacted = map[string]int(rep)
	}
	return rec, nil
}

// requestOf picks the stored request of a row, without its model field.
func requestOf(r Row) (json.RawMessage, string, bool) {
	if strings.TrimSpace(r.RawRequest) != "" {
		var body map[string]json.RawMessage
		if json.Unmarshal([]byte(r.RawRequest), &body) == nil && body != nil {
			delete(body, "model")
			tools := len(body["tools"]) > 0 && string(body["tools"]) != "null" && string(body["tools"]) != "[]"
			out, _ := json.Marshal(body)
			return out, "raw_request", tools
		}
	}
	if len(r.InputHistory) > 0 {
		out, _ := json.Marshal(map[string]any{"messages": r.InputHistory})
		return out, "input_history", false
	}
	if len(r.ResponsesInputHistory) > 0 {
		msgs := traceimport.ResponsesToChat(r.ResponsesInputHistory, nil)
		if len(msgs) > 0 {
			out, _ := json.Marshal(map[string]any{"messages": msgs})
			return out, "responses", false
		}
	}
	return nil, "", false
}

// answerOf reads the answer of a row from its raw response, its output
// message, or its Responses output.
func answerOf(r Row) (Answer, error) {
	if strings.TrimSpace(r.RawResponse) != "" {
		if a, err := ParseAnswer([]byte(r.RawResponse)); err == nil {
			return a, nil
		}
	}
	if len(r.OutputMessage) > 0 && string(r.OutputMessage) != "null" {
		var m Message
		if json.Unmarshal(r.OutputMessage, &m) == nil {
			return answerOfMessage(m, ""), nil
		}
	}
	if len(r.ResponsesOutput) > 0 {
		var a Answer
		for _, raw := range traceimport.ResponsesToChat(nil, r.ResponsesOutput) {
			var m Message
			if json.Unmarshal(raw, &m) != nil || m.Role != "assistant" {
				continue
			}
			b := answerOfMessage(m, "")
			if b.Text != "" {
				a.Text = strings.TrimSpace(a.Text + "\n" + b.Text)
			}
			a.ToolCalls = append(a.ToolCalls, b.ToolCalls...)
		}
		a.Finish = "stop"
		if len(a.ToolCalls) > 0 {
			a.Finish = "tool_calls"
		}
		return a, nil
	}
	if r.Status == "error" {
		return Answer{Finish: "error"}, nil
	}
	return Answer{}, errors.New("no answer")
}

// Message is one chat message as the replay reads it.
type Message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []struct {
		ID       string `json:"id"`
		Function struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls,omitempty"`
}

// Messages returns the messages of a chat completions request body.
func Messages(req json.RawMessage) []Message {
	var body struct {
		Messages []Message `json:"messages"`
	}
	_ = json.Unmarshal(req, &body)
	return body.Messages
}

// ParseAnswer reads the first choice of a chat completions response body.
// Tool call arguments come back canonical when they are JSON.
func ParseAnswer(body []byte) (Answer, error) {
	var resp struct {
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Answer{}, fmt.Errorf("the answer is not a chat completions body: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Answer{}, errors.New("the answer has no choices")
	}
	a := answerOfMessage(resp.Choices[0].Message, resp.Choices[0].FinishReason)
	for i := range a.ToolCalls {
		a.ToolCalls[i].ArgsHash = argsHash(a.ToolCalls[i].Arguments)
		a.ToolCalls[i].Write = WriteTool(a.ToolCalls[i].Name)
	}
	return a, nil
}

func answerOfMessage(m Message, finish string) Answer {
	a := Answer{Finish: finish}
	text, _ := toolContent(m.Content)
	a.Text = text
	for _, tc := range m.ToolCalls {
		a.ToolCalls = append(a.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: canonicalOrText(argString(tc.Function.Arguments))})
	}
	// Responses calls arrive as content parts (traceimport.ResponsesToChat).
	var parts []map[string]any
	if json.Unmarshal(m.Content, &parts) == nil {
		a.Text = ""
		var texts []string
		for _, p := range parts {
			switch p["type"] {
			case "function_call":
				id, _ := p["call_id"].(string)
				name, _ := p["name"].(string)
				var args string
				switch v := p["arguments"].(type) {
				case string:
					args = v
				case nil:
					args = "{}"
				default:
					b, _ := json.Marshal(v)
					args = string(b)
				}
				a.ToolCalls = append(a.ToolCalls, ToolCall{ID: id, Name: name, Arguments: canonicalOrText(args)})
			default:
				if s, ok := p["text"].(string); ok && s != "" {
					texts = append(texts, s)
				}
			}
		}
		a.Text = strings.Join(texts, "\n")
	}
	if a.Finish == "" {
		a.Finish = "stop"
		if len(a.ToolCalls) > 0 {
			a.Finish = "tool_calls"
		}
	}
	return a
}

// argString reads tool call arguments sent as a JSON string or as an object.
func argString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func canonicalOrText(args string) string {
	if c, err := Canonical(args); err == nil {
		return c
	}
	return args
}

func redactArgs(rd *traceimport.Redactor, args string) string {
	if c, err := Canonical(args); err == nil {
		if out, err := Canonical(string(rd.JSON(json.RawMessage(c)))); err == nil {
			return out
		}
	}
	return rd.Text(args)
}

func argsHash(args string) string {
	sum := sha256.Sum256([]byte(args))
	return hex.EncodeToString(sum[:])[:16]
}

// toolContent renders a message content (a string, a list of parts, or an
// object) as text, and says whether it reports a failure: an MCP result
// with isError, an object with an error field, or text starting with
// "Error:".
func toolContent(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		t := strings.TrimSpace(s)
		if strings.HasPrefix(t, "{") {
			var obj map[string]any
			if json.Unmarshal([]byte(t), &obj) == nil {
				if b, ok := obj["isError"].(bool); ok {
					return s, b
				}
				if e, ok := obj["error"]; ok && e != nil && e != false && e != "" {
					return s, true
				}
			}
		}
		return s, strings.HasPrefix(strings.ToLower(t), "error:")
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) == nil {
		var texts []string
		isErr := false
		for _, p := range parts {
			if t, ok := p["text"].(string); ok {
				texts = append(texts, t)
				continue
			}
			if out, ok := p["output"]; ok {
				b, _ := json.Marshal(out)
				t, e := toolContent(b)
				texts, isErr = append(texts, t), isErr || e
			}
		}
		return strings.Join(texts, "\n"), isErr
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if b, ok := obj["isError"].(bool); ok && b {
			return string(raw), true
		}
		e, ok := obj["error"]
		return string(raw), ok && e != nil && e != false && e != ""
	}
	return string(raw), false
}

// writeWords are the first words of tool names that change something.
var writeWords = map[string]bool{
	"create": true, "add": true, "insert": true, "update": true, "set": true, "edit": true, "write": true,
	"delete": true, "remove": true, "send": true, "post": true, "put": true, "patch": true, "pay": true,
	"refund": true, "transfer": true, "cancel": true, "book": true, "order": true, "submit": true,
	"approve": true, "apply": true, "issue": true, "close": true, "merge": true, "deploy": true,
	"run": true, "exec": true, "execute": true,
}

// WriteTool says whether a tool name looks like it changes something:
// its first word (split on _ - . and camel case) is one of create, add,
// insert, update, set, edit, write, delete, remove, send, post, put, patch,
// pay, refund, transfer, cancel, book, order, submit, approve, apply, issue,
// close, merge, deploy, run, exec, execute.
func WriteTool(name string) bool {
	var first []rune
	for i, r := range name {
		if r == '_' || r == '-' || r == '.' || r == ' ' {
			if len(first) > 0 {
				break
			}
			continue
		}
		if i > 0 && unicode.IsUpper(r) && len(first) > 0 {
			break
		}
		first = append(first, unicode.ToLower(r))
	}
	return writeWords[string(first)]
}
