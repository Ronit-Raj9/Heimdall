package canary

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Metric ids the canary compares. Success, cost and tokens are the run
// metrics of the same name; latency is the median latency of the run's
// model requests from the gateway rows (wall time mixes in the agent's own
// work, so a slower route shows up here first).
const (
	MetricSuccess = "verified_success"
	MetricCost    = "cost_usd"
	MetricTokens  = "tokens"
	MetricLatency = "latency_ms"
	// MetricFormatErrors is compared only in the equivalence report: model
	// requests refused as malformed (400 or 422) plus tool calls with no
	// name, per run.
	MetricFormatErrors = "format_errors"
)

// Metrics lists the drift metrics in report order.
var Metrics = []string{MetricSuccess, MetricCost, MetricTokens, MetricLatency}

// HigherIsBetter reports a metric's direction: success up is good; cost,
// tokens, latency and format errors up are bad.
func HigherIsBetter(metric string) bool { return metric == MetricSuccess }

// gateModelFidelity is the gate that flags a served model other than the
// requested one. For the canary that is the signal it watches, not a
// harness fault, so it never makes a run invalid here.
const gateModelFidelity = "model_fidelity"

// gateEgress fired in any run means the harness leaked: always invalid.
const gateEgress = "egress"

// RunObs is what the canary reads from one counted sealed run.
type RunObs struct {
	RunID     string   `json:"run_id"`
	Task      string   `json:"task"`
	Arm       string   `json:"arm"`
	Condition string   `json:"condition"`
	Trial     int      `json:"trial"`
	Outcome   string   `json:"outcome"`
	Success   *float64 `json:"success,omitempty"` // verified_success; nil when it does not score
	CostUSD   *float64 `json:"cost_usd,omitempty"`
	Tokens    *float64 `json:"tokens,omitempty"`
	LatencyMS *float64 `json:"latency_ms,omitempty"` // median latency of answered model requests
	// FirstPromptTokens is the input token count of the run's first model
	// request. Every run of a task sends the same first prompt, so a change
	// at an identical prompt is a tokenizer or provider-side prompt change.
	FirstPromptTokens *int64   `json:"first_prompt_tokens,omitempty"`
	Requested         []string `json:"requested,omitempty"`           // models asked for
	Served            []string `json:"served,omitempty"`              // models that answered
	Providers         []string `json:"providers,omitempty"`           // providers that answered
	Fingerprints      []string `json:"system_fingerprints,omitempty"` // system_fingerprint values in raw rows
	ModelRequests     int      `json:"model_requests"`
	ModelErrors       int      `json:"model_errors"`  // model requests that ended in an error
	FormatErrors      int      `json:"format_errors"` // see MetricFormatErrors
	InfraError        bool     `json:"infra_error,omitempty"`
	Problems          []string `json:"problems,omitempty"`      // seal or slot problems the store found
	InvalidGates      []string `json:"invalid_gates,omitempty"` // instrument gates that fired, model fidelity excepted
	Egress            bool     `json:"egress,omitempty"`
	// FaultFired is whether the run's probe fault fired, from its receipt
	// (armed and then fired); nil for a run with no armed probe.
	FaultFired *bool `json:"fault_fired,omitempty"`
}

// Value returns the run's value of a metric.
func (o RunObs) Value(metric string) *float64 {
	switch metric {
	case MetricSuccess:
		return o.Success
	case MetricCost:
		return o.CostUSD
	case MetricTokens:
		return o.Tokens
	case MetricLatency:
		return o.LatencyMS
	case MetricFormatErrors:
		v := float64(o.FormatErrors)
		return &v
	}
	return nil
}

// Observe reads one sealed run and its gateway rows (nil when the run has
// no gateway log). rename maps an injected condition back to the watched
// one. It is pure.
func Observe(sr schemas.SealedRun, log *schemas.RequestLog, rename map[string]string) RunObs {
	o := RunObs{
		RunID: sr.Run.RunID, Task: sr.Slot.Task, Arm: sr.Slot.Arm, Condition: sr.Slot.Condition, Trial: sr.Slot.Trial,
		Outcome: string(sr.Run.Outcome), Problems: slices.Clone(sr.Problems),
	}
	if to, ok := rename[o.Condition]; ok {
		o.Condition = to
	}
	o.InfraError = sr.Run.Outcome == schemas.OutcomeInfraError || sr.Run.Outcome == schemas.OutcomeAbortedInfra
	o.Success = metricOf(sr, MetricSuccess)
	o.CostUSD = metricOf(sr, MetricCost)
	o.Tokens = metricOf(sr, MetricTokens)
	for _, g := range sr.Gates {
		if !g.Fired || g.Effect != schemas.GateInvalidate {
			continue
		}
		switch g.Gate {
		case gateEgress:
			o.Egress = true
		case gateModelFidelity:
		default:
			if !slices.Contains(o.InvalidGates, g.Gate) {
				o.InvalidGates = append(o.InvalidGates, g.Gate)
			}
		}
	}
	for _, rc := range sr.Receipts {
		if rc.Steps[schemas.StepArmed].State != schemas.StepYes {
			continue
		}
		fired := rc.Steps[schemas.StepFired].State == schemas.StepYes
		o.FaultFired = &fired
	}
	if log == nil {
		return o
	}
	rows := make([]schemas.GatewayRequest, 0, len(log.Requests))
	for _, r := range log.Requests {
		if r.Kind == "message" || r.Kind == "" {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Timestamp.Before(rows[j].Timestamp) })
	var lat []float64
	for _, r := range rows {
		o.ModelRequests++
		o.Requested = addUnique(o.Requested, r.ModelRequested)
		failed := r.Status == "error" || r.ErrorStatus != 0
		if failed {
			o.ModelErrors++
		}
		if r.ErrorStatus == 400 || r.ErrorStatus == 422 {
			o.FormatErrors++
		}
		for _, tc := range r.ToolCalls {
			if strings.TrimSpace(tc.Name) == "" {
				o.FormatErrors++
			}
		}
		if failed {
			continue
		}
		o.Served = addUnique(o.Served, r.ModelServed)
		o.Providers = addUnique(o.Providers, r.Provider)
		if fp := systemFingerprint(r.Raw); fp != "" {
			o.Fingerprints = addUnique(o.Fingerprints, fp)
		}
		if r.LatencyMS > 0 {
			lat = append(lat, r.LatencyMS)
		}
		if o.FirstPromptTokens == nil && r.Tokens != nil {
			in := r.Tokens.Fresh + r.Tokens.CacheRead + r.Tokens.CacheWrite
			o.FirstPromptTokens = &in
		}
	}
	if len(lat) > 0 {
		m := stats.Median(lat)
		o.LatencyMS = &m
	}
	slices.Sort(o.Requested)
	slices.Sort(o.Served)
	slices.Sort(o.Providers)
	slices.Sort(o.Fingerprints)
	return o
}

