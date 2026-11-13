package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ParserCodex is the name of the Codex CLI `exec --json` parser.
const ParserCodex = "codex"

const codexVersion = "1.0.0"

// Codex item types (codex-rs/exec/src/exec_events.rs).
const (
	codexAgentMessage = "agent_message"
	codexReasoning    = "reasoning"
	codexCommand      = "command_execution"
	codexFileChange   = "file_change"
	codexMCPCall      = "mcp_tool_call"
	codexWebSearch    = "web_search"
	codexItemError    = "error"
)

var (
	// codexReconnect matches the stream-retry notice Codex prints as a
	// top-level error line, e.g. "Reconnecting... 2/5 (unexpected status 503 ...)".
	codexReconnect = regexp.MustCompile(`^Reconnecting\.\.\. (\d+)/(\d+)(?: \((.*)\))?$`)
	// codexShellWrap matches the shell Codex reports around every command:
	// `/bin/sh -c <quoted>`, or `-lc` when login shells are allowed.
	codexShellWrap = regexp.MustCompile(`(?s)^(?:/usr)?(?:/bin/)?(?:ba|da|z)?sh -l?c (.+)$`)
)

type codexParser struct{}

func newCodexParser() *codexParser {
	return &codexParser{}
}

// GetName implements schemas.Part.
func (p *codexParser) GetName() string { return ParserCodex }

// GetVersion implements schemas.Part.
func (p *codexParser) GetVersion() string { return codexVersion }

// Parse reads `codex exec --json` output: one ThreadEvent per line
// (thread.started, turn.started, item.started, item.updated, item.completed,
// turn.completed, turn.failed, error). Items carry an id unique within the
// run, so a tool call's started and completed lines pair by item id.
//
// The stream has no model request boundaries: every item of a Codex turn
// arrives between one turn.started and one turn.completed, and usage is the
// turn's total. Model turns are inferred: a new one begins with the first
// item after the previous turn's tool calls have all completed, because Codex
// sends its next request only then. Parallel calls that Codex runs one after
// another therefore count as separate turns, and a tool call Codex rejects
// before running it (an apply_patch that fails verification) leaves no line
// at all. model_response events carry no tokens; the gateway rows supply
// steps and tokens. A retried request appears only when Codex's stream retry
// fires ("Reconnecting... n/m"); its HTTP-level retries print nothing.
// Lines that are not JSON or not understood are skipped.
func (p *codexParser) Parse(ctx context.Context, rc *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	st := newCodexState(rc)
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		st.handle(obj)
	}
	return &schemas.ParseResult{Events: st.events, Claim: st.claim()}, nil
}

// codexState reuses the Claude Code stream state for event identity,
// sequence numbers and the claim, and adds Codex's item bookkeeping.
type codexState struct {
	*streamState
	turnEv      int            // index of the current model_response event, -1 before the first
	fromThought bool           // the current turn's summary came from a reasoning item
	open        int            // tool items started and not completed
	toolDone    bool           // a tool item completed since the current turn began
	items       map[string]int // item id -> index of its tool_call event
}

// codexItem is the union of the item fields the parser reads.
type codexItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Message          string          `json:"message"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Changes          []codexChange   `json:"changes"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           json.RawMessage `json:"result"`
	Error            json.RawMessage `json:"error"`
	Query            string          `json:"query"`
}

type codexChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

func newCodexState(rc *schemas.RunContext) *codexState {
	return &codexState{streamState: newStreamState(rc), turnEv: -1, items: map[string]int{}}
}

func (st *codexState) handle(obj map[string]json.RawMessage) {
	typ, _ := rawString(obj["type"])
	switch typ {
	case "thread.started", "turn.started", "turn.completed":
		st.recognized = true
	case "item.started", "item.updated", "item.completed":
		var it codexItem
		if json.Unmarshal(obj["item"], &it) != nil || it.ID == "" {
			return
		}
		st.recognized = true
		st.item(typ, it)
	case "turn.failed":
		st.recognized = true
		i := st.newEvent(schemas.EventError, st.turn)
		st.events[i].IsError = true
		st.events[i].Summary = clip(oneLine("turn failed: "+rawErrorText(obj["error"])), summaryMax)
	case "error":
		st.recognized = true
		msg, _ := rawString(obj["message"])
		st.topError(msg)
	}
}

