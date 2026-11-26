// Package traceimport turns one recorded agent session into a benchmark task
// draft, so a suite grows from what actually happened in production. It reads
// three local file formats, never the network: OTLP/JSON traces with the
// OpenTelemetry GenAI conventions, Langfuse trace exports, and plain chat
// transcripts (OpenAI-style messages with tool calls). Each is normalized to
// one Session: the user requests, the assistant turns, the tool calls with
// their arguments, results and errors, the final answer, the model, the
// timestamps and what the trace says about the outcome.
//
// A trace shows what happened, not what was right. WriteDraft therefore never
// writes a judgement (an expected answer, an expected final state) without an
// EDIT ME marker that `task validate` refuses, and a draft is never admitted
// on its own. Redact scrubs personal data and credentials before anything is
// written; docs/tasks-from-traces.md is the user guide.
package traceimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Trace formats (Parse, Detect).
const (
	FormatOTLP     = "otlp"     // OTLP/JSON ExportTraceServiceRequest, one object or one per line
	FormatLangfuse = "langfuse" // Langfuse trace with observations, a list of them, or observation rows
	FormatChat     = "chat"     // OpenAI-style messages, one conversation or a list of them
)

// Formats lists the accepted formats in a stable order.
func Formats() []string {
	return []string{FormatOTLP, FormatLangfuse, FormatChat, FormatClaudeCode, FormatCodex}
}

// Size caps. A trace over a cap is refused with an error that names the cap,
// or cut with the cut counted, never read without bound.
const (
	DefaultMaxBytes = 32 << 20 // trace file size
	MaxValues       = 100000   // top-level JSON values (lines of a JSONL file)
	MaxSessions     = 10000    // sessions in one file
	MaxTurns        = 5000     // turns kept per session
	MaxToolCalls    = 1000     // tool calls kept per session
	MaxFieldBytes   = 64 << 10 // one text field (a message, arguments, a result)
)

// Outcome statuses (Outcome.Status). A trace rarely says a session succeeded,
// so unknown is the usual value; ok needs an explicit success signal.
const (
	OutcomeError   = "error"
	OutcomeOK      = "ok"
	OutcomeUnknown = "unknown"
)

// Session is one recorded agent session, normalized from any format.
type Session struct {
	ID          string         `json:"id"` // trace id, or the conversation's id
	Format      string         `json:"format"`
	Name        string         `json:"name,omitempty"`
	Model       string         `json:"model,omitempty"`
	Started     time.Time      `json:"started,omitzero"`
	Ended       time.Time      `json:"ended,omitzero"`
	Turns       []Turn         `json:"turns"`
	ToolCalls   []ToolCall     `json:"tool_calls"`
	FinalAnswer string         `json:"final_answer,omitempty"`
	Outcome     Outcome        `json:"outcome"`
	Skipped     map[string]int `json:"skipped,omitempty"` // content not imported, by kind (image, audio, file, turns, tool calls)
	Truncated   int            `json:"truncated,omitempty"`
	Workspace   *Workspace     `json:"workspace,omitempty"` // coding sessions: where the agent worked
}

