package metrics

import (
	"cmp"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Shared by the three failure-pattern detectors (pattern_loop,
// pattern_unverified_finish, pattern_ignored_error). They read only the
// normalized events and the claim, and report an observed pattern with the
// event ids that show it. A pattern is an observation, never a root cause
// (docs/DESIGN.md 2 and 11, docs/triage.md).

// Reasons and limits of the pattern detectors.
const (
	reasonNoEvents       = "no gateway or agent_stream events"
	reasonNoStreamEvents = "no agent_stream events: gateway rows do not carry tool outcomes"
	reasonNoClaimMade    = "no success claim"
	// gatewayIncomplete is the summary prefix of the event the gateway source
	// writes when Bifrost did not return every row of the run.
	gatewayIncomplete = "gateway request log incomplete"
	// maxReasonEvents caps the event ids a pattern reason lists.
	maxReasonEvents = 10
	// maxReasonText caps a target or summary quoted in a reason.
	maxReasonText = 60
)

// toolTrace is the tool activity of one run as one source saw it: tool
// calls and tool results in sequence order.
type toolTrace struct {
	level      schemas.EvidenceLevel
	events     []schemas.Event // tool_call and tool_result events, in seq order
	calls      []schemas.Event // the tool_call events of events
	byID       map[string]schemas.Event
	callByTool map[string]schemas.Event // tool call id -> tool_call event
	finish     string                   // id of the source's last event
	incomplete bool                     // the gateway marked its log incomplete
}

// pickTrace chooses the source the loop and unverified-finish detectors read.
// The gateway is the witness of record and wins when its log is complete and
// it saw tool calls. The agent's own stream wins when the gateway is missing,
// incomplete, or logged no tool calls while the stream has some (content
// logging off). An incomplete gateway is still returned when it is all there
// is, so a pattern it does show still counts.
func pickTrace(events []schemas.Event) (toolTrace, bool) {
	gw, gwOK := traceOf(events, schemas.SourceGateway)
	as, asOK := traceOf(events, schemas.SourceAgentStream)
	switch {
	case gwOK && !gw.incomplete && (len(gw.calls) > 0 || !asOK || len(as.calls) == 0):
		return gw, true
	case asOK:
		return as, true
	case gwOK:
		return gw, true
	default:
		return toolTrace{}, false
	}
}

// traceOf collects the tool events of one source. It reports false when the
// source wrote no event at all, which is what makes a pattern unknown rather
// than absent.
func traceOf(events []schemas.Event, src schemas.EventSource) (toolTrace, bool) {
	var all []schemas.Event
	for _, e := range events {
		if e.Source == src {
			all = append(all, e)
		}
	}
	if len(all) == 0 {
		return toolTrace{}, false
	}
	slices.SortStableFunc(all, func(x, y schemas.Event) int { return cmp.Compare(x.Seq, y.Seq) })
	tr := toolTrace{
		level:      schemas.EvidenceGateway,
		byID:       map[string]schemas.Event{},
		callByTool: map[string]schemas.Event{},
		finish:     all[len(all)-1].EventID,
	}
	if src == schemas.SourceAgentStream {
		tr.level = schemas.EvidenceNative
	}
	for _, e := range all {
		switch e.Type {
		case schemas.EventToolCall:
			tr.calls = append(tr.calls, e)
			if e.ToolCallID != "" {
				tr.callByTool[e.ToolCallID] = e
			}
		case schemas.EventToolResult:
		case schemas.EventError:
			if e.IsError && strings.HasPrefix(e.Summary, gatewayIncomplete) {
				tr.incomplete = true
			}
			continue
		default:
			continue
		}
		tr.events = append(tr.events, e)
		if e.EventID != "" {
			tr.byID[e.EventID] = e
		}
	}
	return tr, true
}

// callOf returns the tool_call a tool_result answers, by parent event id or
// tool call id.
func (tr toolTrace) callOf(result schemas.Event) (schemas.Event, bool) {
	if c, ok := tr.byID[result.ParentEventID]; ok && c.Type == schemas.EventToolCall {
		return c, true
	}
	c, ok := tr.callByTool[result.ToolCallID]
	return c, ok
}

// patternValue builds the value of a pattern detector. An observed pattern
// lists its event ids first, in the form the triage screen parses:
// "events: id1,id2; summary".
func patternValue(seen bool, level schemas.EvidenceLevel, ids []string, summary string) schemas.Value {
	if !seen {
		v := schemas.Observed(0, level)
		v.Reason = summary
		return v
	}
	v := schemas.Observed(1, level)
	if len(ids) > maxReasonEvents {
		ids = ids[:maxReasonEvents]
	}
	v.Reason = fmt.Sprintf("events: %s; %s", strings.Join(ids, ","), summary)
	return v
}

// isCommand reports whether a tool runs a shell command, by name: Claude
// Code's Bash, Codex's exec_command and shell tools, and any tool whose name
// says shell, command or terminal.
func isCommand(e schemas.Event) bool {
	n := strings.ToLower(e.ToolName)
	return n == "bash" || n == "exec" || strings.Contains(n, "shell") || strings.Contains(n, "command") || strings.Contains(n, "terminal")
}

// editWords mark a tool that changes workspace files. Todo tools write a
// list, not a file.
var editWords = []string{"edit", "write", "patch", "replace", "create_file", "insert"}

// isEdit reports whether a tool call is a workspace edit, by tool name. A
// shell command that edits (sed -i, a redirect) is not seen as one.
func isEdit(e schemas.Event) bool {
	n := strings.ToLower(e.ToolName)
	if n == "" || strings.Contains(n, "todo") || isCommand(e) {
		return false
	}
	for _, w := range editWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// readWords mark a tool that reads files rather than changing them.
var readWords = []string{"read", "view", "open", "grep", "glob", "search", "list", "cat"}

// readCommands are the shell programs that read a file named in their
// arguments.
var readCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true, "sed": true, "awk": true,
	"grep": true, "rg": true, "nl": true, "wc": true, "ls": true, "find": true,
}

// isRead reports whether a tool call reads files: a read-like tool, or a
// shell command whose program is a reader.
func isRead(e schemas.Event) bool {
	if isCommand(e) {
		fields := strings.Fields(e.Target)
		return len(fields) > 0 && readCommands[path.Base(fields[0])]
	}
	n := strings.ToLower(e.ToolName)
	for _, w := range readWords {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// testCommands are the command fragments that count as running tests.
var testCommands = []string{"pytest", "py.test", "python -m pytest", "unittest", "npm test", "go test", "make test"}

// runsTests reports whether a tool call runs tests: a shell command whose
// text contains a test command, or a non-shell tool named for tests.
func runsTests(e schemas.Event) bool {
	if !isCommand(e) {
		return strings.Contains(strings.ToLower(e.ToolName), "test")
	}
	t := strings.ToLower(e.Target)
	for _, c := range testCommands {
		if strings.Contains(t, c) {
			return true
		}
	}
	return false
}

// callKey identifies "the same tool call": the command text for shell tools
// (other arguments such as a timeout do not make it a different call), else
// the tool name and the hash of its arguments, else the name and target.
func callKey(e schemas.Event) string {
	if isCommand(e) && strings.TrimSpace(e.Target) != "" {
		return "cmd\x00" + strings.Join(strings.Fields(e.Target), " ")
	}
	if e.ArgsHash != "" {
		return e.ToolName + "\x00" + e.ArgsHash
	}
	return e.ToolName + "\x00t:" + e.Target
}

// describeCall names a tool call in a reason: the tool and a short target.
func describeCall(e schemas.Event) string {
	name := e.ToolName
	if name == "" {
		name = "tool"
	}
	if t := strings.TrimSpace(e.Target); t != "" {
		return fmt.Sprintf("%s %q", name, clipText(oneLineText(t), maxReasonText))
	}
	return name
}

// exitStatus matches a command result summary that reports a non-zero exit,
// as Codex ("exit 1: ...") and Claude Code ("Exit code 2 ...") write it.
var exitStatus = regexp.MustCompile(`(?i)^\s*exit(?: code)?:?\s+[1-9][0-9]*\b`)

// failedResult reports whether a tool result is an error: flagged by the
// parser, or a command result whose summary reports a non-zero exit.
func failedResult(e schemas.Event) bool {
	return e.IsError || exitStatus.MatchString(e.Summary)
}

// pathTokens returns the file paths a target names: the whole target when it
// is one word, else each word that looks like a path. A pytest node id keeps
// only its file ("tests/test_x.py::test_y" names tests/test_x.py).
func pathTokens(target string) []string {
	fields := strings.Fields(target)
	var out []string
	for _, f := range fields {
		f = strings.Trim(f, `"'`)
		f, _, _ = strings.Cut(f, "::")
		if f == "" || strings.HasPrefix(f, "-") || strings.Contains(f, "=") {
			continue
		}
		if len(fields) == 1 || strings.Contains(f, "/") || path.Ext(f) != "" {
			out = append(out, f)
		}
	}
	return out
}

// readsFileOf reports whether a read call reads a file the target names.
func readsFileOf(read schemas.Event, target string) bool {
	rt := strings.TrimSpace(read.Target)
	if rt == "" {
		return false
	}
	for _, p := range pathTokens(target) {
		if isCommand(read) {
			if strings.Contains(rt, p) || strings.Contains(rt, path.Base(p)) {
				return true
			}
			continue
		}
		if p == rt || strings.HasSuffix(p, rt) || strings.HasSuffix(rt, p) || path.Base(p) == path.Base(rt) {
			return true
		}
	}
	return false
}

// oneLineText collapses whitespace so a quoted target stays on one line.
func oneLineText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipText shortens s to n runes with an ellipsis.
func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
