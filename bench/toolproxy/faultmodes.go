package toolproxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Resilience fault modes (lane X22, RESEARCH_R5 recommendation 1). They model
// the failures real tool APIs have: rate limits, latency, flaky calls,
// partial pages, expired credentials, removed tools, API version changes and
// non-idempotent retries.
const (
	FaultRateLimit       = "rate_limit"       // over Limit calls per WindowS: refused with Retry-After
	FaultSlow            = "slow"             // held DelayMS, then forwarded; the real answer comes back
	FaultFlaky           = "flaky"            // fails with probability Prob, seeded by the nonce
	FaultPartial         = "partial"          // arrays in the result cut to Keep items, with next_cursor and truncated
	FaultAuthExpired     = "auth_expired"     // refused as expired until the agent calls the Refresh tool
	FaultToolRemoved     = "tool_removed"     // the tool leaves tools/list and its calls get "method not found"
	FaultInputDrift      = "input_drift"      // parameters renamed (Rename, old to new): old-shape calls are refused
	FaultDuplicateEffect = "duplicate_effect" // the call reaches the server twice
)

// defaultSlowMS is how long a slow fault holds a call when it does not say.
const defaultSlowMS = 5000

// rpcMethodNotFound is the JSON-RPC error code of a call to a tool that is gone.
const rpcMethodNotFound = -32601

// faultState is the per-fault state of the stateful resilience modes. It is
// reset whenever the fault file changes.
type faultState struct {
	accepted  []time.Time // rate_limit: when calls inside the window were let through
	refreshed bool        // auth_expired: the agent called the refresh tool
	bound     string      // tool_removed without a tool: the first tool called, the one that goes away
}

// gate applies the stateful resilience modes to a call decide already
// matched and counted (the seen'th matching call). It returns false when the
// call is let through untouched; for rate_limit it fills ft.RetryAfterS with
// the wait it tells the agent. Called with f.mu held.
func (f *faultFile) gate(ft *schemas.ToolFault, now time.Time) bool {
	switch ft.Mode {
	case FaultRateLimit:
		window := time.Duration(max(ft.WindowS, 1)) * time.Second
		kept := f.state.accepted[:0]
		for _, t := range f.state.accepted {
			if now.Sub(t) < window {
				kept = append(kept, t)
			}
		}
		f.state.accepted = kept
		if len(kept) < max(ft.Limit, 0) {
			f.state.accepted = append(f.state.accepted, now)
			return false
		}
		if ft.RetryAfterS <= 0 {
			wait := window
			if len(kept) > 0 {
				wait = window - now.Sub(kept[0])
			}
			ft.RetryAfterS = max(1, int(math.Ceil(wait.Seconds())))
		}
		return true
	case FaultFlaky:
		return flakyFails(ft.Nonce, f.seen, ft.Prob)
	case FaultAuthExpired:
		return !f.state.refreshed
	}
	return true
}

// flakyFails is the seeded coin of a flaky fault: the same nonce and call
// number always give the same answer, so a run can be replayed exactly.
func flakyFails(nonce string, call int, p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	h := sha256.Sum256(fmt.Appendf(nil, "%s:%d", nonce, call))
	u := float64(binary.BigEndian.Uint64(h[:8])>>11) / float64(1<<53)
	return u < p
}

// isRefreshCall reports whether a call is the auth_expired fault's refresh
// tool on the faulted server; such a call renews the token and is never
// faulted itself.
func isRefreshCall(ft *schemas.ToolFault, server, tool string) bool {
	return ft.Mode == FaultAuthExpired && ft.Refresh != "" && tool == ft.Refresh && (ft.Server == "" || ft.Server == server)
}

// answersItself reports whether a mode answers the agent from the proxy
// without forwarding the call (when it takes it). input_drift answers itself
// only for an old-shape call (driftCall).
func answersItself(mode string) bool {
	switch mode {
	case FaultError, FaultTimeout, FaultRateLimit, FaultFlaky, FaultAuthExpired, FaultToolRemoved:
		return true
	}
	return false
}

// syntheticMessage is the error text a self-answering resilience mode gives
// the agent. The nonce rides along like a request id, as for error faults.
func syntheticMessage(ft schemas.ToolFault) string {
	if ft.Message != "" {
		return faultMessage(ft)
	}
	msg := ""
	switch ft.Mode {
	case FaultRateLimit:
		msg = fmt.Sprintf("429 Too Many Requests: rate limit of %d calls per %ds exceeded. Retry-After: %d", ft.Limit, max(ft.WindowS, 1), ft.RetryAfterS)
	case FaultFlaky:
		msg = "503 Service Unavailable: the tool server failed this request, try again"
	case FaultAuthExpired:
		msg = "401 Unauthorized: access token expired"
		if ft.Refresh != "" {
			msg += fmt.Sprintf("; call %s to get a new token", ft.Refresh)
		}
	case FaultToolRemoved:
		msg = fmt.Sprintf("tool %q not found: it is no longer offered by this server", ft.Tool)
	case FaultInputDrift:
		msg = driftMessage(ft)
	default:
		return faultMessage(ft)
	}
	ft.Message = msg
	return faultMessage(ft)
}

