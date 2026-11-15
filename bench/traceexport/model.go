package traceexport

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// step is one logical model request: the gateway message rows that share a
// root row (a fallback row's parent chain leads to the row it fell back
// from), in time order.
type step struct {
	id     string // logical request id: the root row id, or request:<row index> for a row without one
	number int    // 1-based, in order of first row
	rows   []schemas.GatewayRequest
}

// window is the time from the step's first request to its last response.
func (s step) window() window {
	w := window{}
	for _, r := range s.rows {
		w.cover(r.Timestamp)
		w.cover(responseTime(r))
	}
	return w
}

// modelSpans adds one span per logical model request, named and attributed
// as a GenAI inference span, with its retries and fallbacks as events, and
// then one span per tool call the model requested.
func (b *builder) modelSpans() {
	steps := groupSteps(b.in.Evidence.Requests)
	stepSpan := map[string]string{}
	for _, st := range steps {
		w := st.window()
		if w.start.IsZero() {
			continue
		}
		first, last := st.rows[0], st.rows[len(st.rows)-1]
		name := "chat"
		if m := firstNonEmpty(first.ModelRequested, last.ModelServed); m != "" {
			name += " " + m
		}
		i := b.add("model:"+st.id, b.parentAt(w.start, ""), name, SpanKindClient, w, b.modelAttrs(st))
		sp := &b.spans[i]
		sp.Events = stepEvents(st, w)
		switch last.Status {
		case "error":
			msg := clean(fmt.Sprintf("error %d: %s", last.ErrorStatus, last.ErrorMessage), textCap)
			sp.Status = &Status{Code: StatusError, Message: msg}
			errType := "_OTHER"
			if last.ErrorStatus != 0 {
				errType = strconv.Itoa(last.ErrorStatus)
			}
			sp.Attributes = append(sp.Attributes, Str("error.type", errType))
		case "processing":
			sp.Attributes = append(sp.Attributes, Bool("heimdall.gateway.incomplete", true))
		}
		stepSpan[st.id] = sp.SpanID
		b.models = append(b.models, modelRef{spanID: sp.SpanID, index: i, window: w})
	}
	b.requestedToolSpans(steps, stepSpan)
}

// modelAttrs are a model request span's attributes.
func (b *builder) modelAttrs(st step) []KeyValue {
	first, last := st.rows[0], st.rows[len(st.rows)-1]
	a := attrs{}
	a.str("gen_ai.operation.name", "chat")
	provider := genAIProvider(last.Provider)
	a.str("gen_ai.provider.name", provider)
	a.str("gen_ai.system", provider)
	a.str("heimdall.gateway.provider", last.Provider)
	a.str("gen_ai.request.model", first.ModelRequested)
	a.str("gen_ai.response.model", last.ModelServed)
	a.str("gen_ai.response.id", last.ResponseID)
	a.bool("gen_ai.request.stream", first.Stream)
	a.str("gen_ai.conversation.id", b.in.Run.Gateway.SessionID)
	if tok, ok := sumTokens(st.rows); ok {
		a.int("gen_ai.usage.input_tokens", tok.Fresh+tok.CacheRead+tok.CacheWrite)
		a.int("gen_ai.usage.output_tokens", tok.Out)
		a.int("gen_ai.usage.cache_read.input_tokens", tok.CacheRead)
		a.int("gen_ai.usage.cache_write.input_tokens", tok.CacheWrite)
	}
	number, attempts, retries, fallbacks := b.stepCounts(st)
	a.int("heimdall.logical_step", int64(number))
	a.str("heimdall.logical_request_id", st.id)
	ids := make([]string, 0, len(st.rows))
	for _, r := range st.rows {
		if r.ID != "" {
			ids = append(ids, r.ID)
		}
	}
	a.strs("heimdall.gateway.request_ids", ids)
	a.int("heimdall.attempts", int64(attempts))
	a.int("heimdall.retries", int64(retries))
	a.int("heimdall.fallbacks", int64(fallbacks))
	if usd, ok := b.stepCost(ids); ok {
		a.double("heimdall.cost_usd", usd)
	}
	for _, c := range b.in.Evidence.Cost {
		if c.FaultInject && slices.Contains(ids, c.RequestID) {
			a.bool("heimdall.fault_injected", true)
			break
		}
	}
	if last.CacheHit {
		a.bool("heimdall.gateway.cache_hit", true)
	}
	return a
}