// metricOf returns an observed, finite run metric.
func metricOf(sr schemas.SealedRun, id string) *float64 {
	v, ok := sr.Metrics[id]
	if !ok {
		return nil
	}
	f, ok := v.Float()
	if !ok {
		return nil
	}
	return &f
}

// systemFingerprint finds a "system_fingerprint" string in a raw gateway
// row (OpenAI-style responses carry one), at most three objects deep.
func systemFingerprint(raw json.RawMessage) string {
	if len(raw) == 0 || !strings.Contains(string(raw), "system_fingerprint") {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	var find func(v any, depth int) string
	find = func(v any, depth int) string {
		m, ok := v.(map[string]any)
		if !ok || depth > 3 {
			return ""
		}
		if s, ok := m["system_fingerprint"].(string); ok && s != "" {
			return s
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s := find(m[k], depth+1); s != "" {
				return s
			}
		}
		return ""
	}
	return find(v, 0)
}

// addUnique appends s when it is not empty and not present.
func addUnique(xs []string, s string) []string {
	if s == "" || slices.Contains(xs, s) {
		return xs
	}
	return append(xs, s)
}

// ClusterRef is one failure cluster (bench/clusters) of an experiment.
type ClusterRef struct {
	Signature string   `json:"signature"`
	Title     string   `json:"title"`
	Count     int      `json:"count"`
	Tasks     []string `json:"tasks"`
}

// Execution is one canary experiment as the canary reads it back from the
// sealed run folder.
type Execution struct {
	ExpID        string            `json:"exp_id"`
	ManifestHash string            `json:"manifest_hash"`
	Fingerprint  string            `json:"fingerprint"` // frozen-config fingerprint (Fingerprint)
	CreatedAt    time.Time         `json:"created_at"`
	Smoke        bool              `json:"smoke"`
	Slots        int               `json:"slots"` // counted slots planned
	Arms         map[string]string `json:"arms"`  // arm id -> label
	Runs         []RunObs          `json:"runs"`
	Clusters     []ClusterRef      `json:"clusters"`
	Problems     []string          `json:"problems,omitempty"` // experiment-level problems (seal chain, missing manifest)
	Injected     string            `json:"injected,omitempty"` // probe forced on a smoke canary look (test hook)
}

// Served summarizes, per arm, which models and providers answered.
type Served struct {
	Arm          string   `json:"arm"`
	Label        string   `json:"label"`
	Requested    []string `json:"requested"`
	Served       []string `json:"served"`
	Providers    []string `json:"providers"`
	Fingerprints []string `json:"system_fingerprints,omitempty"`
	Requests     int      `json:"requests"`
}

// ServedSummary is the served-model fingerprint of an execution, one entry
// per arm in arm order.
func ServedSummary(x *Execution) []Served {
	byArm := map[string]*Served{}
	for _, r := range x.Runs {
		s := byArm[r.Arm]
		if s == nil {
			s = &Served{Arm: r.Arm, Label: x.Arms[r.Arm], Requested: []string{}, Served: []string{}, Providers: []string{}}
			byArm[r.Arm] = s
		}
		for _, m := range r.Requested {
			s.Requested = addUnique(s.Requested, m)
		}
		for _, m := range r.Served {
			s.Served = addUnique(s.Served, m)
		}
		for _, p := range r.Providers {
			s.Providers = addUnique(s.Providers, p)
		}
		for _, f := range r.Fingerprints {
			s.Fingerprints = addUnique(s.Fingerprints, f)
		}
		s.Requests += r.ModelRequests
	}
	out := make([]Served, 0, len(byArm))
	for _, s := range byArm {
		slices.Sort(s.Requested)
		slices.Sort(s.Served)
		slices.Sort(s.Providers)
		slices.Sort(s.Fingerprints)
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Served) int { return strings.Compare(a.Arm, b.Arm) })
	return out
}
