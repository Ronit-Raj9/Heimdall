package handlers

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Through Bifrost (lane C09): the run page panel that shows what Bifrost did
// for one run's model calls. Facts come from the sealed run's evidence.json
// (Bifrost log rows and the fault proxy ledger) and, when present, the
// per-request usage records the heimdall Bifrost plugin sent for the run
// (FileGatewayUsage, lane C03). Nothing here calls Bifrost live.

// FileGatewayUsage is the run file holding the heimdall plugin's usage
// records (one JSON array). It is optional: runs without the plugin have
// none and the panel uses the Bifrost log rows only.
const FileGatewayUsage = "gateway_usage.json"

// PluginUsage is one per-request usage record of the heimdall Bifrost plugin.
// Fields the plugin did not send stay zero or empty.
type PluginUsage struct {
	RequestID    string   `json:"request_id,omitempty"`
	RunID        string   `json:"run_id,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Model        string   `json:"model,omitempty"`
	InputTokens  int64    `json:"input_tokens,omitempty"`
	OutputTokens int64    `json:"output_tokens,omitempty"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
	LatencyMS    float64  `json:"latency_ms,omitempty"`
	CacheHit     bool     `json:"cache_hit,omitempty"`
	FallbackUsed bool     `json:"fallback_used,omitempty"`
	Fault        string   `json:"fault,omitempty"` // the fault the plugin applied, empty when none
}

// BifrostCall is one model call as Bifrost saw it.
type BifrostCall struct {
	ID             string          `json:"id"`
	At             time.Time       `json:"at"`
	Provider       string          `json:"provider"`
	ModelRequested string          `json:"model_requested"`
	ModelServed    string          `json:"model_served"`
	Status         string          `json:"status"`
	ErrorStatus    int             `json:"error_status,omitempty"`
	FallbackIndex  int             `json:"fallback_index"`
	CacheHit       bool            `json:"cache_hit"`
	Fault          string          `json:"fault,omitempty"` // fault applied to this call, empty when none
	Tokens         *schemas.Tokens `json:"tokens,omitempty"`
	CostUSD        *float64        `json:"cost_usd,omitempty"`
	LatencyMS      float64         `json:"latency_ms"`
}

// RunBifrost is GET /runs/{id}/bifrost.
type RunBifrost struct {
	RunID string `json:"run_id"`
	// Available is false when the run has no Bifrost log rows and no plugin
	// usage records (the run did not go through Bifrost, or is not sealed).
	Available  bool           `json:"available"`
	Complete   bool           `json:"complete"` // Bifrost returned every row of the run
	Source     string         `json:"source"`   // "bifrost_logs", "plugin_usage" or "both"
	Providers  []string       `json:"providers"`
	Models     []string       `json:"models"` // served models
	Calls      int            `json:"calls"`
	Fallbacks  int            `json:"fallbacks"`  // calls served by a fallback (fallback_index > 0)
	CacheHits  int            `json:"cache_hits"` // calls answered from Bifrost's cache
	Faults     int            `json:"faults"`     // calls with a fault applied
	Errors     int            `json:"errors"`
	Tokens     schemas.Tokens `json:"tokens"`
	CostUSD    *float64       `json:"cost_usd,omitempty"` // nil when no call carried a cost
	CostKnown  int            `json:"cost_known"`         // calls whose cost Bifrost reported
	LatencyMS  float64        `json:"latency_ms"`         // sum over calls
	AvgLatency float64        `json:"avg_latency_ms"`
	CallList   []BifrostCall  `json:"call_list"`
}

// RunBifrostHandler serves the Through Bifrost panel.
type RunBifrostHandler struct {
	mode           schemas.Mode
	index          ReadIndex
	heldOutVisible bool
}

// NewRunBifrostHandler creates the handler. index may be nil.
func NewRunBifrostHandler(mode schemas.Mode, index ReadIndex) *RunBifrostHandler {
	return &RunBifrostHandler{mode: mode, index: index}
}

// WithHeldOutVisible sets whether a held-out task's run is served.
func (h *RunBifrostHandler) WithHeldOutVisible(visible bool) *RunBifrostHandler {
	h.heldOutVisible = visible
	return h
}

// RegisterRoutes registers GET /runs/{id}/bifrost.
func (h *RunBifrostHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/runs/{id}/bifrost", lib.ChainMiddlewares(h.getRunBifrost, middlewares...))
}

