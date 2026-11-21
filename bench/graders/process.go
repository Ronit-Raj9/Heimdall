package graders

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Process metric names. All three are report only: they are never part of
// verified_success, a gate or a verdict, whatever the experiment selects.
const (
	ProcessToolMatch      = "process_tool_match"
	ProcessStepRatio      = "process_step_ratio"
	ProcessRedundantCalls = "process_redundant_calls"
)

// Sources of the tool path a process score was computed from.
const (
	ProcessSourceToolProxy = "tool_proxy" // the tool proxy ledger: names and arguments
	ProcessSourceGateway   = "gateway"    // tool calls the model requested through Bifrost: names only
)

const (
	processVersion        = "1.0.0"
	maxReferenceBytes     = 4 << 20
	maxProcessCalls       = 5000
	reasonNoProcess       = "the task names no reference tool path"
	reasonNoToolEvidence  = "no tool calls were observed: the run has no tool proxy ledger and no gateway tool requests"
	reasonNoReferenceLen  = "the reference path has no tool calls"
	reasonArgsNotObserved = "the run's tool arguments were not observed (gateway names only)"
)

// ProcessCall is one tool call on a path: its name and, when observed, its
// arguments as decoded JSON.
type ProcessCall struct {
	Name      string
	Args      any
	ArgsKnown bool
	ArgsMatch string // expected calls only
}

// ProcessScore is the comparison of one run's tool path with the task's
// reference path.
type ProcessScore struct {
	Match          string   `json:"match"`
	Matched        bool     `json:"matched"`
	ArgsChecked    bool     `json:"args_checked"`
	Source         string   `json:"source"`
	AgentCalls     int      `json:"agent_calls"`
	ReferenceCalls int      `json:"reference_calls"`
	StepRatio      *float64 `json:"step_ratio,omitempty"`
	OverBudget     bool     `json:"over_budget,omitempty"`
	Redundant      *int     `json:"redundant,omitempty"`
	FirstDeviation int      `json:"first_deviation,omitempty"` // strict: 1-based position where the path first differs
	Missing        []string `json:"missing,omitempty"`         // reference calls the agent never made
	Extra          []string `json:"extra,omitempty"`           // agent calls outside the reference
}

// ProcessMetrics returns the report-only process metrics. cmd/bifrost-bench
// adds them to the registry beside metrics.Builtin.
func ProcessMetrics() []schemas.Metric {
	return []schemas.Metric{&processMetric{name: ProcessToolMatch}, &processMetric{name: ProcessStepRatio}, &processMetric{name: ProcessRedundantCalls}}
}

// ScoreProcess compares a run's tool path with the task's reference. It reads
// the reference file when the task names one. A task without a process block
// is not applicable; a run with no observed tool calls is unknown, never a
// mismatch.
func ScoreProcess(rc *schemas.RunContext, ev *schemas.Evidence) (*ProcessScore, string, error) {
	if rc == nil || rc.Task == nil || rc.Task.Process == nil {
		return nil, "", nil
	}
	spec := rc.Task.Process
	ref, err := referencePath(spec, rc.Task.Dir)
	if err != nil {
		return nil, "", err
	}
	agent, source := agentPath(ev)
	if source == "" {
		return nil, reasonNoToolEvidence, nil
	}
	s, err := CompareProcess(spec, ref, agent)
	if err != nil {
		return nil, "", err
	}
	s.Source = source
	return s, "", nil
}

