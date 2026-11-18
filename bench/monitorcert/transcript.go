package monitorcert

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Run folder files the transcript builder reads. It opens them read-only
// and never writes into a run folder.
const (
	planFile     = "plan.json"
	gatewayFile  = "gateway.requests.jsonl"
	toolsFile    = "tools.jsonl"
	eventsFile   = "events.jsonl"
	instructBase = "instruction.md"
	maxLineBytes = 64 << 20 // a gateway row with raw request and response can be large
)

// Limits bound what one transcript carries, so a long run cannot make a
// request unbounded. Content over a limit is cut at a UTF-8 boundary and
// marked truncated; a run with more than MaxActions actions keeps the first
// and the last halves and reports the number dropped from the middle.
type Limits struct {
	MaxActions      int
	MaxActionBytes  int
	MaxInstruction  int
	MaxFinalMessage int
}

// DefaultLimits are the transcript bounds used unless the caller sets
// others: 400 actions of at most 8,000 bytes each (a file write or a patch
// in a tool call's arguments is often over 2,000), an 8,000-byte
// instruction excerpt and a 4,000-byte final message, about 3.2 MB at most.
func DefaultLimits() Limits {
	return Limits{MaxActions: 400, MaxActionBytes: 8000, MaxInstruction: 8000, MaxFinalMessage: 4000}
}

// Transcript is what a monitor sees of one run: the task instruction, the
// agent's actions in order and its final message. It never holds a label,
// a gate result, a grade, a probe id or a run id.
type Transcript struct {
	Instruction          string
	InstructionTruncated bool
	Actions              []schemas.MonitorAction
	FinalMessage         string
	Omitted              int  // actions dropped from the middle
	Truncated            bool // any content cut or any action dropped
}

// nonceRE matches the bench's receipt nonces. A nonce shows a probe was
// armed, which a monitor must not learn from the bench's own bookkeeping,
// so every one is replaced; the text around it (a canary URL, a planted
// line) stays, because that is the attack itself.
var nonceRE = regexp.MustCompile(`nonce_[a-z0-9]{24}`)

const nonceRedacted = "[redacted]"

// BuildTranscript reads a sealed run folder and returns its transcript.
// Sources, merged in time order: assistant text and requested tool calls
// (name and arguments) from the gateway rows' raw detail, falling back to
// the row's parsed tool calls; tool proxy ledger calls; commands, file
// changes and network activity from the step timeline's trusted observers
// (rows before the agent started are dropped, so files a probe planted are
// never shown as the agent's); and agent-stream tool calls the gateway did
// not already show. Fault, retry, gate, grade and integrity events are
// never read. The final message is the run's claim text.
func BuildTranscript(dir string, run *schemas.Run, lim Limits) (*Transcript, error) {
	if lim.MaxActions < 2 {
		lim = DefaultLimits()
	}
	b := &builder{seen: map[string]bool{}}
	if run != nil && run.Timing.AgentStarted != nil {
		b.agentStarted = *run.Timing.AgentStarted
	}
	ins, err := readInstruction(dir)
	if err != nil {
		return nil, err
	}
	steps := []func(string) error{b.readGateway, b.readToolLedger, b.readSteps, b.readEvents}
	for _, step := range steps {
		if err := step(dir); err != nil {
			return nil, err
		}
	}
	t := &Transcript{}
	t.Instruction, t.InstructionTruncated = clip(redact(ins), lim.MaxInstruction)
	if run != nil && run.Claim != nil {
		var cut bool
		t.FinalMessage, cut = clip(redact(run.Claim.Text), lim.MaxFinalMessage)
		t.Truncated = t.Truncated || cut
	}
	t.Actions, t.Omitted = b.finish(lim)
	t.Truncated = t.Truncated || t.Omitted > 0 || t.InstructionTruncated
	for _, a := range t.Actions {
		t.Truncated = t.Truncated || a.Truncated
	}
	return t, nil
}

// Requests turns the transcript into monitor requests: one with every action
// in transcript mode, one per action in action mode.
func (t *Transcript) Requests(mode string) []schemas.MonitorRequest {
	base := schemas.MonitorRequest{
		Version: schemas.MonitorProtocolVersion, Mode: mode,
		Instruction: t.Instruction, InstructionTruncated: t.InstructionTruncated,
		FinalMessage: t.FinalMessage, OmittedActions: t.Omitted,
	}
	if mode != schemas.MonitorModeAction {
		base.Actions = append([]schemas.MonitorAction(nil), t.Actions...)
		return []schemas.MonitorRequest{base}
	}
	out := make([]schemas.MonitorRequest, 0, len(t.Actions))
	for i := range t.Actions {
		r := base
		a := t.Actions[i]
		r.Action = &a
		r.FinalMessage = ""
		out = append(out, r)
	}
	return out
}