// driftMessage names the renamed parameters, the way a versioned API's
// validation error does.
func driftMessage(ft schemas.ToolFault) string {
	version := ft.Version
	if version == "" {
		version = "v2"
	}
	var parts []byte
	for _, old := range sortedStrings(ft.Rename) {
		if len(parts) > 0 {
			parts = append(parts, ", "...)
		}
		parts = fmt.Appendf(parts, "%q is now %q", old, ft.Rename[old])
	}
	return fmt.Sprintf("400 Bad Request: invalid arguments for API %s: %s", version, parts)
}

// syntheticStatus is the HTTP status a plain HTTP or external tool call gets
// from a self-answering mode.
func syntheticStatus(mode string) int {
	switch mode {
	case FaultTimeout:
		return http.StatusGatewayTimeout
	case FaultRateLimit:
		return http.StatusTooManyRequests
	case FaultFlaky:
		return http.StatusServiceUnavailable
	case FaultAuthExpired:
		return http.StatusUnauthorized
	case FaultToolRemoved:
		return http.StatusNotFound
	case FaultInputDrift:
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// rpcErrorResponse is a JSON-RPC 2.0 error response, used by tool_removed:
// a call to a tool the server no longer has is a protocol error, not a tool
// result.
func rpcErrorResponse(id json.RawMessage, code int, msg string) (json.RawMessage, json.RawMessage) {
	if len(bytes.TrimSpace(id)) == 0 {
		id = json.RawMessage("null")
	}
	e, _ := json.Marshal(map[string]any{"code": code, "message": msg})
	b, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   json.RawMessage `json:"error"`
	}{"2.0", id, e})
	if err != nil {
		return toolErrorResponse(id, msg), e
	}
	return b, e
}

// driftCall checks one call's arguments against an input_drift fault. An
// old-shape call (any renamed parameter under its old name) is refused:
// stale is true. A new-shape call is translated back to the shape the real
// server still takes, so it succeeds: out holds the arguments to forward.
// Arguments that are not a JSON object are forwarded unchanged.
func driftCall(ft schemas.ToolFault, args []byte) (out []byte, stale bool) {
	var obj map[string]json.RawMessage
	if len(bytes.TrimSpace(args)) == 0 || json.Unmarshal(args, &obj) != nil || obj == nil {
		return args, false
	}
	for old := range ft.Rename {
		if _, ok := obj[old]; ok {
			return args, true
		}
	}
	changed := false
	for old, nu := range ft.Rename {
		if v, ok := obj[nu]; ok {
			delete(obj, nu)
			obj[old] = v
			changed = true
		}
	}
	if !changed {
		return args, false
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return args, false
	}
	return b, false
}

// withArguments replaces params.arguments of a tools/call message.
func withArguments(raw json.RawMessage, args []byte) (json.RawMessage, bool) {
	var msg map[string]json.RawMessage
	if json.Unmarshal(raw, &msg) != nil {
		return raw, false
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(msg["params"], &params) != nil || params == nil {
		return raw, false
	}
	params["arguments"] = json.RawMessage(args)
	p, err := json.Marshal(params)
	if err != nil {
		return raw, false
	}
	msg["params"] = p
	out, err := json.Marshal(msg)
	if err != nil {
		return raw, false
	}
	return out, true
}

// partialValue cuts every array of more than Keep items in v to Keep items.
// An object that held a cut array gets "truncated": true and a "next_cursor"
// (when it has none), so a careful agent can tell the page is not all.
func partialValue(v any, keep int) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		changed, cut := false, false
		for k, val := range x {
			if arr, ok := val.([]any); ok && len(arr) > keep {
				x[k], changed, cut = arr[:keep], true, true
				continue
			}
			nv, c := partialValue(val, keep)
			x[k], changed = nv, changed || c
		}
		if cut {
			x["truncated"] = true
			if _, ok := x["next_cursor"]; !ok {
				x["next_cursor"] = fmt.Sprintf("page-2-of-%d", keep)
			}
		}
		return x, changed
	case []any:
		if len(x) > keep {
			return map[string]any{"items": x[:keep], "truncated": true, "next_cursor": fmt.Sprintf("page-2-of-%d", keep)}, true
		}
		changed := false
		for i := range x {
			nv, c := partialValue(x[i], keep)
			x[i], changed = nv, changed || c
		}
		return x, changed
	}
	return v, false
}

