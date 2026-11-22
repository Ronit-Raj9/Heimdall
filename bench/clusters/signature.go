package clusters

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Layer kinds, most specific first (docs/failure-clusters.md).
const (
	LayerOutcome          = "outcome"
	LayerFailedTests      = "failed_tests"
	LayerFailedDimensions = "failed_dimensions"
	LayerAnswerCategory   = "answer_category"
	LayerStateChecks      = "state_checks"
	LayerGates            = "gates"
	LayerLoop             = "loop"
	LayerError            = "error"
	LayerProvider         = "provider"
	LayerLastTool         = "last_tool"
	LayerCondition        = "condition"
)

// Error fingerprint sources (Fingerprint.Source).
const (
	SourcePartFailure  = "part_failure"
	SourceToolProxy    = "tool_proxy"
	SourceAgent        = "agent"
	SourceModel        = "model"
	SourceStderr       = "stderr"
	SourceFinalMessage = "final_message"
	SourceGrader       = "grader"
)

// patternLoop is the triage metric whose reason names the repeated call
// (docs/triage.md). Clusters reuse it rather than detect loops again.
const patternLoop = "pattern_loop"

// gatewayIncomplete is the summary prefix of the marker event the gateway
// source writes when Bifrost did not return every row; it is not an error
// of the run.
const gatewayIncomplete = "gateway request log incomplete"

// exitStatus matches a command result summary that reports a non-zero exit,
// the same rule triage uses for a failed tool result (docs/triage.md).
var exitStatus = regexp.MustCompile(`(?i)^\s*exit(?: code)?:?\s+[1-9][0-9]*\b`)

// loopCall extracts the repeated call from a pattern_loop reason summary:
// `Bash "pytest -q" repeated 3 times in a row`.
var loopCall = regexp.MustCompile(`^(.*?) repeated \d+ times`)

// Input is everything one verified sealed run contributes to its signature:
// the sealed record (grade, gates, metrics) and the parsed evidence files.
// Missing files are nil.
type Input struct {
	Run    schemas.SealedRun
	Events []schemas.Event
	Ledger []schemas.LedgerEntry
	Tools  []schemas.ToolCall
	Stderr string
}

// FailedSet is what fell short in a failed run: hidden test ids, reward
// dimensions, the answer category or state check ids, by Kind (a layer kind).
type FailedSet struct {
	Kind string
	IDs  []string // sorted, unique
}

// Fingerprint is a run's normalized error line and where it came from.
type Fingerprint struct {
	Source string
	Tool   string // tool name, for tool sources
	Text   string // normalized first stable line
}

// LastTool is the run's last tool call and whether it errored.
type LastTool struct {
	Name    string
	Errored *bool // nil when no result was observed
}

// Features are every signal of one run, before the signature picks layers.
type Features struct {
	Outcome   string
	Failed    *FailedSet
	Gates     []string // fired gate names, sorted
	Loop      string   // normalized repeated call from pattern_loop
	Error     *Fingerprint
	Provider  string // real upstream status at the end of the run: "503", "transport error"
	LastTool  *LastTool
	Condition string
}

// Layer is one part of a signature. Key is the canonical value the hash
// reads; Label and Items are for display.
type Layer struct {
	Kind   string   `json:"kind"`
	Label  string   `json:"label"`
	Items  []string `json:"items,omitempty"`
	Source string   `json:"source,omitempty"`
	key    string
}

// Extract computes the features of one run. It is pure.
func Extract(in Input) Features {
	r := in.Run
	f := Features{Outcome: string(r.Run.Outcome), Condition: r.Slot.Condition}
	if f.Outcome == "" {
		f.Outcome = string(schemas.OutcomeUnknown)
	}
	if r.Run.Outcome == schemas.OutcomeFail {
		f.Failed = failedSet(r.Grade)
	}
	for _, g := range r.Gates {
		if g.Fired {
			f.Gates = append(f.Gates, g.Gate)
		}
	}
	slices.Sort(f.Gates)
	f.Gates = slices.Compact(f.Gates)
	f.Loop = loopOf(r.Metrics)
	f.Error = fingerprint(in)
	f.Provider = providerStatus(in.Ledger)
	f.LastTool = lastTool(in)
	return f
}

