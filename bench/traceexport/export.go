// Package traceexport turns one sealed run into an OpenTelemetry trace in
// OTLP/JSON, so a run can be read in any OTLP backend (Maxim, Langfuse,
// Jaeger, an OpenTelemetry Collector). The run is the root span; sessions,
// model requests, tool calls, the grade and each gate are child spans, and
// injected faults are span events. The commands and file changes the trusted
// observers saw are child spans of the model turn they follow. Model and tool spans follow the
// OpenTelemetry GenAI semantic conventions (gen_ai.*); everything else is
// under heimdall.*. docs/trace-export.md lists every span and attribute.
//
// The export reads only sealed evidence and is deterministic: trace and span
// ids are derived from the run id and the record each span comes from, so
// exporting the same run twice gives the same bytes. A value the run did not
// observe is left out, never written as zero. Tool arguments and results are
// left out unless Options.Payloads is set; then they are redacted and capped.
// Build never reads files and never checks a seal: the caller loads the run
// (LoadRun, LoadFiles) and verifies its seal first.
package traceexport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ExporterVersion is the version of the span layout. It changes when a span
// or attribute changes meaning, and is the instrumentation scope version.
const ExporterVersion = "1.0.0"

// ScopeName is the instrumentation scope of every exported span.
const ScopeName = "github.com/maximhq/bifrost/bench/traceexport"

// ServiceName is the resource service.name of every exported run.
const ServiceName = "heimdall"

// DefaultPayloadCap is the most bytes of one tool argument or result kept
// when payloads are exported.
const DefaultPayloadCap = 2048

// textCap is the most bytes kept of short free text: error messages, gate
// details, grade reasons.
const textCap = 500

// listCap is the most entries kept of a list attribute.
const listCap = 50

// ErrUnsealed is returned for a run without a seal hash: only sealed
// evidence is exported.
var ErrUnsealed = errors.New("run is not sealed")

// Input is everything the exporter reads about one sealed run.
type Input struct {
	Slot        schemas.Slot
	Run         schemas.Run
	SealHash    string // sha256 of the run's seal.json
	Evidence    schemas.Evidence
	Metrics     map[string]schemas.Value // metrics.json; nil when the run has none
	Transitions []schemas.Transition     // transitions.jsonl, for stage times
}

// Options controls what an export includes.
type Options struct {
	// Payloads includes tool call arguments and results, the agent's answer
	// and its claim text, each redacted and capped at PayloadCap. Off by
	// default: then only their sha256 and size are exported.
	Payloads       bool
	PayloadCap     int    // bytes per payload; 0 means DefaultPayloadCap
	ServiceVersion string // bench version, written as service.version when set
}

// Build returns the trace of one run: one resource, one scope, the root run
// span first and its children after it in a fixed order. The input must
// carry a run id and a seal hash.
func Build(in Input, opts Options) (*TracesData, error) {
	if in.Run.RunID == "" {
		return nil, fmt.Errorf("build trace: run has no id")
	}
	if in.SealHash == "" {
		return nil, fmt.Errorf("build trace for run %s: %w", in.Run.RunID, ErrUnsealed)
	}
	if opts.PayloadCap <= 0 {
		opts.PayloadCap = DefaultPayloadCap
	}
	b := newBuilder(in, opts)
	if b.window.start.IsZero() {
		return nil, fmt.Errorf("build trace for run %s: the run records no times", in.Run.RunID)
	}
	b.sessionSpans()
	b.modelSpans()
	b.toolSpans()
	b.stepSpans()
	b.gradeSpan()
	b.gateSpans()
	b.faultEvents()
	root := b.rootSpan()
	spans := append([]Span{root}, b.spans...)
	td := &TracesData{ResourceSpans: []ResourceSpans{{
		Resource:   Resource{Attributes: b.resourceAttrs()},
		ScopeSpans: []ScopeSpans{{Scope: Scope{Name: ScopeName, Version: ExporterVersion}, Spans: spans}},
	}}}
	scrub(td)
	return td, nil
}

// TraceID returns the trace id of a run: the first 16 bytes of
// sha256("heimdall/trace/" + run id), in hex.
func TraceID(runID string) string {
	return hashID("heimdall/trace/"+runID, 16)
}

