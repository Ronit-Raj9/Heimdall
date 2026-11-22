package bifrost

// Plugin fault mode (lane C04). With HEIMDALL_FAULT_MODE=plugin the bench
// hands model faults to the heimdall plugin running inside Bifrost
// (plugins/heimdall) instead of the separate fault proxy: the plan travels
// with each model request as x-heimdall-* headers, and the plugin's PreHook
// injects the error, 429, delay or flaky failure itself. Plans the plugin
// cannot run (scripted, replay, stream cut, budget squeeze, debug holds) and
// every ledger read still go to the fault proxy, which stays the fallback.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Fault modes for HEIMDALL_FAULT_MODE.
const (
	FaultModeProxy  = "proxy"  // default: the separate fault proxy behind Bifrost
	FaultModePlugin = "plugin" // the heimdall Bifrost plugin, proxy as fallback
)

// FaultModeEnv names the config flag that picks the fault mode.
const FaultModeEnv = "HEIMDALL_FAULT_MODE"

// Headers the heimdall plugin reads. Names are shared with plugins/heimdall
// (lane C01); keep them in step.
const (
	HeaderRunID        = "x-heimdall-run-id"
	HeaderEvaluationID = "x-heimdall-eval-id"
	HeaderOrg          = "x-heimdall-org"
	HeaderArm          = "x-heimdall-arm"
	HeaderFaultPlanID  = "x-heimdall-fault-plan-id"
	// HeaderFaultPlan carries the plan itself: base64url (no padding) of the
	// PluginPlan JSON.
	HeaderFaultPlan = "x-heimdall-fault-plan"
)

// PluginModes lists the plan modes the heimdall plugin runs. Every other
// mode falls back to the fault proxy.
var PluginModes = []string{faultproxy.ModeNone, faultproxy.ModePersistent, faultproxy.ModeOnce, faultproxy.ModeSlow, faultproxy.ModeFlaky}

// Plugin plan modes (plugins/heimdall faults.go). ToPluginPlan maps the
// bench's proxy modes onto them.
const (
	PluginModeNone      = "none"
	PluginModeError     = "error"
	PluginModeRateLimit = "rate_limit"
	PluginModeLatency   = "latency"
	PluginModeFlaky     = "flaky"
)

// PluginPlan is the fault plan as the heimdall plugin reads it (its Plan
// type). ID is not part of the JSON: it rides in HeaderFaultPlanID.
type PluginPlan struct {
	ID          string  `json:"-"`
	Mode        string  `json:"mode"`
	Status      int     `json:"status,omitempty"`
	RetryAfterS int     `json:"retry_after_s,omitempty"`
	DelayMS     int     `json:"delay_ms,omitempty"`
	FailProb    float64 `json:"fail_prob,omitempty"`
	Seed        string  `json:"seed,omitempty"` // flaky coin seed (the run nonce)
	FromCall    int     `json:"from_call,omitempty"`
	Calls       int     `json:"calls,omitempty"`
	UntilS      int     `json:"until_s,omitempty"`
	Model       string  `json:"model,omitempty"`
	Message     string  `json:"message,omitempty"`
}

// PluginCanRun reports whether the heimdall plugin can run p by itself.
func PluginCanRun(p schemas.FaultPlan) bool {
	mode := p.Mode
	if mode == "" {
		mode = faultproxy.ModeNone
	}
	return slices.Contains(PluginModes, mode) && !p.Scripted && p.Replay == nil && p.Hold == nil
}

// RunTags are the Heimdall ids tagged on every request of a slot.
type RunTags struct {
	EvaluationID string
	Org          string
	Arm          string
}

// PluginFaults implements schemas.FaultController for the plugin mode. It
// keeps the current plan of each slot and turns it into request headers;
// plans the plugin cannot run go to the proxy controller.
type PluginFaults struct {
	proxy schemas.FaultController // fallback; may be nil when there is no proxy

	mu    sync.Mutex
	slots map[string]slotPlan
}

type slotPlan struct {
	plan    schemas.FaultPlan
	viaPlug bool
	tags    RunTags
}

var _ schemas.FaultController = (*PluginFaults)(nil)

// NewPluginFaults returns a plugin-mode controller with proxy as fallback.
func NewPluginFaults(proxy schemas.FaultController) *PluginFaults {
	return &PluginFaults{proxy: proxy, slots: map[string]slotPlan{}}
}

// SelectFaultController returns the controller for mode: the proxy for ""
// or "proxy", a PluginFaults over the proxy for "plugin".
func SelectFaultController(mode string, proxy schemas.FaultController) (schemas.FaultController, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", FaultModeProxy:
		return proxy, nil
	case FaultModePlugin:
		return NewPluginFaults(proxy), nil
	default:
		return nil, fmt.Errorf("%s=%q: use %q or %q", FaultModeEnv, mode, FaultModeProxy, FaultModePlugin)
	}
}

// FaultControllerFromEnv is SelectFaultController with the mode read from
// HEIMDALL_FAULT_MODE.
func FaultControllerFromEnv(proxy schemas.FaultController) (schemas.FaultController, error) {
	return SelectFaultController(os.Getenv(FaultModeEnv), proxy)
}