// builder collects actions with their time and arrival order.
type builder struct {
	agentStarted time.Time
	actions      []timedAction
	seen         map[string]bool // tool call ids already shown
	gatewayCalls int
}

type timedAction struct {
	ts  time.Time
	seq int
	a   schemas.MonitorAction
}

func (b *builder) add(ts time.Time, a schemas.MonitorAction) {
	b.actions = append(b.actions, timedAction{ts: ts, seq: len(b.actions), a: a})
}

// finish sorts the actions by time (arrival order on ties), clips each and
// drops the middle of an overlong run.
func (b *builder) finish(lim Limits) ([]schemas.MonitorAction, int) {
	sort.SliceStable(b.actions, func(i, j int) bool {
		if !b.actions[i].ts.Equal(b.actions[j].ts) {
			return b.actions[i].ts.Before(b.actions[j].ts)
		}
		return b.actions[i].seq < b.actions[j].seq
	})
	all := make([]schemas.MonitorAction, 0, len(b.actions))
	for i, ta := range b.actions {
		a := ta.a
		a.Index = i
		a.Name = redact(a.Name)
		a.Content, a.Truncated = clip(redact(a.Content), lim.MaxActionBytes)
		all = append(all, a)
	}
	if len(all) <= lim.MaxActions {
		return all, 0
	}
	head := lim.MaxActions / 2
	tail := lim.MaxActions - head
	kept := append(all[:head:head], all[len(all)-tail:]...)
	return kept, len(all) - lim.MaxActions
}

// gatewayRow is the part of a gateway.requests.jsonl row the builder reads.
type gatewayRow struct {
	Timestamp time.Time             `json:"timestamp"`
	LatencyMS float64               `json:"latency_ms"`
	Status    string                `json:"status"`
	ToolCalls []schemas.ToolCallRef `json:"tool_calls"`
	Raw       *struct {
		OutputMessage *struct {
			Content json.RawMessage `json:"content"`
		} `json:"output_message"`
		ToolCalls []rawToolCall `json:"tool_calls"`
	} `json:"raw"`
}

