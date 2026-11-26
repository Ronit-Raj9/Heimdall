package traceimport

import (
	"fmt"
	"regexp"
	"strings"
)

// FormatClaudeCode is a Claude Code session file: one JSON object per line,
// as kept in ~/.claude/projects/<project>/<session id>.jsonl.
const FormatClaudeCode = "claude_code"

// claudeCodeLineTypes are the line types that mark a Claude Code session.
var claudeCodeLineTypes = map[string]bool{"user": true, "assistant": true, "summary": true, "system": true, "attachment": true}

// looksClaudeCode reports whether one decoded line is a Claude Code session
// line: it has "sessionId", "uuid" and a conversation line type.
func looksClaudeCode(v map[string]any) bool {
	return asString(v["sessionId"]) != "" && asString(v["uuid"]) != "" && claudeCodeLineTypes[asString(v["type"])]
}

// claudeWrappers are the wrappers Claude Code puts around text it adds to a
// user message itself (reminders, slash command echoes).
var claudeWrappers = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>|<command-[a-z-]+>.*?</command-[a-z-]+>|<local-command-[a-z-]+>.*?</local-command-[a-z-]+>`)

// parseClaudeCode reads Claude Code session lines, one session per
// sessionId. Subagent lines, meta lines and bookkeeping lines are skipped;
// thinking and images are dropped and counted.
func parseClaudeCode(values []any) ([]*Session, error) {
	var out []*Session
	byID := map[string]*claudeState{}
	for _, v := range values {
		obj := asMap(v)
		if obj == nil {
			return nil, fmt.Errorf("a top-level %s; want one object per line", kindOf(v))
		}
		typ := asString(obj["type"])
		id := asString(obj["sessionId"])
		if id == "" || (typ != "user" && typ != "assistant") {
			continue
		}
		st := byID[id]
		if st == nil {
			st = &claudeState{b: newBuilder(id)}
			byID[id] = st
			out = append(out, st.b.s)
		}
		if asBool(obj["isSidechain"]) {
			st.b.skip("subagent line")
			continue
		}
		if asBool(obj["isMeta"]) {
			continue
		}
		st.line(obj, typ)
	}
	for _, s := range out {
		st := byID[s.ID]
		if st.lastAPIError {
			s.Outcome.Status = OutcomeError
		} else {
			s.Outcome.Status = OutcomeUnknown
		}
		for _, c := range s.ToolCalls {
			if c.IsError {
				s.Outcome.Hints = append(s.Outcome.Hints, "tool "+c.Name+" failed: "+oneLine(firstNonEmpty(c.Error, c.Result), 160))
			}
		}
	}
	return out, nil
}

type claudeState struct {
	b            *builder
	sawUser      bool
	lastAPIError bool
}

func (st *claudeState) line(obj map[string]any, typ string) {
	b := st.b
	msg := asMap(obj["message"])
	t := parseTime(obj["timestamp"])
	if typ == "user" && !st.sawUser {
		st.sawUser = true
		cwd, branch := asString(obj["cwd"]), asString(obj["gitBranch"])
		if cwd != "" || branch != "" {
			b.s.Workspace = &Workspace{Cwd: cwd, Branch: branch}
		}
	}
	if typ == "assistant" {
		st.lastAPIError = asBool(obj["isApiErrorMessage"])
		if m := asString(msg["model"]); b.s.Model == "" && m != "" && m != "<synthetic>" {
			b.s.Model = m
		}
	}
	var blocks []any
	switch c := msg["content"].(type) {
	case string:
		blocks = []any{map[string]any{"type": "text", "text": c}}
	case []any:
		blocks = c
	}
	var texts []string
	for _, bl := range blocks {
		p := asMap(bl)
		switch asString(p["type"]) {
		case "text":
			texts = append(texts, asString(p["text"]))
		case "tool_use":
			b.requestCall(callPart{id: asString(p["id"]), name: asString(p["name"]), args: p["input"]}, t)
		case "tool_result":
			text := claudeResultText(p["content"])
			isErr := asBool(p["is_error"]) || strings.HasPrefix(strings.TrimSpace(text), "Error:")
			i := b.findOpen(asString(p["tool_use_id"]), "")
			if i < 0 {
				if i = b.newCall(asString(p["tool_use_id"]), "", nil, t); i < 0 {
					continue
				}
			}
			b.setResult(i, text, "", isErr)
		case "thinking", "redacted_thinking":
			b.skip("thinking")
		case "image":
			b.skip("image")
		default:
			b.skip(asString(p["type"]))
		}
	}
	text := strings.Join(texts, "\n")
	role := "assistant"
	if typ == "user" {
		role = "user"
		text = claudeWrappers.ReplaceAllString(text, "")
	}
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	if len(b.s.Turns) >= MaxTurns {
		b.skip("turns")
		return
	}
	b.s.Turns = append(b.s.Turns, Turn{Role: role, Text: b.clip(text), Time: t})
}

// claudeResultText reads a tool_result content: a string, or a list of text
// blocks (other blocks are left out).
func claudeResultText(v any) string {
	list, ok := v.([]any)
	if !ok {
		text, _ := resultText(v)
		return text
	}
	var parts []string
	for _, item := range list {
		if s := asString(asMap(item)["text"]); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}
