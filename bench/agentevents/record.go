// Package agentevents turns the steps an agent reports about itself into
// timeline events (docs/agent-events.md). An agent reports through the run's
// event collector (OTLP/HTTP traces with the GenAI or OpenInference
// conventions, or the native event protocol of spec/agent-event.schema.json),
// an events file in the aux volume, or prefixed stdout lines. Every byte is
// the agent's own word: the package caps every field, redacts secrets, and
// the events it returns carry the agent_reported origin with partial
// strength, so they can be shown and counted on their own but never raise a
// trusted metric or decide an outcome.
//
// The package has no dependency beyond schemas and utils, so the collector
// (toolproxy), the evidence pipeline and remote agent adapters share it.
package agentevents

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Reported step types. The first thirteen are the native protocol's; agent,
// llm and span come only from OTLP spans (a root agent span, a model call
// the agent reports, any other span).
const (
	TypePlan           = "plan"
	TypeThoughtSummary = "thought_summary"
	TypeToolCall       = "tool_call"
	TypeToolResult     = "tool_result"
	TypeSubagentStart  = "subagent_start"
	TypeSubagentEnd    = "subagent_end"
	TypeHandoff        = "handoff"
	TypeMemoryRead     = "memory_read"
	TypeMemoryWrite    = "memory_write"
	TypeRetrieval      = "retrieval"
	TypeMessage        = "message"
	TypeError          = "error"
	TypeCustom         = "custom"
	TypeAgent          = "agent"
	TypeLLM            = "llm"
	TypeSpan           = "span"
)

var nativeTypes = map[string]bool{
	TypePlan: true, TypeThoughtSummary: true, TypeToolCall: true, TypeToolResult: true,
	TypeSubagentStart: true, TypeSubagentEnd: true, TypeHandoff: true, TypeMemoryRead: true,
	TypeMemoryWrite: true, TypeRetrieval: true, TypeMessage: true, TypeError: true, TypeCustom: true,
}

// InternalStep reports whether a reported type is an internal action the
// reported_steps metric counts: a plan, a subagent run, a handoff, a memory
// operation, a retrieval or a tool call. Messages, thought summaries, errors,
// model calls (the gateway counts those) and generic spans are not actions.
func InternalStep(typ string) bool {
	switch typ {
	case TypePlan, TypeSubagentStart, TypeHandoff, TypeMemoryRead, TypeMemoryWrite, TypeRetrieval, TypeToolCall:
		return true
	}
	return false
}

// Statuses of a reported step.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusStarted = "started"
	StatusUnknown = "unknown"
)

// Record is one reported step after decoding and capping, before it becomes
// an event. The collector stores records, one per line, in its ledger.
type Record struct {
	Channel    string            `json:"channel"`
	Type       string            `json:"type"`
	ID         string            `json:"id,omitempty"`
	ParentID   string            `json:"parent_id,omitempty"`
	TraceID    string            `json:"trace_id,omitempty"`
	Name       string            `json:"name,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Input      string            `json:"input,omitempty"`
	Output     string            `json:"output,omitempty"`
	Status     string            `json:"status,omitempty"`
	Start      time.Time         `json:"start,omitzero"`
	End        time.Time         `json:"end,omitzero"`
	Received   time.Time         `json:"received,omitzero"`
	Model      string            `json:"model,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Limits bound what one run can report. A zero field takes its default.
type Limits struct {
	MaxEvents int   // events kept per run
	MaxBytes  int64 // bytes of kept records per run
	MaxBody   int64 // bytes of one collector request
	MaxText   int   // bytes of an input or output
}

// Default limits.
const (
	DefaultMaxEvents = 5000
	DefaultMaxBytes  = 8 << 20
	DefaultMaxBody   = 4 << 20
	DefaultMaxText   = 2048
	maxID            = 128
	maxName          = 256
	maxAttrs         = 32
	maxAttrValue     = 256
	maxAttrKey       = 64
)

// WithDefaults fills zero fields.
func (l Limits) WithDefaults() Limits {
	if l.MaxEvents <= 0 {
		l.MaxEvents = DefaultMaxEvents
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultMaxBytes
	}
	if l.MaxBody <= 0 {
		l.MaxBody = DefaultMaxBody
	}
	if l.MaxText <= 0 {
		l.MaxText = DefaultMaxText
	}
	return l
}

// tokenPattern catches bare API keys that carry no key name.
var tokenPattern = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|sk_[A-Za-z0-9]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16})\b`)

// Redact removes secrets from agent text: the bench's redaction rules, bare
// API key shapes and the given literal secrets (the run's gateway key).
func Redact(s string, secrets ...string) string {
	for _, k := range secrets {
		if len(k) >= 8 {
			s = strings.ReplaceAll(s, k, "[redacted]")
		}
	}
	s = utils.RedactSecrets(s)
	return tokenPattern.ReplaceAllString(s, "[redacted]")
}

// clip cuts s to at most n bytes on a rune boundary, valid UTF-8, with no
// control characters except newline and tab.
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// sanitize caps every field of r in place and redacts generic secrets. The
// run key is redacted again when records become events.
func sanitize(r *Record, lim Limits) {
	r.Type = normType(r.Type)
	r.ID = clip(Redact(r.ID), maxID)
	r.ParentID = clip(Redact(r.ParentID), maxID)
	r.TraceID = clip(r.TraceID, maxID)
	r.ToolCallID = clip(Redact(r.ToolCallID), maxID)
	r.Name = clip(Redact(r.Name), maxName)
	r.Model = clip(Redact(r.Model), maxName)
	r.Input = clip(Redact(r.Input), lim.MaxText)
	r.Output = clip(Redact(r.Output), lim.MaxText)
	switch r.Status {
	case StatusOK, StatusError, StatusStarted:
	default:
		r.Status = StatusUnknown
	}
	if len(r.Attributes) > 0 {
		keys := make([]string, 0, len(r.Attributes))
		for k := range r.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]string{}
		for _, k := range keys {
			if len(out) >= maxAttrs {
				break
			}
			ck := clip(Redact(k), maxAttrKey)
			if ck == "" {
				continue
			}
			// The key names the value ("api_key"), so redact them together.
			v, ok := strings.CutPrefix(Redact(ck+"="+r.Attributes[k]), ck+"=")
			if !ok {
				v = "[redacted]"
			}
			out[ck] = clip(v, maxAttrValue)
		}
		r.Attributes = out
	}
}

// normType maps a reported type onto the known set; anything else is custom.
func normType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if nativeTypes[t] || t == TypeAgent || t == TypeLLM || t == TypeSpan {
		return t
	}
	return TypeCustom
}

// eventType is the common event type of every reported step: agent_step,
// whatever the agent called it (ReportedType keeps that). Metrics that read
// tool calls, tool results or errors from events select them by type, so a
// reported step must never look like one: the agent could otherwise steer a
// metric that rests on trusted or native evidence.
func eventType(string) schemas.EventType {
	return schemas.EventAgentStep
}