// SpanID returns the span id of the record key within a run: the first 8
// bytes of sha256("heimdall/span/" + run id + "/" + key), in hex. Keys are
// "run", "session:<index>", "model:<logical request id>", "tool_call:<tool
// call id>", "tool:<ledger seq>", "step:<timeline seq>", "grade" and
// "gate:<index>:<gate>".
func SpanID(runID, key string) string {
	return hashID("heimdall/span/"+runID+"/"+key, 8)
}

// window is a span's time range.
type window struct{ start, end time.Time }

// cover widens w to include t; a zero t changes nothing.
func (w *window) cover(t time.Time) {
	if t.IsZero() {
		return
	}
	if w.start.IsZero() || t.Before(w.start) {
		w.start = t
	}
	if w.end.IsZero() || t.After(w.end) {
		w.end = t
	}
}

// contains reports whether t lies in w.
func (w window) contains(t time.Time) bool {
	return !t.IsZero() && !t.Before(w.start) && !t.After(w.end)
}

// sessionRef is a session span, for parenting the spans inside it.
type sessionRef struct {
	id     string // SessionResult.ID; empty for the single implicit session
	spanID string
	window window
}

// modelRef is a model request span, for parenting its tool calls and faults.
type modelRef struct {
	spanID string
	index  int // into builder.spans
	window window
	step   int // logical step number, for parenting timeline rows by turn
}

// builder carries the state of one Build.
type builder struct {
	in       Input
	opts     Options
	traceID  string
	rootID   string
	window   window // the run's own times; the root span covers this and every child
	stages   map[schemas.Stage]window
	spans    []Span
	sessions []sessionRef
	models   []modelRef
	rootEvts []SpanEvent
}

// newBuilder sets the trace id and the run's window from its timing,
// transitions and execution record.
func newBuilder(in Input, opts Options) *builder {
	b := &builder{in: in, opts: opts, traceID: TraceID(in.Run.RunID), stages: map[schemas.Stage]window{}}
	b.rootID = SpanID(in.Run.RunID, "run")
	t := in.Run.Timing
	b.window.cover(t.Started)
	if t.Sealed != nil {
		b.window.cover(*t.Sealed)
	}
	for _, tr := range in.Transitions {
		w := b.stages[tr.Stage]
		w.cover(tr.At)
		b.stages[tr.Stage] = w
		b.window.cover(tr.At)
	}
	if ex := in.Evidence.Exec; ex != nil {
		b.window.cover(ex.Started)
		b.window.cover(ex.Ended)
	}
	return b
}

// add appends a span of the run's trace and returns its index.
func (b *builder) add(key, parent, name string, kind SpanKind, w window, attrs []KeyValue) int {
	if w.end.Before(w.start) {
		w.end = w.start
	}
	b.spans = append(b.spans, Span{
		TraceID: b.traceID, SpanID: SpanID(b.in.Run.RunID, key), ParentSpanID: parent,
		Name: name, Kind: kind, StartTimeUnixNano: nanos(w.start), EndTimeUnixNano: nanos(w.end), Attributes: attrs,
	})
	return len(b.spans) - 1
}

// rootSpan is the run: identity, outcome and hashes, covering every child.
func (b *builder) rootSpan() Span {
	w := b.window
	for _, sp := range b.spans {
		w.cover(time.Unix(0, int64(sp.StartTimeUnixNano)))
		w.cover(time.Unix(0, int64(sp.EndTimeUnixNano)))
	}
	sp := Span{TraceID: b.traceID, SpanID: b.rootID, Name: "heimdall.run " + b.in.Slot.Task, Kind: SpanKindInternal,
		StartTimeUnixNano: nanos(w.start), EndTimeUnixNano: nanos(w.end), Attributes: b.runAttrs(), Events: b.rootEvts}
	if sp.Name == "heimdall.run " {
		sp.Name = "heimdall.run"
	}
	switch b.in.Run.Outcome {
	case schemas.OutcomeInfraError, schemas.OutcomeAbortedInfra, schemas.OutcomeInvalidEvaluator, schemas.OutcomeIntegrityViolation:
		sp.Status = &Status{Code: StatusError, Message: "outcome " + string(b.in.Run.Outcome)}
	}
	return sp
}