// CompareProcess scores an agent path against a reference path under the
// spec's match mode. It is pure.
func CompareProcess(spec *schemas.ProcessSpec, ref, agent []ProcessCall) (*ProcessScore, error) {
	mode := spec.Match
	if mode == "" {
		mode = schemas.ProcessMatchStrict
	}
	switch mode {
	case schemas.ProcessMatchStrict, schemas.ProcessMatchUnordered, schemas.ProcessMatchSubset, schemas.ProcessMatchSuperset:
	default:
		return nil, fmt.Errorf("process match %q is not strict, unordered, subset or superset", mode)
	}
	if spec.MaxStepsRatio < 0 || math.IsNaN(spec.MaxStepsRatio) || spec.ReferenceSteps < 0 {
		return nil, errors.New("process max_steps_ratio and reference_steps must not be negative")
	}
	argsKnown := len(agent) == 0 || agent[0].ArgsKnown
	s := &ProcessScore{Match: mode, AgentCalls: len(agent), ReferenceCalls: len(ref), ArgsChecked: argsKnown}
	same := func(want, got ProcessCall) bool { return callMatches(want, got, argsKnown) }
	switch mode {
	case schemas.ProcessMatchStrict:
		s.Matched = len(ref) == len(agent)
		for i := range max(len(ref), len(agent)) {
			if i >= len(ref) || i >= len(agent) || !same(ref[i], agent[i]) {
				s.Matched = false
				s.FirstDeviation = i + 1
				break
			}
		}
	}
	missing, extra := multisetDiff(ref, agent, same)
	s.Missing, s.Extra = names(missing), names(extra)
	switch mode {
	case schemas.ProcessMatchUnordered:
		s.Matched = len(missing) == 0 && len(extra) == 0
	case schemas.ProcessMatchSubset:
		s.Matched = len(extra) == 0
	case schemas.ProcessMatchSuperset:
		s.Matched = len(missing) == 0
	}
	refSteps := spec.ReferenceSteps
	if refSteps == 0 {
		refSteps = len(ref)
	}
	if refSteps > 0 {
		r := float64(len(agent)) / float64(refSteps)
		s.StepRatio = &r
		s.OverBudget = spec.MaxStepsRatio > 0 && r > spec.MaxStepsRatio
	}
	if argsKnown {
		n := redundantCalls(agent)
		s.Redundant = &n
	}
	return s, nil
}

