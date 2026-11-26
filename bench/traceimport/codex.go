package traceimport

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// FormatCodex is a Codex session file (a rollout): one JSON object per line,
// as kept in ~/.codex/sessions/YYYY/MM/DD/rollout-<time>-<id>.jsonl. Newer
// files wrap each line as {timestamp, type, payload}; files written before
// mid 2025 hold the items bare, after a first {id, timestamp, instructions}
// line. Both are read.
const FormatCodex = "codex"

// codexExit matches a command output that reports a non-zero exit code on
// any of its lines (the rule of metrics/patternevents.go exitStatus).
var codexExit = regexp.MustCompile(`(?im)^\s*exit(?: code)?:?\s+[1-9][0-9]*\b`)

// looksCodex reports whether one decoded line is a Codex rollout line: a
// session_meta line, a wrapped response_item, event_msg or turn_context, or
// the first line of the older layout.
func looksCodex(v map[string]any) bool {
	switch asString(v["type"]) {
	case "session_meta", "response_item", "event_msg", "turn_context":
		return v["payload"] != nil
	}
	_, hasInstr := v["instructions"]
	return hasInstr && asString(v["id"]) != "" && v["type"] == nil && v["messages"] == nil
}

// parseCodex reads one Codex rollout file as one session.
func parseCodex(values []any) ([]*Session, error) {
	b := newBuilder("")
	ws := &Workspace{}
	for _, v := range values {
		obj := asMap(v)
		if obj == nil {
			return nil, fmt.Errorf("a top-level %s; want one object per line", kindOf(v))
		}
		t := parseTime(obj["timestamp"])
		item, kind := obj, asString(obj["type"])
		if p := asMap(obj["payload"]); p != nil {
			item = p
		} else if _, ok := obj["instructions"]; ok && kind == "" {
			if b.s.ID == "" {
				b.s.ID = asString(obj["id"])
			}
			continue
		} else {
			kind = "response_item"
		}
		switch kind {
		case "session_meta":
			if b.s.ID == "" {
				b.s.ID = asString(item["id"])
			}
			if ws.Cwd == "" {
				ws.Cwd = asString(item["cwd"])
			}
			if g := asMap(item["git"]); g != nil {
				ws.Commit, ws.Branch, ws.RepoURL = asString(g["commit_hash"]), asString(g["branch"]), asString(g["repository_url"])
			}
		case "turn_context":
			if m := asString(item["model"]); b.s.Model == "" && m != "" {
				b.s.Model = m
			}
			if ws.Cwd == "" {
				ws.Cwd = asString(item["cwd"])
			}
		case "event_msg":
			switch asString(item["type"]) {
			case "turn_aborted":
				b.s.Outcome.Status = OutcomeError
				b.s.Outcome.Hints = append(b.s.Outcome.Hints, "turn aborted: "+oneLine(asString(item["reason"]), 160))
			case "task_complete":
				if m := asString(item["last_agent_message"]); m != "" {
					b.s.FinalAnswer = b.clip(m)
				}
			}
		case "response_item":
			codexItem(b, item, t)
		}
	}
	if b.s.ID == "" {
		b.s.ID = "codex-session"
	}
	if *ws != (Workspace{}) {
		b.s.Workspace = ws
	}
	return []*Session{b.s}, nil
}

// codexItem adds one response item to the session.
func codexItem(b *builder, item map[string]any, t time.Time) {
	switch asString(item["type"]) {
	case "message":
		var texts []string
		for _, c := range asArray(item["content"]) {
			if s := asString(asMap(c)["text"]); s != "" {
				texts = append(texts, s)
			}
		}
		if s, ok := item["content"].(string); ok {
			texts = append(texts, s)
		}
		text := strings.TrimSpace(strings.Join(texts, "\n"))
		role := normRole(asString(item["role"]))
		if role == "user" {
			for _, p := range []string{"<environment_context>", "<user_instructions>", "# AGENTS.md"} {
				if strings.HasPrefix(text, p) {
					b.skip(codexDropKind(p))
					return
				}
			}
		}
		if text == "" || (role != "user" && role != "assistant" && role != "system") {
			return
		}
		if len(b.s.Turns) >= MaxTurns {
			b.skip("turns")
			return
		}
		b.s.Turns = append(b.s.Turns, Turn{Role: role, Text: b.clip(text), Time: t})
	case "function_call":
		b.requestCall(callPart{id: asString(item["call_id"]), name: asString(item["name"]), args: item["arguments"]}, t)
	case "custom_tool_call":
		b.requestCall(callPart{id: asString(item["call_id"]), name: asString(item["name"]), args: map[string]any{"input": item["input"]}}, t)
	case "function_call_output", "custom_tool_call_output":
		id := asString(item["call_id"])
		text, failed := codexOutput(item["output"])
		i := b.findOpen(id, "")
		if i < 0 {
			if i = b.newCall(id, "", nil, t); i < 0 {
				return
			}
		}
		b.setResult(i, text, "", failed)
	case "reasoning":
		b.skip("reasoning")
	}
}

// codexOutput renders a call output and reports whether it failed: an
// object with "success": false, text starting "Error:", or a non-zero exit
// code on any line.
func codexOutput(v any) (string, bool) {
	if obj := asMap(v); obj != nil {
		text := textOf(firstOf(obj, "output", "content"))
		if text == "" {
			text = compactJSON(obj)
		}
		if ok, has := obj["success"].(bool); has && !ok {
			return text, true
		}
		return text, codexFailedText(text)
	}
	text := textOf(v)
	return text, codexFailedText(text)
}

func codexFailedText(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "Error:") || codexExit.MatchString(s)
}

func codexDropKind(prefix string) string {
	switch prefix {
	case "<environment_context>":
		return "environment context"
	case "<user_instructions>":
		return "user instructions"
	}
	return "agents file"
}
