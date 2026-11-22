package mcptest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/admitscan"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Inspect limits.
const (
	DefaultRepeats  = 5
	MaxRepeats      = 20
	DefaultMaxCalls = 80
	maxToolRepeats  = 3
)

// Options say how hard Inspect may push the server.
type Options struct {
	// Repeats is how many times tools/list and ping are sent (default 5).
	Repeats int
	// CallReadOnly lets Inspect call tools the server marks readOnlyHint.
	CallReadOnly bool
	// CallTools are tools the owner says are safe to call with sample
	// arguments, read-only or not. A destructive tool is called only when
	// it is named here.
	CallTools []string
	// Args are sample arguments per tool; a tool without them gets
	// placeholder arguments built from its schema.
	Args map[string]map[string]any
	// MaxCalls caps every request Inspect sends (default 80).
	MaxCalls int
	// Now is the clock (tests).
	Now func() time.Time
}

// ToolView is one listed tool as the report shows it.
type ToolView struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	ReadOnly    bool            `json:"read_only"`
	Destructive bool            `json:"destructive"`
}

// Scores are 0 to 100; Overall is their mean.
type Scores struct {
	Overall       int `json:"overall"`
	Reliability   int `json:"reliability"`
	ErrorRecovery int `json:"error_recovery"`
	Latency       int `json:"latency"`
	Schema        int `json:"schema"`
	Description   int `json:"description"`
}

// Reliability counts the plain requests that should always work.
type Reliability struct {
	Attempts      int                    `json:"attempts"`
	Succeeded     int                    `json:"succeeded"`
	Rate          float64                `json:"rate"`
	StableListing bool                   `json:"stable_listing"`
	Changes       []admitscan.ToolChange `json:"listing_changes,omitempty"`
	PingSupported bool                   `json:"ping_supported"`
	Failures      []string               `json:"failures"`
}

// Percentiles of one kind of request, in milliseconds.
type Percentiles struct {
	Samples int   `json:"samples"`
	P50MS   int64 `json:"p50_ms"`
	P95MS   int64 `json:"p95_ms"`
	MaxMS   int64 `json:"max_ms"`
}

// Latency is every timed request, and per method or tool.
type Latency struct {
	Percentiles
	ByMethod map[string]Percentiles `json:"by_method"`
}

// RecoveryCheck is one deliberate bad request and what the server did.
type RecoveryCheck struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail"`
}

// ToolCallResult is one sample call of a tool.
type ToolCallResult struct {
	Tool        string         `json:"tool"`
	Args        map[string]any `json:"args"`
	Placeholder bool           `json:"placeholder"` // the arguments were built from the schema
	OK          bool           `json:"ok"`
	IsError     bool           `json:"is_error"`
	LatencyMS   int64          `json:"latency_ms"`
	Detail      string         `json:"detail"`
	// Excerpt is the start of the result's text, for a reference answer.
	Excerpt string `json:"excerpt,omitempty"`
}

// Report is everything Inspect measured.
type Report struct {
	URL         string                     `json:"url"`
	CheckedAt   time.Time                  `json:"checked_at"`
	Reached     bool                       `json:"reached"`
	Error       string                     `json:"error,omitempty"`
	Hello       Hello                      `json:"server"`
	Tools       []ToolView                 `json:"tools"`
	Scores      Scores                     `json:"scores"`
	Reliability Reliability                `json:"reliability"`
	Latency     Latency                    `json:"latency"`
	Recovery    []RecoveryCheck            `json:"error_recovery"`
	Calls       []ToolCallResult           `json:"calls"`
	Quality     QualityReport              `json:"quality"`
	Scan        []schemas.AdmitScanFinding `json:"scan"`
	Requests    int                        `json:"requests"`
}

// inspector is one Inspect run.
type inspector struct {
	c       *Client
	opts    Options
	rep     *Report
	samples map[string][]time.Duration
	sent    int
}

func (in *inspector) budget() bool { return in.sent < in.opts.MaxCalls }

func (in *inspector) time(key string, a Answer) {
	if a.Transport == "" {
		in.samples[key] = append(in.samples[key], a.Latency)
	}
}