// runAttrs are the root span's attributes.
func (b *builder) runAttrs() []KeyValue {
	r, s := b.in.Run, b.in.Slot
	a := attrs{}
	a.str("heimdall.run.id", r.RunID)
	a.str("heimdall.experiment", firstNonEmpty(s.ExpID, r.ExpID))
	a.str("heimdall.slot", firstNonEmpty(s.SlotID, r.SlotID))
	a.str("heimdall.pair", firstNonEmpty(s.PairID, r.PairID))
	a.str("heimdall.suite", s.Suite)
	a.str("heimdall.task", s.Task)
	a.str("heimdall.task.version", s.TaskVersion)
	a.str("heimdall.task.hash", firstNonEmpty(r.TaskHash, s.TaskHash))
	a.str("heimdall.arm", s.Arm)
	a.str("heimdall.condition", s.Condition)
	if s.Trial > 0 {
		a.int("heimdall.trial", int64(s.Trial))
	}
	if r.Attempt > 0 {
		a.int("heimdall.run.attempt", int64(r.Attempt))
	}
	a.str("heimdall.agent.config_id", r.Agent.ConfigID)
	a.str("heimdall.agent.config_hash", r.Agent.ConfigHash)
	a.str("heimdall.agent.adapter", r.Agent.Adapter)
	a.str("heimdall.agent.version", r.Agent.AgentVersion)
	a.str("heimdall.agent.image_digest", r.Agent.ImageDigest)
	a.str("gen_ai.agent.name", r.Agent.ConfigID)
	a.str("gen_ai.conversation.id", r.Gateway.SessionID)
	a.str("heimdall.outcome", string(r.Outcome))
	if v, ok := b.in.Metrics["verified_success"]; ok {
		if f, observed := v.Float(); observed {
			a.bool("heimdall.verified_success", f >= 1)
		}
	}
	if v, ok := b.in.Metrics["cost_usd"]; ok {
		if f, observed := v.Float(); observed {
			a.double("heimdall.cost_usd", f)
		}
	}
	a.str("heimdall.manifest_hash", r.ManifestHash)
	a.str("heimdall.suite_lock_hash", r.SuiteLockHash)
	a.str("heimdall.run_input_hash", r.RunInputHash)
	a.str("heimdall.canonical_hash", r.CanonicalHash)
	a.str("heimdall.seal_hash", b.in.SealHash)
	if r.Agent.ExitCode != nil {
		a.int("heimdall.agent.exit_code", int64(*r.Agent.ExitCode))
	}
	a.bool("heimdall.agent.timed_out", r.Agent.TimedOut)
	b.claimAttrs(&a)
	b.answerAttrs(&a)
	if rl := b.in.Evidence.Requests; rl != nil {
		a.bool("heimdall.gateway.log_complete", rl.Complete)
	}
	for _, name := range sortedKeys(b.in.Metrics) {
		if f, ok := b.in.Metrics[name].Float(); ok {
			a.double("heimdall.metric."+name, f)
		}
	}
	return a
}

// claimAttrs adds whether the agent claimed success; the claim's text is a
// payload.
func (b *builder) claimAttrs(a *attrs) {
	c := b.in.Run.Claim
	if c == nil {
		c = b.in.Evidence.Claim
	}
	if c == nil {
		return
	}
	a.bool("heimdall.claim.made", c.Made)
	if b.opts.Payloads {
		a.str("heimdall.claim.text", b.payload(c.Text))
	}
}

// answerAttrs adds the exported answer's hash and source; its text is a
// payload.
func (b *builder) answerAttrs(a *attrs) {
	ans := b.in.Evidence.Answer
	if ans == nil {
		return
	}
	a.str("heimdall.answer.source", ans.Source)
	a.str("heimdall.answer.sha256", ans.SHA256)
	if b.opts.Payloads && ans.Text != "" {
		a.str("heimdall.answer.text", b.payload(ans.Text))
	}
}

// resourceAttrs describe the producer and the experiment.
func (b *builder) resourceAttrs() []KeyValue {
	a := attrs{}
	a.str("service.name", ServiceName)
	a.str("service.version", b.opts.ServiceVersion)
	a.str("heimdall.experiment", firstNonEmpty(b.in.Slot.ExpID, b.in.Run.ExpID))
	a.str("heimdall.run.id", b.in.Run.RunID)
	return a
}

