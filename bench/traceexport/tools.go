package traceexport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// toolSpans adds one GenAI execute_tool span per call the tool proxy
// recorded (Evidence.ToolCalls, the ledger of what ran), under the session it
// ran in. An injected fault is an event of its span. Arguments and results
// are exported only with Options.Payloads; by default a span carries their
// sha256 and, when the ledger kept the whole value, their size.
func (b *builder) toolSpans() {
	for _, c := range b.in.Evidence.ToolCalls {
		if c.Started.IsZero() {
			continue
		}
		w := window{start: c.Started, end: c.Started.Add(time.Duration(c.LatencyMS) * time.Millisecond)}
		a := attrs{}
		a.str("gen_ai.operation.name", "execute_tool")
		a.str("gen_ai.tool.name", c.Tool)
		a.str("gen_ai.tool.type", toolType(c.Protocol))
		a.str("heimdall.tool.source", "tool_proxy")
		a.int("heimdall.tool.seq", int64(c.Seq))
		a.str("heimdall.tool.server", c.Server)
		a.str("heimdall.tool.protocol", c.Protocol)
		a.str("heimdall.session.id", c.Session)
		if c.Status != 0 {
			a.int("http.response.status_code", int64(c.Status))
		}
		a.bool("heimdall.tool.is_error", c.IsError)
		a.str("heimdall.tool.fault", c.Fault)
		b.payloadAttrs(&a, "args", c.Arguments, c.ArgsSHA256)
		b.payloadAttrs(&a, "result", c.Result, c.ResultSHA256)
		for _, flag := range []struct {
			key string
			on  bool
		}{{"heimdall.tool.undelivered", c.Undelivered}, {"heimdall.tool.replayed", c.Replayed},
			{"heimdall.tool.unmatched", c.Unmatched}, {"heimdall.tool.blocked", c.Blocked}} {
			if flag.on {
				a.bool(flag.key, true)
			}
		}
		name := strings.TrimSpace("execute_tool " + c.Tool)
		i := b.add(fmt.Sprintf("tool:%d", c.Seq), b.parentAt(c.Started, c.Session), name, SpanKindInternal, w, a)
		sp := &b.spans[i]
		if c.IsError {
			sp.Status = &Status{Code: StatusError, Message: "tool error"}
			sp.Attributes = append(sp.Attributes, Str("error.type", firstNonEmpty(c.Fault, "tool_error")))
		}
		if c.Fault != "" {
			fa := attrs{}
			fa.str("heimdall.fault.source", "tool_proxy")
			fa.str("heimdall.fault.mode", c.Fault)
			fa.str("heimdall.tool.server", c.Server)
			sp.Events = append(sp.Events, SpanEvent{TimeUnixNano: nanos(w.start), Name: "heimdall.fault", Attributes: fa})
		}
	}
}

// payloadAttrs adds one tool payload (args or result): its sha256 always;
// its size when the ledger kept the whole value (the text hashes to the
// recorded sha256), else heimdall.tool.<kind>_clipped; and with
// Options.Payloads the text itself, redacted and capped, as the GenAI
// gen_ai.tool.call.arguments or gen_ai.tool.call.result attribute.
func (b *builder) payloadAttrs(a *attrs, kind, text, sum string) {
	a.str("heimdall.tool."+kind+"_sha256", sum)
	if sum != "" {
		if hexSHA256(text) == sum {
			a.int("heimdall.tool."+kind+"_bytes", int64(len(text)))
		} else {
			a.bool("heimdall.tool."+kind+"_clipped", true)
		}
	}
	if !b.opts.Payloads || text == "" {
		return
	}
	key := "gen_ai.tool.call.arguments"
	if kind == "result" {
		key = "gen_ai.tool.call.result"
	}
	a.str(key, b.payload(text))
}

// toolType maps the proxy protocol to gen_ai.tool.type: a call to an
// external API is an extension, any other call a function.
func toolType(protocol string) string {
	if protocol == "external" {
		return "extension"
	}
	return "function"
}

// hexSHA256 returns the hex sha256 of s.
func hexSHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