// Layers picks the layers of a run's signature, most specific first. The
// outcome is always one; fired gates always join. The rest depends on the
// outcome, so a signature names a cause without splitting one cause into
// many clusters by incidental detail:
//
//   - fail: the failed set, plus the error fingerprint when there is one;
//   - timeout, budget: the repeated call (pattern_loop), plus the error
//     fingerprint;
//   - crash, unknown: the error fingerprint, plus the provider status;
//   - refusal: the final message fingerprint;
//   - integrity_violation: the gates alone;
//   - invalid_evaluator: the grader's reason;
//   - infra_error: the provider status, plus the error fingerprint.
//
// When the outcome-specific layers find nothing, the last tool call (when
// it errored) and then the probe condition stand in, so a run never
// clusters on its outcome alone while any signal is left. It is pure.
func Layers(f Features) []Layer {
	out := []Layer{{Kind: LayerOutcome, Label: f.Outcome, key: f.Outcome}}
	if len(f.Gates) > 0 {
		out = append(out, Layer{Kind: LayerGates, Label: strings.Join(f.Gates, ", "), Items: f.Gates, key: strings.Join(f.Gates, "\x1f")})
	}
	cause := len(out)
	switch schemas.Outcome(f.Outcome) {
	case schemas.OutcomeFail:
		out = appendFailed(out, f.Failed)
		out = appendError(out, f.Error)
	case schemas.OutcomeTimeout, schemas.OutcomeBudget:
		if f.Loop != "" {
			out = append(out, Layer{Kind: LayerLoop, Label: f.Loop, key: f.Loop})
		}
		out = appendError(out, f.Error)
	case schemas.OutcomeRefusal:
		if f.Error != nil && f.Error.Source == SourceFinalMessage {
			out = appendError(out, f.Error)
		}
	case schemas.OutcomeIntegrityViolation:
		return out
	case schemas.OutcomeInvalidEvaluator:
		if f.Error != nil && f.Error.Source == SourceGrader {
			out = appendError(out, f.Error)
		}
	case schemas.OutcomeInfraError:
		out = appendProvider(out, f.Provider)
		out = appendError(out, f.Error)
	default:
		out = appendError(out, f.Error)
		out = appendProvider(out, f.Provider)
	}
	if len(out) > cause {
		return out
	}
	if f.LastTool != nil && f.LastTool.Errored != nil && *f.LastTool.Errored {
		return append(out, Layer{Kind: LayerLastTool, Label: f.LastTool.Name + " errored", key: f.LastTool.Name + "\x1ferrored"})
	}
	if f.Condition != "" && f.Condition != schemas.ConditionClean {
		out = append(out, Layer{Kind: LayerCondition, Label: f.Condition, key: f.Condition})
	}
	return out
}