// sessionSpans adds one span per session of a multi-session run, or one for
// the single agent execution, named as GenAI agent invocations.
func (b *builder) sessionSpans() {
	agent := b.in.Run.Agent.ConfigID
	name := strings.TrimSpace("invoke_agent " + agent)
	if len(b.in.Evidence.Sessions) > 0 {
		for _, s := range b.in.Evidence.Sessions {
			w := window{}
			w.cover(s.Started)
			w.cover(s.Ended)
			if w.start.IsZero() {
				continue
			}
			key := fmt.Sprintf("session:%d", s.Index)
			a := b.sessionAttrs(agent)
			a.str("heimdall.session.id", s.ID)
			a.int("heimdall.session.index", int64(s.Index))
			a.int("heimdall.session.exit_code", int64(s.ExitCode))
			a.bool("heimdall.session.timed_out", s.TimedOut)
			i := b.add(key, b.rootID, name, SpanKindInternal, w, a)
			markSessionStatus(&b.spans[i], s.ExitCode, s.TimedOut)
			b.sessions = append(b.sessions, sessionRef{id: s.ID, spanID: b.spans[i].SpanID, window: w})
		}
		return
	}
	w := window{}
	if ex := b.in.Evidence.Exec; ex != nil {
		w.cover(ex.Started)
		w.cover(ex.Ended)
	}
	if w.start.IsZero() {
		if t := b.in.Run.Timing; t.AgentStarted != nil && t.AgentExited != nil {
			w.cover(*t.AgentStarted)
			w.cover(*t.AgentExited)
		}
	}
	if w.start.IsZero() {
		return
	}
	a := b.sessionAttrs(agent)
	a.int("heimdall.session.index", 1)
	if ex := b.in.Evidence.Exec; ex != nil {
		a.int("heimdall.session.exit_code", int64(ex.ExitCode))
		a.bool("heimdall.session.timed_out", ex.TimedOut)
	}
	i := b.add("session:1", b.rootID, name, SpanKindInternal, w, a)
	if ex := b.in.Evidence.Exec; ex != nil {
		markSessionStatus(&b.spans[i], ex.ExitCode, ex.TimedOut)
	}
	b.sessions = append(b.sessions, sessionRef{spanID: b.spans[i].SpanID, window: w})
}

// sessionAttrs are the attributes every session span shares.
func (b *builder) sessionAttrs(agent string) attrs {
	a := attrs{}
	a.str("gen_ai.operation.name", "invoke_agent")
	a.str("gen_ai.agent.name", agent)
	a.str("gen_ai.conversation.id", b.in.Run.Gateway.SessionID)
	return a
}

// markSessionStatus marks a session that crashed or timed out as an error.
func markSessionStatus(sp *Span, exitCode int, timedOut bool) {
	switch {
	case timedOut:
		sp.Status = &Status{Code: StatusError, Message: "timed out"}
	case exitCode != 0:
		sp.Status = &Status{Code: StatusError, Message: fmt.Sprintf("exit code %d", exitCode)}
	}
}

// parentAt returns the span a record at time t belongs under: the session
// with that id, else the session whose window holds t, else the run.
func (b *builder) parentAt(t time.Time, sessionID string) string {
	if sessionID != "" {
		for _, s := range b.sessions {
			if s.id == sessionID {
				return s.spanID
			}
		}
	}
	for _, s := range b.sessions {
		if s.window.contains(t) {
			return s.spanID
		}
	}
	return b.rootID
}

// stageWindow returns a stage's times, or a zero-length window at the end
// of the run when the run has no transitions for it.
func (b *builder) stageWindow(stage schemas.Stage) window {
	if w, ok := b.stages[stage]; ok {
		return w
	}
	return window{start: b.window.end, end: b.window.end}
}