// item routes one item line. item.updated carries partial output of an item
// already recorded and adds nothing.
func (st *codexState) item(phase string, it codexItem) {
	switch it.Type {
	case codexAgentMessage, codexReasoning:
		if phase == "item.completed" {
			st.message(it)
		}
	case codexCommand, codexFileChange, codexMCPCall, codexWebSearch:
		if phase == "item.updated" {
			return
		}
		if _, seen := st.items[it.ID]; !seen {
			st.toolCall(it)
		}
		if phase == "item.completed" {
			st.toolResult(it)
		}
	case codexItemError:
		if phase == "item.completed" {
			i := st.newEvent(schemas.EventError, st.turn)
			st.events[i].IsError = true
			st.events[i].Summary = clip(oneLine("error: "+it.Message), summaryMax)
		}
	}
}

// modelTurn returns the model_response event an item belongs to, starting a
// new one when the previous turn's tool calls have all completed.
func (st *codexState) modelTurn() int {
	if st.turnEv < 0 || (st.toolDone && st.open == 0) {
		st.turn++
		st.turnEv = st.newEvent(schemas.EventModelResponse, st.turn)
		st.toolDone = false
		st.fromThought = false
	}
	return st.turnEv
}

// message records an agent message or reasoning summary. The latest agent
// message is the run's final text, from which the claim is read.
func (st *codexState) message(it codexItem) {
	ev := &st.events[st.modelTurn()]
	text := oneLine(it.Text)
	if it.Type == codexReasoning {
		if ev.Summary == "" && text != "" {
			ev.Summary = clip("reasoning: "+text, summaryMax)
			st.fromThought = true
		}
		return
	}
	if (ev.Summary == "" || st.fromThought) && text != "" {
		ev.Summary = clip(text, summaryMax)
		st.fromThought = false
	}
	st.lastText.Reset()
	st.lastText.WriteString(it.Text)
}

func (st *codexState) toolCall(it codexItem) {
	parent := st.events[st.modelTurn()]
	name, target, args := codexToolArgs(it)
	i := st.newEvent(schemas.EventToolCall, parent.TurnIndex)
	ev := &st.events[i]
	ev.ToolCallID = it.ID
	ev.ToolName = name
	ev.ParentEventID = parent.EventID
	ev.Target = clip(target, targetMax)
	ev.ArgsHash = hashValue(args)
	ev.Summary = clip(oneLine(strings.TrimSpace(name+" "+target)), summaryMax)
	st.items[it.ID] = i
	st.open++
}

func (st *codexState) toolResult(it codexItem) {
	call := st.events[st.items[it.ID]]
	if st.open > 0 {
		st.open--
	}
	st.toolDone = true
	i := st.newEvent(schemas.EventToolResult, call.TurnIndex)
	ev := &st.events[i]
	ev.ToolCallID = it.ID
	ev.ParentEventID = call.EventID
	ev.ToolName = call.ToolName
	ev.Target = call.Target
	ev.IsError, ev.ResultHash, ev.Summary = codexOutcome(it)
}

// topError turns a top-level error line into a retry (Codex's stream retry
// notice) or an error event (the request's final failure, or any other
// error Codex reports).
func (st *codexState) topError(msg string) {
	if m := codexReconnect.FindStringSubmatch(msg); m != nil {
		i := st.newEvent(schemas.EventRetry, st.turn)
		ev := &st.events[i]
		ev.IsError = true
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			ev.AttemptNo = &n
		}
		ev.Summary = clip(oneLine(fmt.Sprintf("reconnecting %s/%s: %s", m[1], m[2], m[3])), summaryMax)
		return
	}
	i := st.newEvent(schemas.EventError, st.turn)
	st.events[i].IsError = true
	st.events[i].Summary = clip(oneLine("error: "+msg), summaryMax)
}