// stepCounts returns the logical step number and the attempt, retry and
// fallback counts of a step: from the canonical evidence fields when the run
// recorded them, else from the gateway rows by the same rule (a row's
// attempt trail, at least one attempt per row; every attempt after the first
// is a retry; a row with a fallback index is a fallback).
func (b *builder) stepCounts(st step) (number, attempts, retries, fallbacks int) {
	ev := b.in.Evidence
	if len(ev.LogicalSteps) > 0 {
		number = st.number
		for _, l := range ev.LogicalSteps {
			if l.LogicalRequestID == st.id {
				number = l.LogicalStep
			}
		}
		for _, a := range ev.Attempts {
			if a.LogicalRequestID == st.id {
				attempts++
			}
		}
		for _, r := range ev.Retries {
			if r.LogicalRequestID == st.id {
				retries++
			}
		}
		for _, f := range ev.Fallbacks {
			if f.LogicalRequestID == st.id {
				fallbacks++
			}
		}
		return number, attempts, retries, fallbacks
	}
	for _, r := range st.rows {
		n := max(len(r.Attempts), 1)
		attempts += n
		retries += n - 1
		if r.FallbackIndex > 0 {
			fallbacks++
		}
	}
	return st.number, attempts, retries, fallbacks
}

// stepCost sums the priced cost rows of a step's requests. It is unknown
// (false) when any of them has no cost row or an unobserved price.
func (b *builder) stepCost(ids []string) (float64, bool) {
	if len(ids) == 0 {
		return 0, false
	}
	var total float64
	for _, id := range ids {
		found := false
		for _, c := range b.in.Evidence.Cost {
			if c.RequestID != id {
				continue
			}
			v, ok := c.USD.Float()
			if !ok {
				return 0, false
			}
			total += v
			found = true
		}
		if !found {
			return 0, false
		}
	}
	return total, true
}

// stepEvents returns a step's retries and fallbacks as span events, at the
// time of the row they happened in.
func stepEvents(st step, w window) []SpanEvent {
	var out []SpanEvent
	for _, r := range st.rows {
		for i := 1; i < len(r.Attempts); i++ {
			cur, prev := r.Attempts[i], r.Attempts[i-1]
			no := cur.N
			if no == 0 {
				no = i + 1
			}
			a := attrs{}
			a.int("heimdall.attempt", int64(no))
			if prev.StatusCode != 0 {
				a.int("heimdall.previous_status", int64(prev.StatusCode))
			}
			a.str("heimdall.reason", clean(prev.FailReason, textCap))
			a.str("heimdall.failure_class", prev.FailureClass)
			if prev.RetryAfterMS > 0 {
				a.int("heimdall.retry_after_ms", int64(prev.RetryAfterMS))
			}
			a.str("heimdall.gateway.request_id", r.ID)
			out = append(out, SpanEvent{TimeUnixNano: clampNanos(r.Timestamp, w), Name: "heimdall.retry", Attributes: a})
		}
		if r.FallbackIndex > 0 {
			a := attrs{}
			a.int("heimdall.fallback_index", int64(r.FallbackIndex))
			a.str("gen_ai.request.model", r.ModelRequested)
			a.str("gen_ai.response.model", r.ModelServed)
			a.str("heimdall.gateway.request_id", r.ID)
			out = append(out, SpanEvent{TimeUnixNano: clampNanos(r.Timestamp, w), Name: "heimdall.fallback", Attributes: a})
		}
	}
	return out
}

// requestedTool is one tool call the model asked for.
type requestedTool struct {
	id, name string
	record   *schemas.ToolCallRecord // the canonical record, when the run has one
}

// requestedToolSpans adds one span per tool call the model requested, under
// the model request that asked for it. It runs from the response that asked
// for the call to the first later request that carried its result back to the
// model; a call whose result never reached the model is a zero-length span
// with heimdall.tool.result_returned false.
func (b *builder) requestedToolSpans(steps []step, stepSpan map[string]string) {
	type origin struct {
		step string
		row  schemas.GatewayRequest
		ref  schemas.ToolCallRef
	}
	var rows []schemas.GatewayRequest
	asked := map[string]origin{}
	for _, st := range steps {
		for _, r := range st.rows {
			rows = append(rows, r)
			for _, c := range r.ToolCalls {
				if _, seen := asked[c.ID]; !seen && c.ID != "" {
					asked[c.ID] = origin{step: st.id, row: r, ref: c}
				}
			}
		}
	}
	for _, rt := range b.requestedTools(steps) {
		o, ok := asked[rt.id]
		parent := stepSpan[o.step]
		if !ok || parent == "" {
			continue
		}
		w := window{start: responseTime(o.row), end: responseTime(o.row)}
		returned := false
		for _, r := range rows {
			if r.Timestamp.Before(w.start) || !slices.Contains(r.ToolResultIDs, rt.id) {
				continue
			}
			w.end, returned = r.Timestamp, true
			break
		}
		a := attrs{}
		a.str("gen_ai.tool.name", firstNonEmpty(rt.name, o.ref.Name))
		a.str("gen_ai.tool.call.id", rt.id)
		a.str("gen_ai.tool.type", "function")
		a.str("heimdall.tool.source", "model_request")
		a.str("heimdall.tool.args_sha256", o.ref.ArgsHash)
		a.bool("heimdall.tool.result_returned", returned)
		if rt.record != nil {
			a.int("heimdall.logical_step", int64(rt.record.LogicalStep))
			if rt.record.ExecutionSource != "" && rt.record.ExecutionSource != schemas.EvidenceSourceUnknown {
				a.bool("heimdall.tool.executed", rt.record.Executed)
				a.str("heimdall.tool.execution_source", string(rt.record.ExecutionSource))
			}
		}
		if b.opts.Payloads {
			a.str("heimdall.tool.target", b.payload(o.ref.Target))
		}
		name := "tool_call " + firstNonEmpty(rt.name, o.ref.Name)
		b.add("tool_call:"+rt.id, parent, name, SpanKindInternal, w, a)
	}
}

