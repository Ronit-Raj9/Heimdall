package heimdall

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// Request headers Heimdall sends with every model call it routes through
// Bifrost. All are optional; a request without them passes through untouched.
const (
	HeaderRunID       = "x-heimdall-run-id"
	HeaderEvalID      = "x-heimdall-eval-id"
	HeaderOrg         = "x-heimdall-org"
	HeaderArm         = "x-heimdall-arm"
	HeaderFaultPlanID = "x-heimdall-fault-plan-id"
	// HeaderFaultPlan carries an inline fault plan as JSON or base64url JSON.
	// It is honoured only when the plugin config sets AllowInlinePlans.
	HeaderFaultPlan = "x-heimdall-fault-plan"

	// Response headers the plugin adds so callers can see what happened.
	HeaderFaultApplied = "x-heimdall-fault-applied"
)

// contextKey is the private key type for values this plugin stores on the
// Bifrost context, so no other plugin can collide with them.
type contextKey string

const (
	ContextKeyInfo  contextKey = "heimdall-info"  // *RequestInfo
	ContextKeyFault contextKey = "heimdall-fault" // string: the fault mode applied on this request, if any
)

// RequestInfo is the Heimdall identity of one request.
type RequestInfo struct {
	RunID       string `json:"run_id,omitempty"`
	EvalID      string `json:"eval_id,omitempty"`
	Org         string `json:"org,omitempty"`
	Arm         string `json:"arm,omitempty"`
	FaultPlanID string `json:"fault_plan_id,omitempty"`
	InlinePlan  *Plan  `json:"-"`
}

// Empty reports whether the request carries no Heimdall identity at all.
func (i *RequestInfo) Empty() bool {
	return i == nil || (i.RunID == "" && i.EvalID == "" && i.Org == "" && i.Arm == "" && i.FaultPlanID == "" && i.InlinePlan == nil)
}

// Tags returns the identity as flat log dimensions, prefixed heimdall_.
func (i *RequestInfo) Tags() map[string]string {
	out := map[string]string{}
	if i == nil {
		return out
	}
	add := func(k, v string) {
		if v != "" {
			out["heimdall_"+k] = v
		}
	}
	add("run_id", i.RunID)
	add("eval_id", i.EvalID)
	add("org", i.Org)
	add("arm", i.Arm)
	add("fault_plan_id", i.FaultPlanID)
	return out
}

// infoFromHeaders builds the identity from a header map. Lookups ignore case.
func infoFromHeaders(h map[string]string, allowInline bool) *RequestInfo {
	if len(h) == 0 {
		return nil
	}
	get := func(k string) string {
		if v, ok := h[k]; ok {
			return strings.TrimSpace(v)
		}
		for hk, v := range h {
			if strings.EqualFold(hk, k) {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	info := &RequestInfo{
		RunID:       get(HeaderRunID),
		EvalID:      get(HeaderEvalID),
		Org:         get(HeaderOrg),
		Arm:         get(HeaderArm),
		FaultPlanID: get(HeaderFaultPlanID),
	}
	if allowInline {
		if raw := get(HeaderFaultPlan); raw != "" {
			if p, err := parseInlinePlan(raw); err == nil {
				info.InlinePlan = p
			}
		}
	}
	if info.Empty() {
		return nil
	}
	return info
}

func parseInlinePlan(raw string) (*Plan, error) {
	data := []byte(raw)
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		dec, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(raw, "="))
		if err != nil {
			return nil, err
		}
		data = dec
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// requestInfo returns the identity for this request: first a value already on
// the context (set by HTTPTransportPreHook or an in-process caller), then the
// transport's lowercased request header map.
func (p *Plugin) requestInfo(ctx *schemas.BifrostContext) *RequestInfo {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(ContextKeyInfo).(*RequestInfo); ok && v != nil {
		return v
	}
	if h, ok := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string); ok {
		if info := infoFromHeaders(h, p.config.AllowInlinePlans); info != nil {
			ctx.SetValue(ContextKeyInfo, info)
			return info
		}
	}
	return nil
}

// tagContext merges the Heimdall ids into the request's log dimensions, which
// the Bifrost logging plugin stores as log metadata.
func tagContext(ctx *schemas.BifrostContext, info *RequestInfo, fault string) {
	if ctx == nil || info == nil {
		return
	}
	dims, _ := ctx.Value(schemas.BifrostContextKeyDimensions).(map[string]string)
	merged := make(map[string]string, len(dims)+6)
	for k, v := range dims {
		merged[k] = v
	}
	for k, v := range info.Tags() {
		merged[k] = v
	}
	if fault != "" {
		merged["heimdall_fault"] = fault
	}
	ctx.SetValue(schemas.BifrostContextKeyDimensions, merged)
}