// gradeSpan adds the hidden verifier's result.
func (b *builder) gradeSpan() {
	g := b.in.Evidence.Grade
	if g == nil {
		return
	}
	a := attrs{}
	a.bool("heimdall.grade.passed", g.Passed)
	a.bool("heimdall.grade.invalid_evaluator", g.InvalidEvaluator)
	a.int("heimdall.grade.exit_code", int64(g.ExitCode))
	a.int("heimdall.grade.tests_ran", int64(g.TestsRan))
	a.int("heimdall.grade.failures", int64(g.Failures))
	a.int("heimdall.grade.errors", int64(g.Errors))
	a.int("heimdall.grade.skipped", int64(g.Skipped))
	a.str("heimdall.grade.reason", clean(g.Reason, textCap))
	a.str("heimdall.grade.mode", g.Mode)
	a.str("heimdall.grade.result_format", g.ResultFormat)
	if g.Judged {
		a.bool("heimdall.grade.judged", true)
	}
	if g.Reward != nil {
		a.double("heimdall.grade.reward", *g.Reward)
	}
	for _, name := range sortedKeys(g.Dimensions) {
		a.double("heimdall.grade.dimension."+name, g.Dimensions[name])
	}
	a.strs("heimdall.grade.failed_tests", capList(g.FailedTests))
	a.strs("heimdall.grade.missing_required", capList(g.MissingRequired))
	a.str("heimdall.grade.category", g.Category)
	a.str("heimdall.grade.suite_version", g.SuiteVersion)
	a.str("heimdall.grade.grader_version", g.GraderVersion)
	a.str("heimdall.grade.diff_sha256", g.DiffSHA256)
	i := b.add("grade", b.rootID, "heimdall.grade", SpanKindInternal, b.stageWindow(schemas.StageGrade), a)
	if g.InvalidEvaluator {
		b.spans[i].Status = &Status{Code: StatusError, Message: "invalid evaluator: " + clean(g.Reason, textCap)}
	}
}

// gateSpans adds one span per gate, over the gate stage.
func (b *builder) gateSpans() {
	w := b.stageWindow(schemas.StageGate)
	for i, g := range b.in.Evidence.Gates {
		a := attrs{}
		a.str("heimdall.gate.name", g.Gate)
		a.str("heimdall.gate.version", g.Version)
		a.str("heimdall.gate.effect", string(g.Effect))
		a.bool("heimdall.gate.fired", g.Fired)
		a.str("heimdall.gate.detail", clean(g.Detail, textCap))
		ev := make([]string, 0, len(g.Evidence))
		for _, e := range capList(g.Evidence) {
			ev = append(ev, clean(e, textCap))
		}
		a.strs("heimdall.gate.evidence", ev)
		idx := b.add(fmt.Sprintf("gate:%d:%s", i, g.Gate), b.rootID, "heimdall.gate "+g.Gate, SpanKindInternal, w, a)
		if g.Fired && (g.Effect == schemas.GateVeto || g.Effect == schemas.GateInvalidate) {
			b.spans[idx].Status = &Status{Code: StatusError, Message: "gate fired: " + clean(g.Detail, textCap)}
		}
	}
}

// faultEvents adds every injected fault the evidence recorded (fault proxy
// and tool shim events) as a span event: on the model request span whose
// window holds it, else on the run span. Tool proxy faults are events of
// their tool span (toolSpans).
func (b *builder) faultEvents() {
	for _, e := range b.in.Evidence.Events {
		if e.Type != schemas.EventFault {
			continue
		}
		ts := e.TS
		if ts.IsZero() {
			ts = e.Timestamp
		}
		a := attrs{}
		a.str("heimdall.fault.source", string(e.Source))
		a.str("heimdall.fault.summary", clean(e.Summary, textCap))
		a.str("heimdall.fault.target", clean(e.Target, textCap))
		a.str("heimdall.event.id", e.EventID)
		if m := b.modelAt(ts); m != nil {
			sp := &b.spans[m.index]
			sp.Events = append(sp.Events, SpanEvent{TimeUnixNano: clampNanos(ts, m.window), Name: "heimdall.fault", Attributes: a})
			continue
		}
		if ts.IsZero() {
			ts = b.window.start
		}
		b.window.cover(ts)
		b.rootEvts = append(b.rootEvts, SpanEvent{TimeUnixNano: nanos(ts), Name: "heimdall.fault", Attributes: a})
	}
}

// modelAt returns the model request span whose window holds t, else the
// nearest one within a few milliseconds, which allows for the fault proxy's
// clock and the gateway's differing slightly.
func (b *builder) modelAt(t time.Time) *modelRef {
	if t.IsZero() {
		return nil
	}
	for i := range b.models {
		if b.models[i].window.contains(t) {
			return &b.models[i]
		}
	}
	const slack = 50 * time.Millisecond
	var best *modelRef
	bestGap := slack + 1
	for i := range b.models {
		m := &b.models[i]
		gap := m.window.start.Sub(t)
		if t.After(m.window.end) {
			gap = t.Sub(m.window.end)
		}
		if gap <= slack && gap < bestGap {
			best, bestGap = m, gap
		}
	}
	return best
}