// dropTool removes the named tool from a tools/list result object.
func dropTool(obj map[string]any, name string) bool {
	tools, _ := obj["tools"].([]any)
	kept := tools[:0:0]
	for _, t := range tools {
		if tool, _ := t.(map[string]any); tool != nil && tool["name"] == name {
			continue
		}
		kept = append(kept, t)
	}
	if len(kept) == len(tools) {
		return false
	}
	obj["tools"] = kept
	return true
}

// driftSchema renames parameters in one tool's inputSchema (properties and
// required), old to new: the listing of the new API version.
func driftSchema(tool map[string]any, rename map[string]string) bool {
	schema, _ := tool["inputSchema"].(map[string]any)
	if schema == nil {
		return false
	}
	changed := false
	if props, _ := schema["properties"].(map[string]any); props != nil {
		for old, nu := range rename {
			if v, ok := props[old]; ok {
				delete(props, old)
				props[nu] = v
				changed = true
			}
		}
	}
	if req, _ := schema["required"].([]any); req != nil {
		for i, r := range req {
			if s, _ := r.(string); s != "" {
				if nu, ok := rename[s]; ok {
					req[i], changed = nu, true
				}
			}
		}
	}
	return changed
}

// checkResilienceFields checks the fields each resilience mode needs.
func checkResilienceFields(ft schemas.ToolFault) error {
	if ft.Limit < 0 || ft.WindowS < 0 || ft.RetryAfterS < 0 || ft.DelayMS < 0 || ft.Keep < 0 {
		return errors.New("fault limit, window_s, retry_after_s, delay_ms and keep must not be negative")
	}
	switch ft.Mode {
	case FaultRateLimit:
		if ft.Limit < 1 || ft.WindowS < 1 {
			return errors.New("rate_limit fault needs limit >= 1 and window_s >= 1")
		}
	case FaultFlaky:
		if !(ft.Prob > 0 && ft.Prob <= 1) {
			return fmt.Errorf("flaky fault needs prob in (0, 1], got %v", ft.Prob)
		}
	case FaultPartial:
		if ft.Keep < 1 {
			return errors.New("partial fault needs keep >= 1")
		}
	case FaultAuthExpired:
		if ft.Refresh == "" {
			return errors.New("auth_expired fault needs refresh: the tool that renews the token")
		}
		if ft.Refresh == ft.Tool {
			return errors.New("auth_expired refresh tool must differ from the faulted tool")
		}
	case FaultToolRemoved:
		if ft.Tool == "" && ft.FromCall < 2 {
			return errors.New("tool_removed fault needs tool, or from_call >= 2 to remove the first tool the agent calls")
		}
	case FaultInputDrift:
		if ft.Tool == "" || len(ft.Rename) == 0 {
			return errors.New("input_drift fault needs tool and rename")
		}
		for from, to := range ft.Rename {
			if from == "" || to == "" || from == to {
				return fmt.Errorf("input_drift rename %q to %q is not a rename", from, to)
			}
		}
	}
	return nil
}

func sortedStrings(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

// slowHold is how long a slow fault holds a call.
func slowHold(ft schemas.ToolFault) time.Duration {
	if ft.DelayMS > 0 {
		return time.Duration(ft.DelayMS) * time.Millisecond
	}
	return defaultSlowMS * time.Millisecond
}

// sendDuplicate sends a call to the tool server once more before the real
// forward (duplicate_effect): a retry the agent never asked for, which an
// idempotent tool absorbs. The extra answer is drained and dropped; a failure
// of the extra send is logged and the real forward still happens.
func (s *Server) sendDuplicate(r *http.Request, rt Route, path string, body []byte, plain bool) {
	req := s.upstreamRequest(r, rt, path, body)
	if plain {
		req.Header.Del("Content-Encoding")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Warn("duplicate_effect: the extra send failed", "service", rt.Service, "error", transportError(err))
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, s.parseCap))
	resp.Body.Close()
}

// syntheticHTTP answers a plain HTTP tool call that a self-answering
// resilience mode took, with that mode's status (429 with Retry-After for
// rate_limit) and a JSON error body, without reaching the server.
func (s *Server) syntheticHTTP(w http.ResponseWriter, row *schemas.ToolCall, ft schemas.ToolFault) {
	row.Fault, row.Nonce, row.IsError = ft.Mode, ft.Nonce, true
	status := syntheticStatus(ft.Mode)
	if ft.Mode == FaultRateLimit {
		row.RetryAfterS = ft.RetryAfterS
		w.Header().Set("Retry-After", strconv.Itoa(ft.RetryAfterS))
	}
	out, _ := json.Marshal(map[string]string{"error": syntheticMessage(ft)})
	row.Status = status
	row.Result, row.ResultSHA256 = Capture(out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, werr := w.Write(out)
	row.Undelivered = werr != nil
	row.LatencyMS = s.now().Sub(row.Started).Milliseconds()
	s.append(row)
}