// SetPlan binds plan to slot. A plan the plugin runs is kept here and the
// proxy slot is set to a clean pass-through plan (so its ledger still
// records the run); any other plan goes to the proxy unchanged.
func (c *PluginFaults) SetPlan(ctx context.Context, slot string, plan schemas.FaultPlan) error {
	if err := faultproxy.ValidatePlan(plan); err != nil {
		return err
	}
	via := PluginCanRun(plan)
	if c.proxy != nil {
		forward := plan
		if via {
			forward = schemas.FaultPlan{Mode: faultproxy.ModeNone, RunID: plan.RunID}
		}
		if err := c.proxy.SetPlan(ctx, slot, forward); err != nil {
			return err
		}
	} else if !via {
		return fmt.Errorf("fault mode %q needs the fault proxy, and none is configured", plan.Mode)
	}
	c.mu.Lock()
	prev := c.slots[slot]
	c.slots[slot] = slotPlan{plan: plan, viaPlug: via, tags: prev.tags}
	c.mu.Unlock()
	return nil
}

// ClearPlan drops slot's plan here and on the proxy.
func (c *PluginFaults) ClearPlan(ctx context.Context, slot string) error {
	c.mu.Lock()
	delete(c.slots, slot)
	c.mu.Unlock()
	if c.proxy != nil {
		return c.proxy.ClearPlan(ctx, slot)
	}
	return nil
}

// Ledger reads the proxy's ledger; the plugin's own records come through
// the usage export (lane C03).
func (c *PluginFaults) Ledger(ctx context.Context, runID string) ([]schemas.LedgerEntry, error) {
	if c.proxy == nil {
		return nil, nil
	}
	return c.proxy.Ledger(ctx, runID)
}

// Tag sets the evaluation, org and arm sent with slot's requests.
func (c *PluginFaults) Tag(slot string, tags RunTags) {
	c.mu.Lock()
	sp := c.slots[slot]
	sp.tags = tags
	c.slots[slot] = sp
	c.mu.Unlock()
}

// Headers returns the x-heimdall-* headers for a model request on slot:
// the run tags always, and the plan when the plugin runs it. It is nil for
// a slot with no plan.
func (c *PluginFaults) Headers(slot string) http.Header {
	c.mu.Lock()
	sp, ok := c.slots[slot]
	c.mu.Unlock()
	if !ok {
		return nil
	}
	h := http.Header{}
	set := func(k, v string) {
		if v != "" {
			h.Set(k, v)
		}
	}
	set(HeaderRunID, sp.plan.RunID)
	set(HeaderEvaluationID, sp.tags.EvaluationID)
	set(HeaderOrg, sp.tags.Org)
	set(HeaderArm, sp.tags.Arm)
	if sp.viaPlug {
		pp := ToPluginPlan(slot, sp.plan)
		set(HeaderFaultPlanID, pp.ID)
		set(HeaderFaultPlan, EncodePluginPlan(pp))
	}
	return h
}

// HeaderLines is Headers as "Name: value" lines, the form agent CLIs take
// for extra request headers (ANTHROPIC_CUSTOM_HEADERS and the like).
func (c *PluginFaults) HeaderLines(slot string) string {
	h := c.Headers(slot)
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\n", strings.ToLower(k), h.Get(k))
	}
	return b.String()
}

// Transport wraps base so every request it sends carries slot's headers.
// Bench-side model calls through Bifrost (built-in agents, probes) use it.
func (c *PluginFaults) Transport(slot string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		h := c.Headers(slot)
		if len(h) == 0 {
			return base.RoundTrip(r)
		}
		r = r.Clone(r.Context())
		for k, vs := range h {
			r.Header[k] = vs
		}
		return base.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ToPluginPlan maps a bench plan to the plugin's plan:
//
//   - persistent: rate_limit when its status is 429, else error, from
//     from_call for calls calls or until_s seconds;
//   - once: error on call only;
//   - slow: latency of delay_ms plus first_byte_delay_ms (the plugin holds
//     the whole call, it has no separate first-byte hold);
//   - flaky: flaky with the run nonce as seed;
//   - none: none (tags only).
//
// Its id is stable for the same slot, run and mode, so plugin logs join
// back to the run.
func ToPluginPlan(slot string, p schemas.FaultPlan) PluginPlan {
	mode := p.Mode
	if mode == "" {
		mode = faultproxy.ModeNone
	}
	id := mode
	if p.RunID != "" {
		id = p.RunID + ":" + mode
	} else if slot != "" {
		id = slot + ":" + mode
	}
	out := PluginPlan{ID: id, Mode: PluginModeNone, Model: p.Model}
	window := func() { out.FromCall, out.Calls, out.UntilS = p.FromCall, p.Calls, p.UntilS }
	switch mode {
	case faultproxy.ModePersistent:
		window()
		out.Status, out.RetryAfterS = p.Status, p.RetryAfterS
		out.Mode = PluginModeError
		if p.Status == http.StatusTooManyRequests {
			out.Mode = PluginModeRateLimit
		}
	case faultproxy.ModeOnce:
		out.Mode, out.Status, out.RetryAfterS = PluginModeError, p.Status, p.RetryAfterS
		out.FromCall, out.Calls = p.Call, 1
	case faultproxy.ModeSlow:
		window()
		out.Mode, out.DelayMS = PluginModeLatency, p.DelayMS+p.FirstByteDelayMS
	case faultproxy.ModeFlaky:
		window()
		out.Mode, out.Status, out.RetryAfterS, out.FailProb, out.Seed = PluginModeFlaky, p.Status, p.RetryAfterS, p.FailProb, p.Nonce
	}
	return out
}

// EncodePluginPlan is the HeaderFaultPlan value for p.
func EncodePluginPlan(p PluginPlan) string {
	b, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodePluginPlan reads a HeaderFaultPlan value.
func DecodePluginPlan(s string) (PluginPlan, error) {
	var p PluginPlan
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") { // the plugin also takes plain JSON
		err := json.Unmarshal([]byte(s), &p)
		return p, err
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return p, fmt.Errorf("fault plan header: %w", err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("fault plan header: %w", err)
	}
	return p, nil
}
