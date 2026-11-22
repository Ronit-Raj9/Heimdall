// Package heimdall is a Bifrost plugin for Heimdall, the agent benchmark that
// runs on this Bifrost fork. It reads each request's Heimdall identity (run,
// evaluation, org, arm, fault plan) from headers, injects the model faults a
// fault plan asks for inside Bifrost itself (no separate fault proxy), and
// tags the response headers and log metadata with the Heimdall ids.
package heimdall

import (
	"fmt"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// PluginName is the name Bifrost registers this plugin under.
const PluginName = "heimdall"

// Config is the plugin's configuration block.
type Config struct {
	// Enabled turns fault injection on. Tagging runs whenever the plugin is
	// loaded; faults only when Enabled is true.
	Enabled bool `json:"enabled"`
	// Plans are the named fault plans a request selects with
	// x-heimdall-fault-plan-id.
	Plans map[string]Plan `json:"plans,omitempty"`
	// AllowInlinePlans lets a request carry its own plan in
	// x-heimdall-fault-plan. Only turn this on when the callers are trusted
	// (Heimdall's own workers behind the gateway).
	AllowInlinePlans bool `json:"allow_inline_plans,omitempty"`
	// Usage exports one usage row per call to Heimdall (usage.go). Off when
	// Usage.URL is empty.
	Usage UsageConfig `json:"usage,omitempty"`
}

// Plugin implements schemas.LLMPlugin and schemas.HTTPTransportPlugin.
type Plugin struct {
	config  Config
	tracker *tracker
	usage   *UsageExporter
	sleep   func(ctx *schemas.BifrostContext, d time.Duration) bool
}

var (
	_ schemas.LLMPlugin           = (*Plugin)(nil)
	_ schemas.HTTPTransportPlugin = (*Plugin)(nil)
)

// Init validates the config and returns a ready plugin.
func Init(config Config) (*Plugin, error) {
	for id, p := range config.Plans {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("heimdall plan %q: %w", id, err)
		}
	}
	usage, err := NewUsageExporter(config.Usage, nil)
	if err != nil {
		return nil, err
	}
	return &Plugin{config: config, tracker: newTracker(), usage: usage, sleep: ctxSleep}, nil
}

// GetName returns the plugin name.
func (p *Plugin) GetName() string { return PluginName }

// Cleanup sends queued usage rows and drops the per-run call counters.
func (p *Plugin) Cleanup() error {
	p.usage.Close()
	p.tracker.mu.Lock()
	p.tracker.slots = map[string]*slot{}
	p.tracker.mu.Unlock()
	return nil
}

// HTTPTransportPreAuthHook does nothing: the plugin does no credential work.
func (p *Plugin) HTTPTransportPreAuthHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return nil, nil
}

// HTTPTransportPreHook reads the Heimdall headers onto the context.
func (p *Plugin) HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	if ctx == nil || req == nil {
		return nil, nil
	}
	if info := infoFromHeaders(req.Headers, p.config.AllowInlinePlans); info != nil {
		ctx.SetValue(ContextKeyFaultSlot, &faultSlot{})
		ctx.SetValue(ContextKeyInfo, info)
		tagContext(ctx, info, "")
	}
	return nil, nil
}

// HTTPTransportPostHook does nothing; headers are set in
// HTTPTransportResponseHeadersHook, which also covers streams.
func (p *Plugin) HTTPTransportPostHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, _ *schemas.HTTPResponse) error {
	return nil
}