func (h *RunBifrostHandler) getRunBifrost(ctx *fasthttp.RequestCtx) {
	runID, ok := idParam(ctx, h.mode, "id", "run")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	rv, found := h.index.Run(runID)
	if !found {
		sendNotFound(ctx, h.mode, "run", runID)
		return
	}
	if refuseHeldOutRun(ctx, h.mode, h.index, h.heldOutVisible, rv) {
		return
	}
	out, err := LoadRunBifrost(h.index, runID)
	if err != nil {
		sendDepError(ctx, h.mode, "bifrost facts of run "+runID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// LoadRunBifrost reads a run's evidence and plugin usage and builds its
// Through Bifrost facts.
func LoadRunBifrost(index ReadIndex, runID string) (*RunBifrost, error) {
	var ev struct {
		Requests *schemas.RequestLog   `json:"requests"`
		Ledger   []schemas.LedgerEntry `json:"ledger"`
	}
	data, found, err := readInternal(index, runID, FileEvidence) // read for its gateway rows, never served raw
	if err != nil {
		return nil, err
	}
	if found {
		if err := json.Unmarshal(data, &ev); err != nil {
			return nil, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
		}
	}
	var usage []PluginUsage
	data, found, err = readInternal(index, runID, FileGatewayUsage)
	if err != nil {
		return nil, err
	}
	if found {
		if err := json.Unmarshal(data, &usage); err != nil {
			return nil, fmt.Errorf("parse %s of run %s: %w", FileGatewayUsage, runID, err)
		}
	}
	if len(usage) == 0 { // the heimdall plugin's live usage rows (gatewayusage.go, lane C03)
		if run, ok := GatewayUsageForRun(runID); ok {
			usage = pluginUsageOf(run.Records)
		}
	}
	return BuildRunBifrost(runID, ev.Requests, ev.Ledger, usage), nil
}

// pluginUsageOf maps the ingest route's records to the panel's usage rows.
func pluginUsageOf(recs []GatewayUsageRecord) []PluginUsage {
	out := make([]PluginUsage, 0, len(recs))
	for _, r := range recs {
		cost := r.CostUSD
		out = append(out, PluginUsage{RequestID: r.RequestID, RunID: r.RunID, Provider: r.Provider, Model: r.Model,
			InputTokens: int64(r.InputTokens), OutputTokens: int64(r.OutputTokens), CostUSD: &cost,
			LatencyMS: float64(r.LatencyMS), CacheHit: r.CacheHit, FallbackUsed: r.FallbackUsed, Fault: r.FaultApplied})
	}
	return out
}

// BuildRunBifrost folds Bifrost rows, the fault ledger and plugin usage
// into one view. Only message rows are model calls. The n-th message row
// pairs with the n-th counted ledger call for its fault; a plugin record
// pairs with a row by request id, and adds a call when no row matches.
func BuildRunBifrost(runID string, log *schemas.RequestLog, ledger []schemas.LedgerEntry, usage []PluginUsage) *RunBifrost {
	out := &RunBifrost{RunID: runID, Providers: []string{}, Models: []string{}, CallList: []BifrostCall{}}
	faultByCall := map[int]string{}
	for _, e := range ledger {
		if e.Call > 0 && (e.Action == "inject" || e.Action == "reject") {
			faultByCall[e.Call] = fmt.Sprintf("%s %d", e.Action, e.Status)
		}
	}
	byID := map[string]int{}
	if log != nil {
		out.Complete = log.Complete
		n := 0
		for _, r := range log.Requests {
			if r.Kind != "message" {
				continue
			}
			n++
			c := BifrostCall{ID: r.ID, At: r.Timestamp, Provider: r.Provider, ModelRequested: r.ModelRequested,
				ModelServed: r.ModelServed, Status: r.Status, ErrorStatus: r.ErrorStatus, FallbackIndex: r.FallbackIndex,
				CacheHit: r.CacheHit, Fault: faultByCall[n], Tokens: r.Tokens, CostUSD: r.BifrostUSD, LatencyMS: r.LatencyMS}
			byID[r.ID] = len(out.CallList)
			out.CallList = append(out.CallList, c)
		}
	}
	fromLogs := len(out.CallList) > 0
	for _, u := range usage {
		i, ok := byID[u.RequestID]
		if !ok || u.RequestID == "" {
			out.CallList = append(out.CallList, BifrostCall{ID: u.RequestID, Provider: u.Provider, ModelRequested: u.Model,
				ModelServed: u.Model, Status: "success", CacheHit: u.CacheHit, Fault: u.Fault,
				Tokens: &schemas.Tokens{Fresh: u.InputTokens, Out: u.OutputTokens}, CostUSD: u.CostUSD, LatencyMS: u.LatencyMS})
			if u.FallbackUsed {
				out.CallList[len(out.CallList)-1].FallbackIndex = 1
			}
			continue
		}
		c := &out.CallList[i] // the plugin fills what the log row lacks
		if u.Fault != "" {    // plugin rows win where they say something
			c.Fault = u.Fault
		}
		c.CacheHit = c.CacheHit || u.CacheHit
		if u.CostUSD != nil {
			c.CostUSD = u.CostUSD
		}
		if c.Tokens == nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
			c.Tokens = &schemas.Tokens{Fresh: u.InputTokens, Out: u.OutputTokens}
		}
		if u.FallbackUsed && c.FallbackIndex == 0 {
			c.FallbackIndex = 1
		}
	}
	switch {
	case fromLogs && len(usage) > 0:
		out.Source = "both"
	case fromLogs:
		out.Source = "bifrost_logs"
	case len(usage) > 0:
		out.Source, out.Complete = "plugin_usage", true
	}
	out.Available = len(out.CallList) > 0
	var cost float64
	for _, c := range out.CallList {
		out.Calls++
		if c.Provider != "" && !slices.Contains(out.Providers, c.Provider) {
			out.Providers = append(out.Providers, c.Provider)
		}
		if c.ModelServed != "" && !slices.Contains(out.Models, c.ModelServed) {
			out.Models = append(out.Models, c.ModelServed)
		}
		if c.FallbackIndex > 0 {
			out.Fallbacks++
		}
		if c.CacheHit {
			out.CacheHits++
		}
		if c.Fault != "" {
			out.Faults++
		}
		if strings.EqualFold(c.Status, "error") {
			out.Errors++
		}
		if c.Tokens != nil {
			out.Tokens.Fresh += c.Tokens.Fresh
			out.Tokens.CacheRead += c.Tokens.CacheRead
			out.Tokens.CacheWrite += c.Tokens.CacheWrite
			out.Tokens.Out += c.Tokens.Out
		}
		if c.CostUSD != nil {
			cost += *c.CostUSD
			out.CostKnown++
		}
		out.LatencyMS += c.LatencyMS
	}
	if out.CostKnown > 0 {
		out.CostUSD = &cost
	}
	if out.Calls > 0 {
		out.AvgLatency = out.LatencyMS / float64(out.Calls)
	}
	return out
}