// codexToolArgs names a tool item, the target it acts on and the arguments
// that are hashed. Codex reports the executed command, not the model's raw
// arguments, so the hash covers what the stream shows.
func codexToolArgs(it codexItem) (name, target string, args any) {
	switch it.Type {
	case codexCommand:
		return "exec_command", codexCommandTarget(it.Command), map[string]any{"command": it.Command}
	case codexFileChange:
		paths := make([]string, 0, len(it.Changes))
		changes := make([]any, 0, len(it.Changes))
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
			changes = append(changes, map[string]any{"path": c.Path, "kind": c.Kind})
		}
		return "apply_patch", strings.Join(paths, ", "), map[string]any{"changes": changes}
	case codexMCPCall:
		var a any
		if len(it.Arguments) > 0 && json.Unmarshal(it.Arguments, &a) != nil {
			a = string(it.Arguments)
		}
		target = toolTarget(it.Arguments)
		if target == "" {
			target = it.Tool
		}
		return it.Server + "." + it.Tool, target, map[string]any{"arguments": a}
	default: // web_search
		return "web_search", strings.TrimSpace(it.Query), map[string]any{"query": it.Query}
	}
}

// codexOutcome reads a completed tool item: whether it failed, the hash of
// what it returned and a one-line summary.
func codexOutcome(it codexItem) (isErr bool, hash, summary string) {
	failed := it.Status == "failed" || it.Status == "declined"
	switch it.Type {
	case codexCommand:
		isErr = failed || (it.ExitCode != nil && *it.ExitCode != 0)
		code := "?"
		if it.ExitCode != nil {
			code = strconv.Itoa(*it.ExitCode)
		}
		return isErr, hashValue(map[string]any{"exit_code": it.ExitCode, "output": it.AggregatedOutput}),
			clip(oneLine("exit "+code+": "+lastLine(it.AggregatedOutput)), summaryMax)
	case codexMCPCall:
		isErr = failed || (len(it.Error) > 0 && string(it.Error) != "null")
		return isErr, hashRaw(it.Result), clip(oneLine(it.Status+" "+rawErrorText(it.Error)), summaryMax)
	case codexFileChange:
		parts := make([]string, 0, len(it.Changes))
		for _, c := range it.Changes {
			parts = append(parts, c.Kind+" "+c.Path)
		}
		return failed, hashValue(map[string]any{"status": it.Status, "changes": parts}),
			clip(oneLine(it.Status+": "+strings.Join(parts, ", ")), summaryMax)
	default:
		return failed, hashValue(map[string]any{"status": it.Status, "query": it.Query}), clip(oneLine(it.Status), summaryMax)
	}
}

// codexCommandTarget strips the shell wrapper Codex reports around a command
// (`/bin/sh -c 'pytest -q'` becomes `pytest -q`), so the target is what the
// model asked to run. A wrapped argument that is not exactly one quoted word
// is kept whole.
func codexCommandTarget(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	m := codexShellWrap.FindStringSubmatch(cmd)
	if m == nil {
		return cmd
	}
	if inner, ok := unquoteShellWord(m[1]); ok && strings.TrimSpace(inner) != "" {
		return strings.TrimSpace(inner)
	}
	return cmd
}

// unquoteShellWord undoes the POSIX quoting of a single word: single quotes,
// double quotes with backslash escapes, or a bare word.
func unquoteShellWord(s string) (string, bool) {
	n := len(s)
	switch {
	case n >= 2 && s[0] == '\'' && s[n-1] == '\'':
		inner := s[1 : n-1]
		return inner, !strings.Contains(inner, "'")
	case n >= 2 && s[0] == '"' && s[n-1] == '"':
		var b strings.Builder
		inner := s[1 : n-1]
		for i := 0; i < len(inner); i++ {
			c := inner[i]
			switch {
			case c == '"':
				return "", false
			case c == '\\' && i+1 < len(inner) && strings.IndexByte("\"\\$`\n", inner[i+1]) >= 0:
				i++
				b.WriteByte(inner[i])
			default:
				b.WriteByte(c)
			}
		}
		return b.String(), true
	case !strings.ContainsAny(s, " \t\n'\"\\"):
		return s, true
	}
	return "", false
}

// hashValue hashes a value's canonical JSON, falling back to its formatted
// text if it cannot be encoded.
func hashValue(v any) string {
	h, err := utils.HashJSON(v)
	if err != nil {
		return utils.SHA256Bytes([]byte(fmt.Sprint(v)))
	}
	return h
}

// lastLine returns the last non-empty line: a command's verdict (pytest's
// "1 failed in 0.01s") is at its end.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