// Signature is the stable id of a list of layers: "c_" and the first 16 hex
// characters of the sha256 over each layer's kind and canonical key. The
// same layers always give the same signature, whatever run, order of runs
// or filter produced them. It is pure.
func Signature(layers []Layer) string {
	var b strings.Builder
	for _, l := range layers {
		b.WriteString(l.Kind)
		b.WriteByte('=')
		b.WriteString(l.key)
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "c_" + hex.EncodeToString(sum[:])[:16]
}

// outcomeSentences name a signature that has only its outcome layer: the
// bare outcome ("fail") reads as a raw id on the failure groups page.
var outcomeSentences = map[string]string{
	string(schemas.OutcomeFail):    "Failed with no error recorded",
	string(schemas.OutcomeTimeout): "Timed out",
	string(schemas.OutcomeCrash):   "Crashed",
	string(schemas.OutcomeBudget):  "Ran out of budget",
	string(schemas.OutcomeRefusal): "Refused the task",
}

// outcomeOnly reports whether layers hold nothing but the outcome.
func outcomeOnly(layers []Layer) bool {
	return len(layers) == 1 && layers[0].Kind == LayerOutcome
}

// TitleIn is Title with where the runs of a cluster happened: a signature
// that has only its outcome layer gets " on <benchmark>" when every run is
// on one benchmark and " in <tool>" when the runs share a most used last
// tool, so "fail" reads "Failed with no error recorded on swe-lite in
// Bash". Other signatures already name what failed and keep Title. It is
// pure.
func TitleIn(layers []Layer, benchmark, tool string) string {
	t := Title(layers)
	if !outcomeOnly(layers) {
		return t
	}
	if benchmark != "" {
		t += " on " + benchmark
	}
	if tool != "" {
		t += " in " + tool
	}
	return t
}

// Title is the human name of a signature, built from its layers: "fails
// test_limiter_window", "timeout after repeated bash \"pytest -q\"",
// "g1_protected_paths gate", "tool error: desk.refund 500". A signature with
// only its outcome layer reads as a sentence: "Failed with no error
// recorded", "Timed out", "Crashed". It is pure.
func Title(layers []Layer) string {
	if outcomeOnly(layers) {
		if s, ok := outcomeSentences[layers[0].Label]; ok {
			return s
		}
	}
	var head string
	var parts []string
	for _, l := range layers {
		switch l.Kind {
		case LayerOutcome:
			head = l.Label
		case LayerFailedTests:
			head = "fails " + nameList(l.Items, shortTest, 1)
		case LayerStateChecks:
			head = "state check " + nameList(l.Items, nil, 1) + " fails"
		case LayerFailedDimensions:
			head = "falls short on " + nameList(l.Items, nil, 2)
		case LayerAnswerCategory:
			head = "wrong answer (" + l.Label + ")"
		case LayerGates:
			parts = append(parts, nameList(l.Items, nil, 2)+" gate")
		case LayerLoop:
			head += " after repeated " + l.Label
		case LayerError:
			parts = append(parts, errorTitle(l))
		case LayerProvider:
			parts = append(parts, "provider "+l.Label)
		case LayerLastTool:
			parts = append(parts, "tool "+l.Label)
		case LayerCondition:
			parts = append(parts, "under "+l.Label)
		}
	}
	switch {
	case len(parts) == 0:
		return head
	case head == string(schemas.OutcomeIntegrityViolation):
		return strings.Join(parts, "; ")
	case strings.HasPrefix(head, "fails ") || strings.HasPrefix(head, "state check ") ||
		strings.HasPrefix(head, "falls short ") || strings.HasPrefix(head, "wrong answer "):
		return head + "; " + strings.Join(parts, "; ")
	default:
		return head + ": " + strings.Join(parts, "; ")
	}
}

// failedSet reads what fell short from the grade: state check ids for a
// state grade, the failed and missing hidden tests, the reward dimensions
// below 1 (dimensions are scored 0 to 1), or the answer category.
func failedSet(g *schemas.Grade) *FailedSet {
	if g == nil {
		return nil
	}
	ids := append(slices.Clone(g.FailedTests), g.MissingRequired...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	switch {
	case len(ids) > 0 && g.Mode == "state":
		return &FailedSet{Kind: LayerStateChecks, IDs: ids}
	case len(ids) > 0:
		return &FailedSet{Kind: LayerFailedTests, IDs: ids}
	}
	var dims []string
	for name, v := range g.Dimensions {
		if v < 1 {
			dims = append(dims, name)
		}
	}
	if len(dims) > 0 {
		slices.Sort(dims)
		return &FailedSet{Kind: LayerFailedDimensions, IDs: dims}
	}
	if g.Category != "" {
		return &FailedSet{Kind: LayerAnswerCategory, IDs: []string{g.Category}}
	}
	return nil
}

// loopOf returns the repeated call of an observed pattern_loop, normalized,
// or "" when the metric is missing, unknown or 0.
func loopOf(metrics map[string]schemas.Value) string {
	v, ok := metrics[patternLoop]
	if !ok || v.State != schemas.ValueObserved {
		return ""
	}
	if n, ok := v.Float(); !ok || n <= 0 {
		return ""
	}
	summary := v.Reason
	if _, rest, found := strings.Cut(summary, "; "); found && strings.HasPrefix(summary, "events:") {
		summary = rest
	}
	m := loopCall.FindStringSubmatch(summary)
	if m == nil {
		return ""
	}
	return NormalizeLine(m[1])
}

// fingerprint picks the run's error line, first source that has one:
// for a refusal the final message; then a part failure the engine recorded;
// the last errored call in the tool proxy ledger; the agent's terminal error
// event (no agent tool call or model reply after it); the gateway's terminal
// failed model reply; the first stderr line with an error word; for a crash
// the final message; for an invalid evaluator the grader's reason. Errors
// the agent carried on past are not the cause of how it ended and are left
// out (a startup warning logged as an error, for example).
func fingerprint(in Input) *Fingerprint {
	r := in.Run
	claim := ""
	if r.Run.Claim != nil {
		claim = r.Run.Claim.Text
	}
	if r.Run.Outcome == schemas.OutcomeRefusal {
		if line := FirstStableLine(claim, false); line != "" {
			return &Fingerprint{Source: SourceFinalMessage, Text: line}
		}
	}
	if r.Run.Outcome == schemas.OutcomeInvalidEvaluator {
		if r.Grade != nil {
			if line := FirstStableLine(r.Grade.Reason, false); line != "" {
				return &Fingerprint{Source: SourceGrader, Text: line}
			}
		}
		return nil
	}
	for _, pf := range r.Run.Failures {
		if line := FirstStableLine(pf.Error, false); line != "" {
			return &Fingerprint{Source: SourcePartFailure, Tool: pf.Part, Text: line}
		}
	}
	if fp := toolProxyError(in.Tools); fp != nil {
		return fp
	}
	if fp := terminalError(in.Events, schemas.SourceAgentStream, SourceAgent); fp != nil {
		return fp
	}
	if fp := terminalError(in.Events, schemas.SourceGateway, SourceModel); fp != nil {
		return fp
	}
	if line := FirstStableLine(in.Stderr, true); line != "" {
		return &Fingerprint{Source: SourceStderr, Text: line}
	}
	if r.Run.Outcome == schemas.OutcomeCrash {
		if line := FirstStableLine(claim, false); line != "" {
			return &Fingerprint{Source: SourceFinalMessage, Text: line}
		}
	}
	return nil
}

// toolProxyError is the last errored call in the tool proxy ledger, as
// "<server>.<tool> <status>" and the first stable line of its result.
func toolProxyError(calls []schemas.ToolCall) *Fingerprint {
	for i := len(calls) - 1; i >= 0; i-- {
		c := calls[i]
		if !c.IsError && c.Status < 400 {
			continue
		}
		name := c.Tool
		if c.Server != "" {
			name = c.Server + "." + c.Tool
		}
		text := ""
		if c.Status > 0 {
			text = fmt.Sprint(c.Status)
		}
		if c.Fault != "" {
			text = strings.TrimSpace(text + " injected " + c.Fault)
		}
		if line := FirstStableLine(c.Result, false); line != "" {
			text = strings.TrimSpace(text + " " + line)
		}
		return &Fingerprint{Source: SourceToolProxy, Tool: NormalizeLine(name), Text: clip(text, maxLineRunes)}
	}
	return nil
}

// terminalError returns the last error event of one event source when the
// source shows no tool call or successful model reply after it: the error
// the run ended on. For the agent stream that is an error event; for the
// gateway a model reply flagged as an error.
func terminalError(events []schemas.Event, src schemas.EventSource, label string) *Fingerprint {
	var last *schemas.Event
	for i := range events {
		e := &events[i]
		if e.Source != src {
			continue
		}
		switch {
		case isErrorEvent(e, src):
			last = e
		case e.Type == schemas.EventToolCall || e.Type == schemas.EventModelResponse:
			last = nil
		}
	}
	if last == nil {
		return nil
	}
	line := FirstStableLine(last.Summary, false)
	if line == "" {
		return nil
	}
	return &Fingerprint{Source: label, Text: line}
}

// isErrorEvent reports whether an event is an error of its source: an
// agent stream error event, or a gateway model reply flagged as an error.
// The gateway's incomplete-log marker is not one.
func isErrorEvent(e *schemas.Event, src schemas.EventSource) bool {
	if strings.HasPrefix(e.Summary, gatewayIncomplete) {
		return false
	}
	if src == schemas.SourceGateway {
		return e.Type == schemas.EventModelResponse && (e.IsError || e.Status == schemas.EventStatusError)
	}
	return e.Type == schemas.EventError
}

// providerStatus is the real provider's answer to the run's last model call
// when it failed: its HTTP status, or "transport error". Calls the fault
// proxy answered itself (action inject) are the probe, not the provider,
// and are skipped.
func providerStatus(ledger []schemas.LedgerEntry) string {
	for i := len(ledger) - 1; i >= 0; i-- {
		e := ledger[i]
		if e.Kind != "message" || e.Action == "inject" {
			continue
		}
		switch {
		case e.UpstreamError != "":
			return "transport error"
		case e.Status >= 400:
			return fmt.Sprint(e.Status)
		default:
			return ""
		}
	}
	return ""
}

// lastTool is the run's last tool call: from the tool proxy ledger when the
// run had proxied services (the bench witnessed it), else from the agent
// stream (which carries tool outcomes), else from the gateway rows (which do
// not, so Errored stays nil).
func lastTool(in Input) *LastTool {
	if n := len(in.Tools); n > 0 {
		c := in.Tools[n-1]
		name := c.Tool
		if c.Server != "" {
			name = c.Server + "." + c.Tool
		}
		errored := c.IsError || c.Status >= 400
		return &LastTool{Name: name, Errored: &errored}
	}
	for _, src := range []schemas.EventSource{schemas.SourceAgentStream, schemas.SourceGateway} {
		if lt := lastToolOf(in.Events, src); lt != nil {
			return lt
		}
	}
	return nil
}

// lastToolOf returns the last tool call of one source and, when a result
// answering it follows, whether that result failed (the triage rule: flagged
// by the parser, or a command summary with a non-zero exit).
func lastToolOf(events []schemas.Event, src schemas.EventSource) *LastTool {
	var call *schemas.Event
	var result *schemas.Event
	for i := range events {
		e := &events[i]
		if e.Source != src {
			continue
		}
		switch e.Type {
		case schemas.EventToolCall:
			call, result = e, nil
		case schemas.EventToolResult:
			if call != nil && (e.ParentEventID == call.EventID || (e.ToolCallID != "" && e.ToolCallID == call.ToolCallID) || e.ToolCallID == "") {
				result = e
			}
		}
	}
	if call == nil {
		return nil
	}
	name := call.ToolName
	if name == "" {
		name = "tool"
	}
	lt := &LastTool{Name: name}
	if result != nil && src != schemas.SourceGateway {
		errored := result.IsError || exitStatus.MatchString(result.Summary)
		lt.Errored = &errored
	}
	return lt
}

// appendFailed adds the failed set layer.
func appendFailed(out []Layer, fs *FailedSet) []Layer {
	if fs == nil {
		return out
	}
	label := fs.IDs[0]
	switch {
	case fs.Kind == LayerFailedTests && len(fs.IDs) > 1:
		label = fmt.Sprintf("%d hidden tests", len(fs.IDs))
	case fs.Kind == LayerFailedTests:
		label = "1 hidden test"
	case len(fs.IDs) > 1:
		label = strings.Join(fs.IDs, ", ")
	}
	return append(out, Layer{Kind: fs.Kind, Label: label, Items: fs.IDs, key: strings.Join(fs.IDs, "\x1f")})
}

// appendError adds the error fingerprint layer.
func appendError(out []Layer, fp *Fingerprint) []Layer {
	if fp == nil {
		return out
	}
	label := fp.Text
	if fp.Tool != "" {
		label = fp.Tool + " " + fp.Text
	}
	return append(out, Layer{Kind: LayerError, Label: strings.TrimSpace(label), Source: fp.Source,
		key: fp.Source + "\x1f" + fp.Tool + "\x1f" + fp.Text})
}

// appendProvider adds the provider status layer.
func appendProvider(out []Layer, status string) []Layer {
	if status == "" {
		return out
	}
	return append(out, Layer{Kind: LayerProvider, Label: status, key: status})
}

// errorTitle phrases an error layer for a title by its source.
func errorTitle(l Layer) string {
	text := clip(l.Label, 80)
	switch l.Source {
	case SourceToolProxy:
		return "tool error: " + text
	case SourceModel:
		return "model error: " + text
	case SourceStderr:
		return "stderr: " + text
	case SourceFinalMessage:
		return "says: " + text
	case SourcePartFailure:
		return "part failure: " + text
	default:
		return "error: " + text
	}
}

// nameList names the first n items and counts the rest.
func nameList(items []string, short func(string) string, n int) string {
	names := make([]string, 0, n)
	for i, it := range items {
		if i == n {
			break
		}
		if short != nil {
			it = short(it)
		}
		names = append(names, it)
	}
	s := strings.Join(names, ", ")
	if len(items) > n {
		s += fmt.Sprintf(" and %d more", len(items)-n)
	}
	return s
}

// shortTest is the test function of a test id: the part after the last
// "::" or, failing that, the last ".".
func shortTest(id string) string {
	if i := strings.LastIndex(id, "::"); i >= 0 {
		return id[i+2:]
	}
	if i := strings.LastIndex(id, "."); i >= 0 && i < len(id)-1 {
		return id[i+1:]
	}
	return id
}

// parseJSONL decodes one JSON object per line into T, skipping blank and
// malformed lines: a torn last line must not hide the rest of the evidence.
func parseJSONL[T any](data []byte) []T {
	var out []T
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(line, &v); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// sortEvents orders events by source then sequence, the order each source
// wrote them in.
func sortEvents(events []schemas.Event) {
	slices.SortStableFunc(events, func(x, y schemas.Event) int {
		return cmp.Or(cmp.Compare(x.Source, y.Source), cmp.Compare(x.Seq, y.Seq))
	})
}