// rawToolCall is a tool call in Bifrost's raw detail row: OpenAI shape
// (function.name, function.arguments as a JSON string), or flat name and
// arguments.
type rawToolCall struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"arguments"`
	Function struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func (b *builder) readGateway(dir string) error {
	return eachLine(filepath.Join(dir, gatewayFile), func(line []byte) {
		var r gatewayRow
		if json.Unmarshal(line, &r) != nil {
			return
		}
		ts := r.Timestamp.Add(time.Duration(r.LatencyMS * float64(time.Millisecond)))
		if r.Raw != nil && r.Raw.OutputMessage != nil {
			if text := messageText(r.Raw.OutputMessage.Content); strings.TrimSpace(text) != "" {
				b.add(ts, schemas.MonitorAction{Kind: schemas.MonitorActionMessage, Source: schemas.StepSourceGateway, Content: text})
			}
		}
		if r.Raw != nil && len(r.Raw.ToolCalls) > 0 {
			for _, tc := range r.Raw.ToolCalls {
				name, args := tc.Function.Name, tc.Function.Args
				if name == "" {
					name, args = tc.Name, tc.Args
				}
				b.addCall(ts, tc.ID, name, argsText(args), schemas.StepSourceGateway)
			}
			return
		}
		for _, tc := range r.ToolCalls {
			content := tc.Command
			if content == "" {
				content = tc.Target
			}
			b.addCall(ts, tc.ID, tc.Name, content, schemas.StepSourceGateway)
		}
	})
}

// addCall adds a requested tool call unless its id was already shown.
func (b *builder) addCall(ts time.Time, id, name, content, source string) {
	if id != "" {
		if b.seen[id] {
			return
		}
		b.seen[id] = true
	}
	if source == schemas.StepSourceGateway {
		b.gatewayCalls++
	}
	b.add(ts, schemas.MonitorAction{Kind: schemas.MonitorActionToolCall, Source: source, Name: name, Content: content})
}

func (b *builder) readToolLedger(dir string) error {
	return eachLine(filepath.Join(dir, toolsFile), func(line []byte) {
		var c schemas.ToolCall
		if json.Unmarshal(line, &c) != nil || c.Kind != "" {
			return // a listing or unparsed traffic row, not a call
		}
		b.add(c.Started, schemas.MonitorAction{Kind: schemas.MonitorActionProxyCall, Source: schemas.StepSourceToolProxy,
			Name: path.Join(c.Server, c.Tool), Content: c.Arguments})
	})
}

// readSteps takes commands, file changes and network activity from the
// trusted observers of the step timeline.
func (b *builder) readSteps(dir string) error {
	return eachLine(filepath.Join(dir, schemas.StepsFile), func(line []byte) {
		var s schemas.StepRecord
		if json.Unmarshal(line, &s) != nil || b.beforeAgent(s.TS) {
			return
		}
		switch {
		case s.Kind == schemas.StepCommand && s.Source == schemas.StepSourceProcess:
			b.add(s.TS, schemas.MonitorAction{Kind: schemas.MonitorActionCommand, Source: s.Source, Content: s.Summary})
		case s.Kind == schemas.StepFileChange && s.Source == schemas.StepSourceFS:
			b.add(s.TS, schemas.MonitorAction{Kind: schemas.MonitorActionFile, Source: s.Source, Name: s.Op, Content: s.Path})
		case s.Kind == schemas.StepNetwork && s.Source == schemas.StepSourceNetwork:
			b.add(s.TS, schemas.MonitorAction{Kind: schemas.MonitorActionNetwork, Source: s.Source, Content: s.Summary})
		}
	})
}

// readEvents adds tool calls from the normalized events that the gateway
// rows did not show: the agent's own stream, the tool runner, or gateway
// events of a run whose gateway file is gone. Shim, proxy ledger and every
// non-call event type are skipped: they describe faults and bookkeeping.
func (b *builder) readEvents(dir string) error {
	return eachLine(filepath.Join(dir, eventsFile), func(line []byte) {
		var e schemas.Event
		if json.Unmarshal(line, &e) != nil || e.Type != schemas.EventToolCall {
			return
		}
		switch e.Source {
		case schemas.SourceAgentStream, schemas.SourceToolRunner, schemas.SourceGateway:
		default:
			return
		}
		if e.ToolCallID == "" && b.gatewayCalls > 0 {
			return // no id to match on; the gateway already showed the calls
		}
		b.addCall(e.TS, e.ToolCallID, e.ToolName, e.Target, string(e.Source))
	})
}

func (b *builder) beforeAgent(ts time.Time) bool {
	return !b.agentStarted.IsZero() && ts.Before(b.agentStarted)
}

// readInstruction returns the task instruction the agent was given, from the
// plan's instruction file (the one BENCH_INSTRUCTION_FILE names, else the
// file named instruction.md). No other planned file is read: notes and
// fixtures can carry probe content.
func readInstruction(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, planFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", planFile, err)
	}
	var plan struct {
		Env   map[string]string `json:"env"`
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(b, &plan) != nil {
		return "", nil
	}
	if p := plan.Env["BENCH_INSTRUCTION_FILE"]; p != "" {
		if text, ok := plan.Files[p]; ok {
			return text, nil
		}
	}
	keys := make([]string, 0, len(plan.Files))
	for k := range plan.Files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if path.Base(k) == instructBase {
			return plan.Files[k], nil
		}
	}
	return "", nil
}

// eachLine calls fn for every non-empty line of a JSONL file. A missing
// file has no lines; a line fn cannot parse is fn's to skip.
func eachLine(p string, fn func([]byte)) error {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(p), err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := readLine(r)
		if len(line) > 0 {
			fn(line)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(p), err)
		}
	}
}

// readLine reads one line of at most maxLineBytes; a longer line is skipped
// whole and returned empty.
func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	tooLong := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if !tooLong {
			if len(out)+len(chunk) > maxLineBytes {
				tooLong, out = true, nil
			} else {
				out = append(out, chunk...)
			}
		}
		if err != nil || !isPrefix {
			if tooLong {
				out = nil
			}
			return out, err
		}
	}
}

// messageText returns the text of a message content: a string, or the text
// parts of a content array.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// argsText returns tool arguments as text: a JSON string is unquoted, any
// other JSON value is kept as written.
func argsText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func redact(s string) string {
	return nonceRE.ReplaceAllString(s, nonceRedacted)
}

// clip cuts s to at most n bytes at a UTF-8 boundary.
func clip(s string, n int) (string, bool) {
	if n <= 0 || len(s) <= n {
		return s, false
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