// requestedTools lists the requested tool calls: the canonical records
// (Evidence.RequestedTools) when the run has them, else every tool call id
// in the gateway rows, first occurrence only.
func (b *builder) requestedTools(steps []step) []requestedTool {
	var out []requestedTool
	seen := map[string]bool{}
	if recs := b.in.Evidence.RequestedTools; len(recs) > 0 {
		for i := range recs {
			r := &recs[i]
			if r.ToolCallID == "" || seen[r.ToolCallID] {
				continue
			}
			seen[r.ToolCallID] = true
			out = append(out, requestedTool{id: r.ToolCallID, name: r.ToolName, record: r})
		}
		return out
	}
	for _, st := range steps {
		for _, r := range st.rows {
			for _, c := range r.ToolCalls {
				if c.ID == "" || seen[c.ID] {
					continue
				}
				seen[c.ID] = true
				out = append(out, requestedTool{id: c.ID, name: c.Name})
			}
		}
	}
	return out
}

// groupSteps groups the message rows of a gateway log into logical requests,
// as evidence.NormalizeRequestRecords does: rows in time order, each under
// the root of its parent chain.
func groupSteps(log *schemas.RequestLog) []step {
	if log == nil {
		return nil
	}
	var rows []schemas.GatewayRequest
	for _, r := range log.Requests {
		if r.Kind == "message" {
			rows = append(rows, r)
		}
	}
	slices.SortStableFunc(rows, func(a, b schemas.GatewayRequest) int { return a.Timestamp.Compare(b.Timestamp) })
	parents := map[string]string{}
	for _, r := range rows {
		if r.ID != "" {
			parents[r.ID] = r.ParentID
		}
	}
	var steps []step
	index := map[string]int{}
	for i, r := range rows {
		root := rootID(r.ID, parents)
		if root == "" {
			root = fmt.Sprintf("request:%d", i)
		}
		at, ok := index[root]
		if !ok {
			at = len(steps)
			index[root] = at
			steps = append(steps, step{id: root, number: at + 1})
		}
		steps[at].rows = append(steps[at].rows, r)
	}
	return steps
}

// rootID follows parent links among the run's rows to the first row of a
// logical request. A parent outside the set ends the walk; a cycle resolves
// to its smallest id.
func rootID(id string, parents map[string]string) string {
	cur := id
	seen := map[string]bool{}
	for !seen[cur] {
		seen[cur] = true
		p, ok := parents[cur]
		if !ok || p == "" || p == cur {
			return cur
		}
		if _, known := parents[p]; !known {
			return cur
		}
		cur = p
	}
	least := cur
	for at := parents[cur]; at != cur; at = parents[at] {
		least = min(least, at)
	}
	return least
}

// responseTime is when a row's response arrived: its request time plus the
// recorded latency.
func responseTime(r schemas.GatewayRequest) time.Time {
	if r.Timestamp.IsZero() {
		return time.Time{}
	}
	return r.Timestamp.Add(time.Duration(r.LatencyMS * float64(time.Millisecond)))
}

// sumTokens adds the token buckets of the rows that recorded usage. It is
// false when none did: usage is then unknown, not zero.
func sumTokens(rows []schemas.GatewayRequest) (schemas.Tokens, bool) {
	var t schemas.Tokens
	recorded := false
	for _, r := range rows {
		if r.Tokens == nil {
			continue
		}
		recorded = true
		t.Fresh += r.Tokens.Fresh
		t.CacheRead += r.Tokens.CacheRead
		t.CacheWrite += r.Tokens.CacheWrite
		t.Out += r.Tokens.Out
	}
	return t, recorded
}

// genAIProvider maps a Bifrost provider name to the GenAI semantic
// conventions' gen_ai.provider.name value. Names the conventions spell the
// same way, and unknown ones, pass through.
func genAIProvider(p string) string {
	switch p {
	case "bedrock":
		return "aws.bedrock"
	case "vertex":
		return "gcp.vertex_ai"
	case "gemini":
		return "gcp.gemini"
	case "azure":
		return "azure.ai.openai"
	case "mistral":
		return "mistral_ai"
	case "xai":
		return "x_ai"
	}
	return p
}