// Inspect measures the server at t over hc (nil: a default client). It
// never returns an error: a server that cannot be reached is a report with
// Reached false and Error saying why.
func Inspect(ctx context.Context, t Target, hc *http.Client, opts Options) *Report {
	if opts.Repeats <= 0 {
		opts.Repeats = DefaultRepeats
	}
	opts.Repeats = min(opts.Repeats, MaxRepeats)
	if opts.MaxCalls <= 0 {
		opts.MaxCalls = DefaultMaxCalls
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	rep := &Report{URL: t.URL, CheckedAt: now().UTC(), Tools: []ToolView{}, Recovery: []RecoveryCheck{}, Calls: []ToolCallResult{}, Scan: []schemas.AdmitScanFinding{},
		Reliability: Reliability{Failures: []string{}}, Latency: Latency{ByMethod: map[string]Percentiles{}}, Quality: QualityReport{Tools: []ToolQuality{}, Server: []Issue{}}}
	in := &inspector{c: NewClient(t, hc), opts: opts, rep: rep, samples: map[string][]time.Duration{}}
	defer func() { rep.Requests = in.sent }()
	defer in.c.Close(context.WithoutCancel(ctx))

	hello, a, err := in.c.Initialize(ctx)
	in.sent += 2
	in.count("initialize", a, err)
	if err != nil {
		rep.Error = utils.HidePrivateAddresses(err.Error())
		in.finish()
		return rep
	}
	rep.Reached, rep.Hello = true, hello

	tools, a, err := in.c.ListTools(ctx)
	in.sent++
	in.count("tools/list", a, err)
	if err != nil {
		rep.Error = utils.HidePrivateAddresses(err.Error())
		in.finish()
		return rep
	}
	for _, tl := range tools {
		ro, de := annotations(tl)
		rep.Tools = append(rep.Tools, ToolView{Name: tl.Name, Title: tl.Title, Description: tl.Description, InputSchema: tl.InputSchema, ReadOnly: ro, Destructive: de})
	}
	rep.Quality = GradeListing(tools)
	rep.Scan = append(rep.Scan, admitscan.ScanMCPListing(serverLabel(hello, t.URL), admitscan.MCPListing{Instructions: hello.Instructions, Tools: tools}, admitscan.TextOptions{})...)

	in.reliability(ctx, tools)
	in.recovery(ctx, tools)
	in.calls(ctx, tools)
	in.finish()
	return rep
}

func serverLabel(h Hello, url string) string {
	if h.Name != "" {
		return h.Name
	}
	return url
}

// count records one reliability attempt.
func (in *inspector) count(method string, a Answer, err error) {
	in.time(method, a)
	in.rep.Reliability.Attempts++
	if err == nil && !a.Failed() {
		in.rep.Reliability.Succeeded++
		return
	}
	msg := Describe(a)
	if err != nil {
		msg = err.Error()
	}
	in.rep.Reliability.Failures = append(in.rep.Reliability.Failures, method+": "+msg)
}

// reliability repeats tools/list and ping, and checks the listing stays the same.
func (in *inspector) reliability(ctx context.Context, first []admitscan.MCPTool) {
	in.rep.Reliability.StableListing = true
	for i := 1; i < in.opts.Repeats && in.budget(); i++ {
		tools, a, err := in.c.ListTools(ctx)
		in.sent++
		in.count("tools/list", a, err)
		if err == nil {
			if ch := admitscan.DiffListings(first, tools); len(ch) > 0 {
				in.rep.Reliability.StableListing = false
				in.rep.Reliability.Changes = append(in.rep.Reliability.Changes, ch...)
			}
		}
	}
	for i := 0; i < in.opts.Repeats && in.budget(); i++ {
		a := in.c.Call(ctx, "ping", nil)
		in.sent++
		if a.Error != nil && a.Error.Code == -32601 {
			return // ping is optional in MCP; not supported is no failure
		}
		in.rep.Reliability.PingSupported = true
		in.count("ping", a, nil)
	}
}

// callable lists the tools Inspect may call, in listing order.
func (in *inspector) callable(tools []admitscan.MCPTool) []admitscan.MCPTool {
	var out []admitscan.MCPTool
	for _, t := range tools {
		ro, de := annotations(t)
		named := slices.Contains(in.opts.CallTools, t.Name)
		if named || (in.opts.CallReadOnly && ro && !de) {
			out = append(out, t)
		}
	}
	return out
}

// rejected reports whether a deliberate bad request was refused cleanly: a
// JSON-RPC error, a tool result with isError, or a 4xx answer. A 5xx, no
// answer, or a plain success is not.
func rejected(a Answer) (bool, string) {
	switch {
	case a.Transport != "":
		return false, "no clean answer: " + a.Transport
	case a.Status >= 500:
		return false, fmt.Sprintf("the server failed with HTTP %d instead of reporting the error", a.Status)
	case a.Error != nil:
		if len(strings.Fields(a.Error.Message)) < 2 {
			return true, fmt.Sprintf("refused with JSON-RPC error %d; the message %q gives the agent little to fix", a.Error.Code, a.Error.Message)
		}
		return true, fmt.Sprintf("refused with JSON-RPC error %d: %s", a.Error.Code, clip(a.Error.Message, 160))
	case a.Status >= 400:
		return true, fmt.Sprintf("refused with HTTP %d", a.Status)
	case a.Result != nil && resultIsError(a.Result):
		txt := resultText(a.Result)
		if len(strings.Fields(txt)) < 2 {
			return true, "refused with an error result that gives the agent little to fix"
		}
		return true, "refused with an error result: " + clip(txt, 160)
	}
	return false, "the server answered as if the request were fine"
}

// recovery sends the deliberate bad requests, then a healthy one.
func (in *inspector) recovery(ctx context.Context, tools []admitscan.MCPTool) {
	add := func(id, title string, ok bool, detail string) {
		in.rep.Recovery = append(in.rep.Recovery, RecoveryCheck{ID: id, Title: title, Passed: ok, Detail: detail})
	}
	skip := func(id, title, why string) {
		in.rep.Recovery = append(in.rep.Recovery, RecoveryCheck{ID: id, Title: title, Skipped: true, Detail: why})
	}

	a := in.c.CallTool(ctx, "heimdall_no_such_tool", map[string]any{})
	in.sent++
	ok, d := rejected(a)
	add("unknown_tool", "Call to a tool that does not exist", ok, d)

	a = in.c.Call(ctx, "heimdall/no_such_method", nil)
	in.sent++
	ok, d = rejected(a)
	add("unknown_method", "Request with an unknown method", ok, d)

	a = in.c.Raw(ctx, []byte(`{"jsonrpc":"2.0","id":99,"method":"tools/list"`))
	in.sent++
	ok, d = rejected(a)
	if !ok && a.Transport == "" && a.Status < 500 && a.Result == nil && a.Error == nil {
		ok, d = true, fmt.Sprintf("ignored with HTTP %d and no result", a.Status)
	}
	add("malformed_json", "Body that is not valid JSON", ok, d)

	var target *admitscan.MCPTool
	for _, t := range in.callable(tools) {
		var n schemaNode
		if json.Unmarshal(t.InputSchema, &n) == nil && len(n.Required) > 0 {
			target = &t
			break
		}
	}
	if target == nil {
		why := "no tool with required arguments may be called; mark read-only tools or allow a tool to run this check"
		skip("missing_arguments", "Tool call with its required arguments left out", why)
		skip("wrong_types", "Tool call with arguments of the wrong type", why)
	} else {
		a = in.c.CallTool(ctx, target.Name, map[string]any{})
		in.sent++
		in.time("tools/call "+target.Name, a)
		ok, d = rejected(a)
		add("missing_arguments", "Tool call with its required arguments left out ("+target.Name+")", ok, d)

		a = in.c.CallTool(ctx, target.Name, wrongArgs(target.InputSchema))
		in.sent++
		in.time("tools/call "+target.Name, a)
		ok, d = rejected(a)
		add("wrong_types", "Tool call with arguments of the wrong type ("+target.Name+")", ok, d)
	}

	if in.rep.Hello.Session {
		saved := in.c.session
		in.c.session = "heimdall-unknown-session"
		a = in.c.Call(ctx, "tools/list", nil)
		in.sent++
		in.c.session = saved
		ok := a.Transport == "" && (a.Status == http.StatusNotFound || a.Status == http.StatusBadRequest || a.Status == http.StatusUnauthorized || a.Error != nil)
		d = fmt.Sprintf("answered HTTP %d", a.Status)
		if a.Transport != "" {
			d = a.Transport
		} else if !ok {
			d += " and served the request; MCP says an unknown session gets 404"
		}
		add("unknown_session", "Request with a session id the server never issued", ok, d)
	}

	_, a, err := in.c.ListTools(ctx)
	in.sent++
	in.time("tools/list", a)
	d = "tools/list worked after the bad requests"
	if err != nil {
		d = "tools/list failed after the bad requests: " + err.Error()
	}
	add("recovers", "Normal request right after the bad ones", err == nil, d)
}

// calls runs each callable tool with sample arguments up to three times.
func (in *inspector) calls(ctx context.Context, tools []admitscan.MCPTool) {
	for _, t := range in.callable(tools) {
		args, placeholder := in.opts.Args[t.Name], false
		if args == nil {
			args, placeholder = PlaceholderArgs(t.InputSchema), true
		}
		for i := 0; i < maxToolRepeats && in.budget(); i++ {
			a := in.c.CallTool(ctx, t.Name, args)
			in.sent++
			in.time("tools/call "+t.Name, a)
			res := ToolCallResult{Tool: t.Name, Args: args, Placeholder: placeholder, LatencyMS: a.Latency.Milliseconds()}
			switch {
			case a.Failed():
				res.Detail = Describe(a)
			case resultIsError(a.Result):
				res.IsError, res.Detail = true, "error result: "+clip(resultText(a.Result), 200)
			default:
				res.OK, res.Detail = true, "ok"
				res.Excerpt = clip(resultText(a.Result), 400)
			}
			in.rep.Calls = append(in.rep.Calls, res)
			if !res.OK {
				break // a failing sample call is not repeated
			}
		}
	}
}

// finish fills the latency percentiles and the scores.
func (in *inspector) finish() {
	var all []time.Duration
	for k, s := range in.samples {
		in.rep.Latency.ByMethod[k] = percentiles(s)
		all = append(all, s...)
	}
	in.rep.Latency.Percentiles = percentiles(all)
	r := &in.rep.Reliability
	if r.Attempts > 0 {
		r.Rate = float64(r.Succeeded) / float64(r.Attempts)
	}
	if !in.rep.Reached {
		return
	}
	sc := &in.rep.Scores
	sc.Reliability = int(r.Rate * 100)
	if !r.StableListing {
		sc.Reliability = max(sc.Reliability-20, 0)
	}
	n, passed := 0, 0
	for _, c := range in.rep.Recovery {
		if c.Skipped {
			continue
		}
		n++
		if c.Passed {
			passed++
		}
	}
	if n > 0 {
		sc.ErrorRecovery = 100 * passed / n
	}
	sc.Latency = latencyScore(in.rep.Latency.P95MS)
	sc.Schema, sc.Description = in.rep.Quality.SchemaScore, in.rep.Quality.DescriptionScore
	sc.Overall = (sc.Reliability + sc.ErrorRecovery + sc.Latency + sc.Schema + sc.Description) / 5
}

// latencyScore maps the p95 to a score: an agent waits on every call.
func latencyScore(p95 int64) int {
	switch {
	case p95 <= 300:
		return 100
	case p95 <= 1000:
		return 80
	case p95 <= 3000:
		return 50
	case p95 <= 10000:
		return 25
	}
	return 10
}

func percentiles(s []time.Duration) Percentiles {
	if len(s) == 0 {
		return Percentiles{}
	}
	ms := make([]int64, len(s))
	for i, d := range s {
		ms[i] = d.Milliseconds()
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	at := func(p float64) int64 {
		i := int(p*float64(len(ms)-1) + 0.5)
		return ms[min(i, len(ms)-1)]
	}
	return Percentiles{Samples: len(ms), P50MS: at(0.5), P95MS: at(0.95), MaxMS: ms[len(ms)-1]}
}

// resultIsError reports whether an MCP tool result says isError.
func resultIsError(raw json.RawMessage) bool {
	var r struct {
		IsError bool `json:"isError"`
	}
	return json.Unmarshal(raw, &r) == nil && r.IsError
}

// resultText joins the text items of an MCP tool result, or gives the
// structured content as JSON.
func resultText(raw json.RawMessage) string {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	if len(parts) == 0 && len(r.Structured) > 0 {
		return string(r.Structured)
	}
	return strings.Join(parts, "\n")
}

// PlaceholderArgs builds arguments for a schema's required properties: the
// default, the first example or enum value, else a plain value of the type.
// It is pure.
func PlaceholderArgs(schema json.RawMessage) map[string]any {
	out := map[string]any{}
	var n schemaNode
	if json.Unmarshal(schema, &n) != nil {
		return out
	}
	for _, name := range n.Required {
		p := n.Properties[name]
		if p == nil {
			out[name] = "test"
			continue
		}
		out[name] = placeholder(p)
	}
	return out
}

func placeholder(p *schemaNode) any {
	switch {
	case p.Default != nil:
		return p.Default
	case len(p.Examples) > 0:
		return p.Examples[0]
	case len(p.Enum) > 0:
		return p.Enum[0]
	}
	switch p.typeName() {
	case "integer", "number":
		return 1
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		obj := map[string]any{}
		for _, r := range p.Required {
			if c := p.Properties[r]; c != nil {
				obj[r] = placeholder(c)
			}
		}
		return obj
	}
	return "test"
}

// wrongArgs gives every required property a value of another type.
func wrongArgs(schema json.RawMessage) map[string]any {
	out := map[string]any{}
	var n schemaNode
	if json.Unmarshal(schema, &n) != nil {
		return out
	}
	for _, name := range n.Required {
		p := n.Properties[name]
		t := ""
		if p != nil {
			t = p.typeName()
		}
		switch t {
		case "string", "":
			out[name] = map[string]any{"not": "a string"}
		default:
			out[name] = "not-a-" + t
		}
	}
	return out
}