// HTTPTransportResponseHeadersHook tags the response with the Heimdall ids,
// the fault applied, and Retry-After for an injected 429.
func (p *Plugin) HTTPTransportResponseHeadersHook(ctx *schemas.BifrostContext, _ *schemas.HTTPRequest, resp *schemas.HTTPResponseMetadata) error {
	if ctx == nil || resp == nil {
		return nil
	}
	info, _ := ctx.Value(ContextKeyInfo).(*RequestInfo)
	if info == nil {
		return nil
	}
	if info.RunID != "" {
		resp.SetHeader(HeaderRunID, info.RunID)
	}
	if info.EvalID != "" {
		resp.SetHeader(HeaderEvalID, info.EvalID)
	}
	if info.Arm != "" {
		resp.SetHeader(HeaderArm, info.Arm)
	}
	d, _ := ctx.Value(ContextKeyFault).(*Decision)
	if slot, ok := ctx.Value(ContextKeyFaultSlot).(*faultSlot); ok && slot != nil && (d == nil || !d.Faulted()) {
		d = slot.get()
	}
	if d != nil && d.Faulted() {
		resp.SetHeader(HeaderFaultApplied, d.Mode)
		if d.RetryAfterS > 0 {
			resp.SetHeader("Retry-After", strconv.Itoa(d.RetryAfterS))
		}
	}
	return nil
}

// HTTPTransportStreamChunkHook passes chunks through.
func (p *Plugin) HTTPTransportStreamChunkHook(_ *schemas.BifrostContext, _ *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

// PreRequestHook does no routing.
func (p *Plugin) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}

// plan returns the fault plan this request selects, if any.
func (p *Plugin) plan(info *RequestInfo) (Plan, bool) {
	if info.InlinePlan != nil {
		return *info.InlinePlan, true
	}
	if info.FaultPlanID != "" {
		pl, ok := p.config.Plans[info.FaultPlanID]
		return pl, ok
	}
	return Plan{}, false
}

// PreLLMHook tags the request and applies its fault plan. It runs once per
// attempt, so a fallback attempt counts as its own call: an outage on the
// primary lets Bifrost fallbacks take over when the plan names its model.
func (p *Plugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	info := p.requestInfo(ctx)
	if info == nil {
		return req, nil, nil
	}
	tagContext(ctx, info, "")
	if !p.config.Enabled || req == nil {
		return req, nil, nil
	}
	pl, ok := p.plan(info)
	if !ok {
		return req, nil, nil
	}
	provider, model, _ := req.GetRequestFields()
	key := info.RunID + "|" + info.FaultPlanID
	d := p.tracker.decide(key, pl, model)
	if !d.Faulted() {
		return req, nil, nil
	}
	ctx.SetValue(ContextKeyFault, &d)
	if slot, ok := ctx.Value(ContextKeyFaultSlot).(*faultSlot); ok && slot != nil {
		slot.set(&d)
	}
	tagContext(ctx, info, d.Mode)
	if d.Delay > 0 && !p.sleep(ctx, d.Delay) {
		return req, nil, nil // caller went away; let core report the cancel
	}
	if d.Pass {
		return req, nil, nil
	}
	status := d.Status
	errType := "heimdall_fault"
	code := d.Mode
	be := &schemas.BifrostError{
		Type:       &errType,
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: d.Message, Type: &errType, Code: &code},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RequestType:            req.RequestType,
			Provider:               provider,
			OriginalModelRequested: model,
		},
	}
	if d.RetryAfterS > 0 {
		be.ExtraFields.RetryAfter = int64(d.RetryAfterS) * 1000
	}
	return req, &schemas.LLMPluginShortCircuit{Error: be}, nil
}

// PostLLMHook makes sure the ids are on the log dimensions after the call.
func (p *Plugin) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if info, _ := ctx.Value(ContextKeyInfo).(*RequestInfo); info != nil {
		fault := ""
		d, _ := ctx.Value(ContextKeyFault).(*Decision)
		if d != nil {
			fault = d.Mode
		}
		tagContext(ctx, info, fault)
		if rec, ok := usageRecord(info, d, resp, bifrostErr, time.Now()); ok {
			p.usage.Add(rec)
		}
	}
	return resp, bifrostErr, nil
}

func ctxSleep(ctx *schemas.BifrostContext, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