// Workspace is where a coding agent worked during a session (Claude Code and
// Codex session files).
type Workspace struct {
	Cwd     string `json:"cwd,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	RepoURL string `json:"repo_url,omitempty"`
}

// Turn is one message of the conversation.
type Turn struct {
	Role string    `json:"role"` // system | user | assistant | tool
	Text string    `json:"text"`
	Time time.Time `json:"time,omitzero"`
}

// ToolCall is one tool invocation the session made.
type ToolCall struct {
	Seq       int             `json:"seq"` // 1-based order in the session
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"` // always valid JSON; text that is not JSON is kept as a JSON string
	Result    string          `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	IsError   bool            `json:"is_error"`
	Started   time.Time       `json:"started,omitzero"`
}

// Outcome is what the trace says about how the session ended. Hints are short
// facts (a span status message, a score, a count of tool errors).
type Outcome struct {
	Status string   `json:"status"`
	Hints  []string `json:"hints,omitempty"`
}

// Trace is every session found in one file.
type Trace struct {
	Format   string
	Sessions []*Session
	Warnings []string
}

// Requests returns the text of every user turn, in order.
func (s *Session) Requests() []string {
	var out []string
	for _, t := range s.Turns {
		if t.Role == "user" && strings.TrimSpace(t.Text) != "" {
			out = append(out, t.Text)
		}
	}
	return out
}

// FirstRequest is the first user request, or "".
func (s *Session) FirstRequest() string {
	if r := s.Requests(); len(r) > 0 {
		return r[0]
	}
	return ""
}

// ToolErrors counts the tool calls that failed.
func (s *Session) ToolErrors() int {
	n := 0
	for _, c := range s.ToolCalls {
		if c.IsError {
			n++
		}
	}
	return n
}

// Summary is the outcome column of `task from-trace -list`: the status, and
// the tool error count when there were any.
func (s *Session) Summary() string {
	out := s.Outcome.Status
	if n := s.ToolErrors(); n > 0 {
		out += fmt.Sprintf(" (%d tool errors)", n)
	}
	return out
}

// ReadFile reads a trace file of at most maxBytes (DefaultMaxBytes when 0).
// A larger file is refused before it is read in full.
func ReadFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat trace: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("trace %s is not a regular file", path)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("trace %s is %d bytes, over the %d byte cap; export a single session", path, info.Size(), maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read trace: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("trace %s is over the %d byte cap; export a single session", path, maxBytes)
	}
	return data, nil
}

// Parse decodes a trace in the given format, or the detected one when format
// is empty. It never panics on malformed input: every failure is an error
// that says what was wrong. It is pure.
func Parse(data []byte, format string) (tr *Trace, err error) {
	defer func() {
		if r := recover(); r != nil {
			tr, err = nil, fmt.Errorf("parse trace: malformed input (%v)", r)
		}
	}()
	values, err := decodeValues(data)
	if err != nil {
		return nil, err
	}
	if format == "" {
		if format, err = detect(values); err != nil {
			return nil, err
		}
	}
	tr = &Trace{Format: format}
	switch format {
	case FormatOTLP:
		tr.Sessions, err = parseOTLP(values)
	case FormatLangfuse:
		tr.Sessions, err = parseLangfuse(values)
	case FormatChat:
		tr.Sessions, err = parseChat(values)
	case FormatClaudeCode:
		tr.Sessions, err = parseClaudeCode(values)
	case FormatCodex:
		tr.Sessions, err = parseCodex(values)
	default:
		return nil, fmt.Errorf("format %q: want one of %s", format, strings.Join(Formats(), ", "))
	}
	if err != nil {
		return nil, fmt.Errorf("parse trace as %s: %w", format, err)
	}
	if len(tr.Sessions) == 0 {
		return nil, fmt.Errorf("parse trace as %s: no session found", format)
	}
	if len(tr.Sessions) > MaxSessions {
		tr.Warnings = append(tr.Warnings, fmt.Sprintf("%d sessions found; only the first %d are kept", len(tr.Sessions), MaxSessions))
		tr.Sessions = tr.Sessions[:MaxSessions]
	}
	for _, s := range tr.Sessions {
		s.Format = format
		finish(s)
	}
	return tr, nil
}

// Detect names the format of a trace file's content.
func Detect(data []byte) (string, error) {
	values, err := decodeValues(data)
	if err != nil {
		return "", err
	}
	return detect(values)
}

// Pick returns the session with the given id, or the only session when id is
// empty. A file with several sessions and no id is an error that lists them.
func (t *Trace) Pick(id string) (*Session, error) {
	if id == "" {
		if len(t.Sessions) == 1 {
			return t.Sessions[0], nil
		}
		ids := make([]string, 0, min(len(t.Sessions), 10))
		for i, s := range t.Sessions {
			if i == 10 {
				ids = append(ids, fmt.Sprintf("and %d more", len(t.Sessions)-10))
				break
			}
			ids = append(ids, s.ID)
		}
		return nil, fmt.Errorf("the trace holds %d sessions (%s); pick one with -session, or see them with -list", len(t.Sessions), strings.Join(ids, ", "))
	}
	for _, s := range t.Sessions {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no session %q in the trace; see the sessions with -list", id)
}

// decodeValues reads one JSON value, or a stream of them (JSON Lines).
func decodeValues(data []byte) ([]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("parse trace: the file is empty")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("parse trace: the file is not UTF-8 text")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var out []any
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse trace: invalid JSON near byte %d: %w", dec.InputOffset(), err)
		}
		if len(out) == MaxValues {
			return nil, fmt.Errorf("parse trace: more than %d JSON values; export fewer sessions", MaxValues)
		}
		out = append(out, v)
	}
	return out, nil
}

// detect tells the format from the decoded values' keys.
func detect(values []any) (string, error) {
	// Coding session files first: their lines also carry a role or a
	// message, which the chat check would take.
	for _, v := range values {
		if m := asMap(v); m != nil && looksClaudeCode(m) {
			return FormatClaudeCode, nil
		}
	}
	for _, v := range values {
		if m := asMap(v); m != nil && looksCodex(m) {
			return FormatCodex, nil
		}
	}
	for _, v := range values {
		if f := detectOne(v); f != "" {
			return f, nil
		}
	}
	return "", fmt.Errorf("cannot tell the trace format; pass -format %s", strings.Join(Formats(), "|"))
}

func detectOne(v any) string {
	switch x := v.(type) {
	case map[string]any:
		switch {
		case x["resourceSpans"] != nil || x["resource_spans"] != nil:
			return FormatOTLP
		case isLangfuseTrace(x) || isLangfuseObservation(x):
			return FormatLangfuse
		case asArray(x["messages"]) != nil:
			return FormatChat
		}
		if data := asArray(x["data"]); len(data) > 0 {
			return detectOne(data[0])
		}
		if role := asString(x["role"]); role != "" {
			return FormatChat
		}
	case []any:
		if len(x) > 0 {
			return detectOne(x[0])
		}
	}
	return ""
}

// finish fills what every format derives the same way: the final answer when
// the format gave none, sequence numbers, times and the outcome status.
func finish(s *Session) {
	if s.FinalAnswer == "" {
		for i := len(s.Turns) - 1; i >= 0; i-- {
			if s.Turns[i].Role == "assistant" && strings.TrimSpace(s.Turns[i].Text) != "" {
				s.FinalAnswer = s.Turns[i].Text
				break
			}
		}
	}
	for i := range s.ToolCalls {
		s.ToolCalls[i].Seq = i + 1
		if len(s.ToolCalls[i].Arguments) == 0 {
			s.ToolCalls[i].Arguments = json.RawMessage("{}")
		}
	}
	if s.Outcome.Status == "" {
		s.Outcome.Status = OutcomeUnknown
	}
	if n := s.ToolErrors(); n > 0 {
		s.Outcome.Hints = append(s.Outcome.Hints, fmt.Sprintf("%d of %d tool calls failed", n, len(s.ToolCalls)))
	}
	if s.Started.IsZero() || s.Ended.IsZero() {
		var times []time.Time
		for _, t := range s.Turns {
			if !t.Time.IsZero() {
				times = append(times, t.Time)
			}
		}
		for _, c := range s.ToolCalls {
			if !c.Started.IsZero() {
				times = append(times, c.Started)
			}
		}
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		if len(times) > 0 && s.Started.IsZero() {
			s.Started = times[0]
		}
		if len(times) > 0 && s.Ended.IsZero() {
			s.Ended = times[len(times)-1]
		}
	}
}
