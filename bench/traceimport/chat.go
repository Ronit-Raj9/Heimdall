package traceimport

import (
	"fmt"
	"sort"
	"strings"
)

// parseChat reads plain chat transcripts: one conversation as an object with
// "messages" (and optional "id", "model", "status", "error") or as a bare
// array of messages; several as an array of such objects, a "data" list, or
// one object per line. A file of one message per line is one conversation.
func parseChat(values []any) ([]*Session, error) {
	var out []*Session
	var loose []any
	for _, v := range values {
		switch x := v.(type) {
		case map[string]any:
			switch {
			case asArray(x["messages"]) != nil:
				out = append(out, chatSession(x, len(out)))
			case asArray(x["data"]) != nil:
				more, err := parseChat(asArray(x["data"]))
				if err != nil {
					return nil, err
				}
				out = append(out, more...)
			case asString(x["role"]) != "":
				loose = append(loose, x)
			default:
				return nil, fmt.Errorf("an object with neither messages nor role (keys %s)", keys(x))
			}
		case []any:
			if len(x) > 0 && asMap(x[0]) != nil && asArray(asMap(x[0])["messages"]) != nil {
				for _, c := range x {
					if obj := asMap(c); obj != nil {
						out = append(out, chatSession(obj, len(out)))
					}
				}
				continue
			}
			out = append(out, chatSession(map[string]any{"messages": x}, len(out)))
		default:
			return nil, fmt.Errorf("a top-level %s; want an object or an array", kindOf(v))
		}
	}
	if len(loose) > 0 {
		out = append(out, chatSession(map[string]any{"messages": loose}, len(out)))
	}
	for _, s := range out {
		if len(s.Turns) == 0 && len(s.ToolCalls) == 0 {
			return nil, fmt.Errorf("conversation %s has no message with a role", s.ID)
		}
	}
	return out, nil
}

// chatSession builds one session from a conversation object.
func chatSession(obj map[string]any, n int) *Session {
	id := asString(firstOf(obj, "id", "conversation_id", "session_id", "trace_id", "thread_id"))
	if id == "" {
		id = fmt.Sprintf("conversation-%d", n+1)
	}
	b := newBuilder(id)
	b.addMessages(parseMessages(obj["messages"]))
	b.s.Model = asString(obj["model"])
	b.s.Name = asString(firstOf(obj, "name", "title"))
	b.s.Outcome = chatOutcome(obj)
	return b.s
}

// chatOutcome reads the optional status, outcome and error fields a
// transcript may carry. A transcript without them has an unknown outcome.
func chatOutcome(obj map[string]any) Outcome {
	o := Outcome{Status: OutcomeUnknown}
	if e := textOf(obj["error"]); e != "" && e != "false" {
		o.Status = OutcomeError
		o.Hints = append(o.Hints, "error: "+oneLine(e, 200))
	}
	for _, k := range []string{"status", "outcome"} {
		s := strings.ToLower(asString(obj[k]))
		switch s {
		case "":
			continue
		case "error", "failed", "failure", "fail":
			o.Status = OutcomeError
		case "ok", "success", "succeeded", "completed", "pass", "passed":
			if o.Status != OutcomeError {
				o.Status = OutcomeOK
			}
		}
		o.Hints = append(o.Hints, k+": "+oneLine(s, 80))
	}
	return o
}

// oneLine collapses s to one line of at most n bytes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

func keys(m map[string]any) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = append(out[:8], "...")
	}
	return strings.Join(out, ", ")
}