// ParseReference reads a reference path from JSON lines: tool proxy ledger
// rows (tool, arguments, kind) or {name, args} rows. Ledger rows that are not
// tool calls are skipped. It is pure.
func ParseReference(data []byte) ([]ProcessCall, error) {
	var out []ProcessCall
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxReferenceBytes)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			schemas.ToolCall
			Name string `json:"name"`
			Args any    `json:"args"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("reference line %d: %w", n, err)
		}
		switch {
		case row.Name != "":
			out = append(out, ProcessCall{Name: row.Name, Args: row.Args, ArgsKnown: row.Args != nil})
		case row.Tool != "":
			if !row.ToolCall.IsToolCall() {
				continue
			}
			out = append(out, ledgerCall(row.ToolCall))
		default:
			return nil, fmt.Errorf("reference line %d names no tool (want tool or name)", n)
		}
		if len(out) > maxProcessCalls {
			return nil, fmt.Errorf("reference has more than %d calls", maxProcessCalls)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read reference: %w", err)
	}
	return out, nil
}

// processMetric is one of the three report-only process metrics.
type processMetric struct{ name string }

// GetName implements schemas.Part.
func (m *processMetric) GetName() string { return m.name }

// GetVersion implements schemas.Part.
func (m *processMetric) GetVersion() string { return processVersion }

// Descriptor implements schemas.Metric.
func (m *processMetric) Descriptor() schemas.MetricDescriptor {
	d := schemas.MetricDescriptor{Name: m.name, Version: processVersion, MinEvidence: schemas.EvidenceGateway, ReportOnly: true}
	switch m.name {
	case ProcessToolMatch:
		d.Label, d.Kind, d.HigherIsBetter = "Tool path match", schemas.KindRate, true
		d.Description = "1 when the run's tool calls match the task's reference path under its match mode (strict, unordered, subset or superset), else 0; report only, never part of verified_success. Not applicable without a reference path."
	case ProcessStepRatio:
		d.Label, d.Kind = "Steps vs reference", schemas.KindCount
		d.Description = "The run's tool calls over the reference run's (1 is the same, 2 twice as many); report only."
	default:
		d.Label, d.Kind = "Repeated tool calls", schemas.KindCount
		d.Description = "Tool calls that repeat an earlier call with the same name and arguments; report only, unknown when arguments were not observed."
	}
	return d
}

// Compute implements schemas.Metric.
func (m *processMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	s, why, err := ScoreProcess(rc, ev)
	if err != nil {
		return schemas.Unknown("process: "+err.Error(), schemas.EvidenceGateway), nil
	}
	if why != "" {
		return schemas.Unknown(why, schemas.EvidenceGateway), nil
	}
	if s == nil {
		return schemas.NotApplicable(), nil
	}
	level := schemas.EvidenceGateway
	switch m.name {
	case ProcessToolMatch:
		v := 0.0
		if s.Matched {
			v = 1
		}
		return schemas.Observed(v, level), nil
	case ProcessStepRatio:
		if s.StepRatio == nil {
			return schemas.Unknown(reasonNoReferenceLen, level), nil
		}
		return schemas.Observed(*s.StepRatio, level), nil
	default:
		if s.Redundant == nil {
			return schemas.Unknown(reasonArgsNotObserved, level), nil
		}
		return schemas.Observed(float64(*s.Redundant), level), nil
	}
}

// referencePath is the spec's expected tools, or its reference file read
// from the task folder.
func referencePath(spec *schemas.ProcessSpec, dir string) ([]ProcessCall, error) {
	if len(spec.ExpectedTools) > 0 {
		out := make([]ProcessCall, 0, len(spec.ExpectedTools))
		for i, t := range spec.ExpectedTools {
			if strings.TrimSpace(t.Name) == "" {
				return nil, fmt.Errorf("process expected_tools[%d] has no name", i)
			}
			am := cmp.Or(t.ArgsMatch, spec.ArgsMatch)
			if am == "" {
				am = schemas.ProcessArgsIgnore
				if t.Args != nil {
					am = schemas.ProcessArgsSubset
				}
			}
			if am != schemas.ProcessArgsIgnore && am != schemas.ProcessArgsExact && am != schemas.ProcessArgsSubset {
				return nil, fmt.Errorf("process args_match %q is not ignore, exact or subset", am)
			}
			out = append(out, ProcessCall{Name: t.Name, Args: normalizeJSON(t.Args), ArgsKnown: t.Args != nil, ArgsMatch: am})
		}
		return out, nil
	}
	if spec.Reference == "" {
		return nil, errors.New(reasonNoProcess)
	}
	if filepath.IsAbs(spec.Reference) || strings.HasPrefix(filepath.Clean(spec.Reference), "..") {
		return nil, fmt.Errorf("process reference %q must be a path inside the task folder", spec.Reference)
	}
	if dir == "" {
		return nil, errors.New("process reference needs the task folder")
	}
	f, err := os.Open(filepath.Join(dir, spec.Reference))
	if err != nil {
		return nil, fmt.Errorf("process reference: %w", err)
	}
	defer f.Close()
	data, err := readLimited(f, maxReferenceBytes)
	if err != nil {
		return nil, fmt.Errorf("process reference: %w", err)
	}
	ref, err := ParseReference(data)
	if err != nil {
		return nil, err
	}
	am := spec.ArgsMatch
	if am == "" {
		am = schemas.ProcessArgsIgnore
	}
	for i := range ref {
		ref[i].ArgsMatch = am
		ref[i].Args = normalizeJSON(ref[i].Args)
	}
	return ref, nil
}

// agentPath is the run's tool path: the tool proxy ledger's tool calls when
// it has any (names and arguments), else the tool calls the model requested
// through the gateway (names only). An empty source means nothing was
// observed.
func agentPath(ev *schemas.Evidence) ([]ProcessCall, string) {
	if ev == nil {
		return nil, ""
	}
	var out []ProcessCall
	for _, c := range ev.ToolCalls {
		if c.IsToolCall() && len(out) < maxProcessCalls {
			out = append(out, ledgerCall(c))
		}
	}
	if len(out) > 0 {
		return out, ProcessSourceToolProxy
	}
	for _, c := range ev.RequestedTools {
		if c.Requested && c.ToolName != "" && len(out) < maxProcessCalls {
			out = append(out, ProcessCall{Name: c.ToolName})
		}
	}
	if len(out) > 0 || ev.ToolCalls != nil || ev.Requests != nil {
		return out, ProcessSourceGateway
	}
	return nil, ""
}

// ledgerCall converts one tool proxy ledger row. Arguments that are not JSON
// are kept as their text.
func ledgerCall(c schemas.ToolCall) ProcessCall {
	pc := ProcessCall{Name: c.Tool, ArgsKnown: true}
	if c.Arguments == "" {
		return pc
	}
	var v any
	if err := json.Unmarshal([]byte(c.Arguments), &v); err != nil {
		pc.Args = c.Arguments
		return pc
	}
	pc.Args = normalizeJSON(v)
	return pc
}

// callMatches compares one expected call with one agent call. When the
// agent's arguments were not observed only names are compared.
func callMatches(want, got ProcessCall, argsKnown bool) bool {
	if want.Name != got.Name {
		return false
	}
	if !argsKnown || !want.ArgsKnown {
		return true
	}
	switch want.ArgsMatch {
	case schemas.ProcessArgsExact:
		return jsonEqual(want.Args, got.Args)
	case schemas.ProcessArgsSubset:
		return argsSubset(want.Args, got.Args)
	}
	return true
}

// argsSubset reports whether every key of want is in got with an equal
// value; a non-object want must equal got.
func argsSubset(want, got any) bool {
	w, ok := want.(map[string]any)
	if !ok {
		return jsonEqual(want, got)
	}
	g, ok := got.(map[string]any)
	if !ok {
		return len(w) == 0
	}
	for k, v := range w {
		gv, ok := g[k]
		if !ok || !jsonEqual(v, gv) {
			return false
		}
	}
	return true
}

// multisetDiff pairs reference and agent calls greedily in order and
// returns the unpaired ones on each side.
func multisetDiff(ref, agent []ProcessCall, same func(want, got ProcessCall) bool) (missing, extra []ProcessCall) {
	used := make([]bool, len(agent))
	for _, want := range ref {
		found := false
		for j, got := range agent {
			if !used[j] && same(want, got) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	for j, got := range agent {
		if !used[j] {
			extra = append(extra, got)
		}
	}
	return missing, extra
}

// redundantCalls counts calls that repeat an earlier call's name and
// arguments.
func redundantCalls(calls []ProcessCall) int {
	seen := map[string]bool{}
	n := 0
	for _, c := range calls {
		b, err := json.Marshal(c.Args)
		if err != nil {
			continue
		}
		k := c.Name + "\x00" + string(b)
		if seen[k] {
			n++
		}
		seen[k] = true
	}
	return n
}

// names lists call names, sorted, for display.
func names(calls []ProcessCall) []string {
	if len(calls) == 0 {
		return nil
	}
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// normalizeJSON round-trips a value through JSON, so YAML maps, ints and
// JSON numbers compare alike.
func normalizeJSON(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// jsonEqual compares two values as canonical JSON (map keys sorted).
func jsonEqual(a, b any) bool {
	x, errA := json.Marshal(normalizeJSON(a))
	y, errB := json.Marshal(normalizeJSON(b))
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// readLimited reads at most limit bytes and fails on more.
func readLimited(f *os.File, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := buf.ReadFrom(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if n > limit {
		return nil, fmt.Errorf("file is over %d bytes", limit)
	}
	return buf.Bytes(), nil
}
