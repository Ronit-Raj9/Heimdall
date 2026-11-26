// Package replayrec turns one real production session, read from the
// owner's Bifrost logs, into a recording: the ordered model calls with their
// answers and the tool results the production agent sent back, all
// redacted. A recording is replayed against another model without running
// any tool (package shadowreplay).
package replayrec

import (
	"encoding/json"
	"time"
)

// FormatVersion is the version of the recording file format.
const FormatVersion = 1

// MaxRecordingBytes bounds one stored recording file.
const MaxRecordingBytes = 8 << 20

// MaxCalls is the number of model calls kept per session.
const MaxCalls = 200

// MaxToolContent bounds one stored tool result.
const MaxToolContent = 64 << 10

// Recording is one production session as a list of model calls.
type Recording struct {
	Version   int            `json:"version"` // FormatVersion
	ID        string         `json:"id"`      // "rec_" + first 12 hex of sha256(source + "\x1f" + session id)
	Source    string         `json:"source"`  // inbox source name of kind bifrost
	SessionID string         `json:"session_id"`
	Provider  string         `json:"provider"` // of the first call
	Model     string         `json:"model"`
	Started   time.Time      `json:"started"`
	Calls     []Call         `json:"calls"`
	Tools     []ToolResult   `json:"tools"`              // every recorded tool result, in order
	Redacted  map[string]int `json:"redacted,omitempty"` // counts by kind, never values
	Gaps      []string       `json:"gaps,omitempty"`     // plain sentences: "call 4 has no stored request"
	FetchedAt time.Time      `json:"fetched_at"`
}

// Call is one model request the production agent sent, and its answer.
type Call struct {
	Index        int             `json:"index"`  // 0-based, time order
	RowID        string          `json:"row_id"` // Bifrost log row id
	At           time.Time       `json:"at"`
	Provider     string          `json:"provider"`
	Model        string          `json:"model"`
	Request      json.RawMessage `json:"request"`       // chat completions body, redacted; model field removed
	RequestFrom  string          `json:"request_from"`  // "raw_request" | "input_history" | "responses"
	ToolsDefined bool            `json:"tools_defined"` // the request carries tool definitions
	RequestHash  string          `json:"request_hash"`  // replay.RequestHash of Request
	HasRedaction bool            `json:"has_redaction"` // Request holds a "[REDACTED " marker
	Answer       Answer          `json:"answer"`
	Status       string          `json:"status"` // "success" | "error"
	CostUSD      *float64        `json:"cost_usd,omitempty"`
	LatencyMS    *float64        `json:"latency_ms,omitempty"`
	Tokens       int             `json:"tokens,omitempty"`
}

// Answer is what the model answered to one call.
type Answer struct {
	Text      string     `json:"text"` // redacted
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Finish    string     `json:"finish,omitempty"` // stop | tool_calls | length | error
}

// ToolCall is one tool call of an answer.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // canonical JSON (keys sorted), redacted
	ArgsHash  string `json:"args_hash"` // sha256 of Arguments, 16 hex
	Write     bool   `json:"write"`     // WriteTool(Name)
}

// ToolResult is a tool answer the production agent sent back to the model:
// a role "tool" message in the request after the call that asked for it.
type ToolResult struct {
	CallID   string `json:"call_id"`
	Name     string `json:"name"`
	ArgsHash string `json:"args_hash"`
	Content  string `json:"content"`  // redacted, at most 64 KiB
	IsError  bool   `json:"is_error"` // same rule as traceimport: isError, an "error" field, or "Error:" prefix
	AskedAt  int    `json:"asked_at"` // index of the Call whose answer asked for it
	SeenAt   int    `json:"seen_at"`  // index of the Call whose request carried it
}

// RedactionMarker starts every placeholder the redactor writes.
const RedactionMarker = "[REDACTED "