// payload returns text as a payload attribute value: redacted and capped.
func (b *builder) payload(s string) string {
	return clean(s, b.opts.PayloadCap)
}

// attrs builds an attribute list, leaving out empty strings and empty lists
// so an unknown value is never exported as a zero.
type attrs []KeyValue

func (a *attrs) str(k, v string) {
	if v != "" {
		*a = append(*a, Str(k, v))
	}
}

func (a *attrs) strs(k string, vs []string) {
	if len(vs) > 0 {
		*a = append(*a, Strs(k, vs))
	}
}

func (a *attrs) int(k string, v int64)      { *a = append(*a, Int(k, v)) }
func (a *attrs) bool(k string, v bool)      { *a = append(*a, Bool(k, v)) }
func (a *attrs) double(k string, v float64) { *a = append(*a, Double(k, v)) }

var (
	noncePattern  = regexp.MustCompile(`nonce_[0-9a-z]{24}`)
	bearerPattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	keyPattern    = regexp.MustCompile(`\b(sk|pk)-[A-Za-z0-9_-]{8,}`)
)

// clean redacts secrets from text and caps it at max bytes. Redaction is the
// bench's own (utils.RedactSecrets, as evidence uses for gateway errors),
// plus bare bearer tokens and provider-shaped keys, and fault nonces, which
// are a probe's receipt and stay in the run folder.
func clean(s string, max int) string {
	if s == "" {
		return ""
	}
	return capText(redact(s), max)
}

// redact applies every redaction rule clean uses, without the cap.
func redact(s string) string {
	s = bearerPattern.ReplaceAllString(s, "$1 [redacted]")
	s = keyPattern.ReplaceAllString(s, "$1-[redacted]")
	s = utils.RedactSecrets(s)
	return noncePattern.ReplaceAllString(s, "nonce_[redacted]")
}

// capText cuts s to at most max bytes on a rune boundary and marks the cut.
func capText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " [truncated]"
}

// capList keeps at most listCap entries.
func capList(vs []string) []string {
	if len(vs) > listCap {
		return vs[:listCap]
	}
	return vs
}

// scrub redacts every string value, span name and status message of a trace
// once more. Fields are already redacted where they are set; this is the
// guard for one that was missed.
func scrub(td *TracesData) {
	for ri := range td.ResourceSpans {
		rs := &td.ResourceSpans[ri]
		scrubAttrs(rs.Resource.Attributes)
		for si := range rs.ScopeSpans {
			for pi := range rs.ScopeSpans[si].Spans {
				sp := &rs.ScopeSpans[si].Spans[pi]
				sp.Name = redact(sp.Name)
				scrubAttrs(sp.Attributes)
				for ei := range sp.Events {
					scrubAttrs(sp.Events[ei].Attributes)
				}
				if sp.Status != nil {
					sp.Status.Message = redact(sp.Status.Message)
				}
			}
		}
	}
}

func scrubAttrs(kvs []KeyValue) {
	for i := range kvs {
		scrubValue(&kvs[i].Value)
	}
}

func scrubValue(v *AnyValue) {
	if v.StringValue != nil {
		s := redact(*v.StringValue)
		v.StringValue = &s
	}
	if v.ArrayValue != nil {
		for i := range v.ArrayValue.Values {
			scrubValue(&v.ArrayValue.Values[i])
		}
	}
	if v.KvlistValue != nil {
		scrubAttrs(v.KvlistValue.Values)
	}
}

// hashID returns the first n bytes of sha256(s) in hex, never all zeros.
func hashID(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	id := sum[:n]
	allZero := true
	for _, c := range id {
		allZero = allZero && c == 0
	}
	if allZero {
		id[n-1] = 1
	}
	return hex.EncodeToString(id)
}

// nanos returns t in unix nanoseconds; a zero time is 0.
func nanos(t time.Time) Uint64 {
	if t.IsZero() {
		return 0
	}
	return Uint64(t.UnixNano())
}

// clampNanos returns t moved into w, in unix nanoseconds.
func clampNanos(t time.Time, w window) Uint64 {
	switch {
	case t.IsZero() || t.Before(w.start):
		return nanos(w.start)
	case t.After(w.end):
		return nanos(w.end)
	}
	return nanos(t)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
