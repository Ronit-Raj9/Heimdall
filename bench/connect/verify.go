package connect

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// Verify v2 (section 4.9): prove a config works, row by row, with the same
// request a run sends. It replaces the READY ping of "Test my endpoint": a
// nonce must come back at the answer path, the state probe decides the
// conversation history mode, the isolation probe catches memory that leaks
// across sessions. The agent.verify job runs these rows and then the real
// task row (one library task through the real launcher on the org's
// worker).

// Row ids, in order.
const (
	RowReach     = "reach"
	RowAuth      = "auth"
	RowShape     = "shape"
	RowAnswer    = "answer"
	RowLatency   = "latency"
	RowState     = "state"
	RowIsolation = "isolation"
	RowVersion   = "version"
	RowTools     = "tools"
	RowGateway   = "gateway"
	RowRealTask  = "real_task"
)

// Row statuses.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusPass    = "pass"
	StatusWarn    = "warn"
	StatusFail    = "fail"
	StatusSkip    = "skip"
)

// VerifyRow is one row of a verification.
type VerifyRow struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Detail  string   `json:"detail,omitempty"`
	Failure *Failure `json:"failure,omitempty"`
	MS      int64    `json:"ms,omitempty"`
}

// Suggest is what the rows learned that the config should say.
type Suggest struct {
	// History is the conversation history mode the state probe found:
	// server (the agent keeps it by session id), client (it needs the
	// conversation in every request) or none (one-shot).
	History string `json:"history,omitempty"`
	// Sessions is false when the agent remembers nothing across turns:
	// conversation and memory benchmarks are left out.
	Sessions *bool `json:"sessions,omitempty"`
	// TimeoutS is a request timeout that fits the latency seen.
	TimeoutS int `json:"timeout_s,omitempty"`
	// Streams is true when the answer came as a stream.
	Streams bool `json:"streams,omitempty"`
}

// VerifyResult is the answer of Verify (and the agent.verify job result).
type VerifyResult struct {
	Agent   string      `json:"agent,omitempty"`
	OK      bool        `json:"ok"`
	Rows    []VerifyRow `json:"rows"`
	Suggest Suggest     `json:"suggest"`
	Version string      `json:"version,omitempty"`
	Nonce   string      `json:"nonce,omitempty"`
	Probes  []Probe     `json:"probes"`
}

// Row returns the row with id, or nil.
func (r *VerifyResult) Row(id string) *VerifyRow {
	for i := range r.Rows {
		if r.Rows[i].ID == id {
			return &r.Rows[i]
		}
	}
	return nil
}

// VerifyOptions configure Verify.
type VerifyOptions struct {
	Client  *http.Client
	Hosted  bool   // refuse private addresses and plain http
	Target  string // where the requests leave from, for messages
	Secrets map[string]string
	// Emit is called with every row change, for streamed rows.
	Emit func(rows []VerifyRow)
	// Dial opens the reach row's TCP connection; nil uses the guarded
	// dialer on hosted servers and a plain dialer otherwise.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Lookup resolves the reach row's host; nil uses the system resolver.
	Lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
	Now    func() time.Time
	// RealTask adds the pending real_task row the job fills in.
	RealTask bool
}

var rowTitles = map[string]string{
	RowReach: "Reach", RowAuth: "Sign-in", RowShape: "Request accepted", RowAnswer: "Answer found",
	RowLatency: "Latency", RowState: "Conversation memory", RowIsolation: "Sessions independent",
	RowVersion: "Version", RowTools: "Tools", RowGateway: "Model calls", RowRealTask: "One real task",
}

// RowIDs are the row ids in order, real task last.
var RowIDs = []string{RowReach, RowAuth, RowShape, RowAnswer, RowLatency, RowState, RowIsolation, RowVersion, RowTools, RowGateway, RowRealTask}

type verifier struct {
	ctx context.Context
	cfg *schemas.AgentConfig
	opt VerifyOptions
	res *VerifyResult
	snd *Sender
}

func (v *verifier) emit() {
	if v.opt.Emit != nil {
		v.opt.Emit(append([]VerifyRow(nil), v.res.Rows...))
	}
}

func (v *verifier) set(id, status, detail string, f *Failure, ms int64) {
	r := v.res.Row(id)
	r.Status, r.Detail, r.Failure, r.MS = status, detail, f, ms
	v.emit()
}

func (v *verifier) skipRest(from, why string) {
	skip := false
	for i := range v.res.Rows {
		if v.res.Rows[i].ID == from {
			skip = true
			continue
		}
		if skip && v.res.Rows[i].ID != RowRealTask && v.res.Rows[i].Status == StatusPending {
			v.res.Rows[i].Status, v.res.Rows[i].Detail = StatusSkip, why
		}
	}
	v.emit()
}

// endpointOf is the URL the config calls first.
func endpointOf(cfg *schemas.AgentConfig) string {
	switch {
	case cfg.OpenAI != nil && cfg.Adapter == "openai_agent":
		return OpenAIEndpoint(cfg.OpenAI.URL)
	case cfg.A2A != nil && cfg.Adapter == "a2a":
		return cfg.A2A.CardURL
	case cfg.Remote != nil:
		return cfg.Remote.URL
	}
	return ""
}

// Verify runs the rows for cfg. It never returns nil; OK is true when
// reach, sign-in, request and answer passed.
func Verify(ctx context.Context, cfg *schemas.AgentConfig, opt VerifyOptions) *VerifyResult {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Target == "" {
		opt.Target = "the shared worker"
	}
	if opt.Client == nil {
		opt.Client = &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res := &VerifyResult{Probes: []Probe{}}
	if cfg != nil {
		res.Agent = cfg.ID
	}
	for _, id := range RowIDs {
		if id == RowRealTask && !opt.RealTask {
			continue
		}
		res.Rows = append(res.Rows, VerifyRow{ID: id, Title: rowTitles[id], Status: StatusPending})
	}
	v := &verifier{ctx: ctx, cfg: cfg, opt: opt, res: res}
	v.emit()
	if cfg == nil {
		v.set(RowReach, StatusFail, "no agent config", nil, 0)
		v.skipRest(RowReach, "no agent config")
		return res
	}
	raw := endpointOf(cfg)
	if raw == "" {
		v.set(RowReach, StatusSkip, "Heimdall starts this agent in a container next to each task, so it has no endpoint to call: the real task row checks it.", nil, 0)
		v.skipRest(RowReach, "container agent: checked by the real task row")
		res.OK = true
		return res
	}
	v.snd = &Sender{Client: opt.Client, Config: cfg, Secrets: opt.Secrets, Now: opt.Now,
		Log: func(p Probe) { res.Probes = append(res.Probes, p) }}
	if !v.reach(raw) {
		v.skipRest(RowReach, "skipped: the endpoint is not reachable")
		return res
	}
	ex, ok := v.firstTurn()
	if !ok {
		return res
	}
	res.OK = true
	v.latency(ex)
	v.state()
	v.version(ex)
	v.toolsAndGateway()
	return res
}

func (v *verifier) reach(raw string) bool {
	start := v.opt.Now()
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		v.set(RowReach, StatusFail, "the URL is not a URL", nil, 0)
		return false
	}
	if err := webhooks.ValidateURL(raw, !v.opt.Hosted); err != nil {
		msg := strings.TrimPrefix(err.Error(), webhooks.ErrInvalid.Error()+": ")
		msg = strings.Replace(msg, "delivers to", "calls", 1)
		if v.opt.Hosted && (strings.Contains(msg, "private") || strings.Contains(msg, "localhost")) {
			f := Lookup(FailPrivateAddress, map[string]string{"host": u.Hostname(), "ip": u.Hostname(), "url": redactURL(raw), "fix": "heimdall serve " + redactURL(raw) + " --map auto"})
			v.set(RowReach, StatusFail, msg, &f, 0)
			return false
		}
		v.set(RowReach, StatusFail, msg, nil, 0)
		return false
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	if net.ParseIP(host) == nil {
		lookup := v.opt.Lookup
		if lookup == nil {
			lookup = net.DefaultResolver.LookupIPAddr
		}
		lctx, cancel := context.WithTimeout(v.ctx, 5*time.Second)
		ips, err := lookup(lctx, host)
		cancel()
		if err != nil || len(ips) == 0 {
			f := Lookup(FailDNS, map[string]string{"host": host, "target": v.opt.Target, "url": redactURL(raw), "fix": "heimdall serve " + redactURL(raw) + " --map auto"})
			v.set(RowReach, StatusFail, fmt.Sprintf("%s does not resolve", host), &f, v.opt.Now().Sub(start).Milliseconds())
			return false
		}
		if v.opt.Hosted {
			public := false
			for _, ip := range ips {
				if !blocked(ip.IP) {
					public = true
				}
			}
			if !public {
				f := Lookup(FailPrivateAddress, map[string]string{"host": host, "ip": ips[0].IP.String(), "url": redactURL(raw), "fix": "heimdall serve " + redactURL(raw) + " --map auto"})
				v.set(RowReach, StatusFail, host+" resolves to "+ips[0].IP.String(), &f, v.opt.Now().Sub(start).Milliseconds())
				return false
			}
		}
	}
	dial := v.opt.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		if v.opt.Hosted {
			d = webhooks.GuardedDialer(10 * time.Second)
		}
		dial = d.DialContext
	}
	dctx, cancel := context.WithTimeout(v.ctx, 10*time.Second)
	defer cancel()
	conn, err := dial(dctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		f, ok := ExplainRunError(err.Error())
		if !ok || f.Code == FailTimeout {
			f = Lookup(FailIPAllowlist, map[string]string{"source": v.opt.Target})
		}
		if f.Code == FailPrivateAddress {
			f = Lookup(FailPrivateAddress, map[string]string{"host": host, "ip": ipInText.FindString(err.Error()), "url": redactURL(raw), "fix": "heimdall serve " + redactURL(raw) + " --map auto"})
		}
		v.set(RowReach, StatusFail, "could not connect: "+capText(err.Error(), 160), &f, v.opt.Now().Sub(start).Milliseconds())
		return false
	}
	detail := "connected to " + net.JoinHostPort(host, port)
	if u.Scheme == "https" && !v.mutualTLS() {
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(dctx); err != nil {
			conn.Close()
			f := Lookup(FailTLS, map[string]string{"host": host, "detail": capText(err.Error(), 120)})
			v.set(RowReach, StatusFail, "TLS: "+capText(err.Error(), 160), &f, v.opt.Now().Sub(start).Milliseconds())
			return false
		}
		detail += ", TLS " + tlsVersion(tc.ConnectionState().Version)
		conn = tc
	}
	conn.Close()
	v.set(RowReach, StatusPass, detail+" from "+v.opt.Target, nil, v.opt.Now().Sub(start).Milliseconds())
	return true
}

// mutualTLS: a config with its own CA or client certificate is checked by
// the launcher's TLS settings, not the system roots.
func (v *verifier) mutualTLS() bool {
	r := v.cfg.Remote
	return r != nil && r.Auth != nil && (r.Auth.CACertSecret != "" || r.Auth.ClientCertSecret != "")
}

func tlsVersion(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	}
	return "1.x"
}

func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func (v *verifier) send(instr string, session string, index int, history []map[string]string) Exchange {
	ctx, cancel := context.WithTimeout(v.ctx, 110*time.Second)
	defer cancel()
	msgs := append(append([]map[string]string(nil), history...), map[string]string{"role": "user", "content": instr})
	return v.snd.Send(ctx, Vars{Instruction: instr, Messages: msgs, SessionID: session, SessionIndex: index, Model: v.cfg.Model})
}

func (v *verifier) secretName() string {
	switch {
	case v.cfg.Remote != nil && v.cfg.Remote.AuthSecret != "":
		return v.cfg.Remote.AuthSecret
	case v.cfg.OpenAI != nil && v.cfg.OpenAI.APIKeySecret != "":
		return v.cfg.OpenAI.APIKeySecret
	}
	return "AGENT_TOKEN"
}

// firstTurn sends the nonce and fills the auth, shape and answer rows.
func (v *verifier) firstTurn() (Exchange, bool) {
	nonce := NewNonce()
	v.res.Nonce = nonce
	prompt := NoncePrompt(nonce)
	for _, id := range []string{RowAuth, RowShape, RowAnswer} {
		v.res.Row(id).Status = StatusRunning
	}
	v.emit()
	ex := v.send(prompt, "heimdall-verify-"+newID(), 1, nil)
	ms := ex.TotalMS
	if ex.Err != nil {
		msg := scrub(ex.Err.Error(), v.opt.Secrets)
		if strings.Contains(msg, "auth is checked by the real task row") || strings.Contains(msg, "secret ") || strings.Contains(msg, "token endpoint") {
			v.set(RowAuth, StatusFail, msg, nil, ms)
			v.skipRest(RowAuth, "skipped: sign-in failed")
			return ex, false
		}
		f, ok := ExplainRunError(msg)
		var fp *Failure
		if ok {
			fp = &f
		}
		v.set(RowAuth, StatusSkip, "no answer to check", nil, 0)
		v.set(RowShape, StatusFail, "the endpoint did not answer: "+capText(msg, 200), fp, ms)
		v.skipRest(RowShape, "skipped: no answer")
		return ex, false
	}
	hdr := map[string]string{"x-amzn-errortype": ex.Header.Get("x-amzn-errortype"), "www-authenticate": ex.Header.Get("WWW-Authenticate")}
	vars := map[string]string{"secret": v.secretName(), "snippet": capText(string(ex.Body), 80), "fix": v.secretName(), "n": "1"}
	if ex.Status == 401 || ex.Status == 403 || hdr["x-amzn-errortype"] != "" {
		f, _ := statusFailure(ex.Status, hdr, ex.SentToken, vars)
		v.set(RowAuth, StatusFail, fmt.Sprintf("HTTP %d", ex.Status), &f, ms)
		v.skipRest(RowAuth, "skipped: sign-in failed")
		return ex, false
	}
	authDetail := "no sign-in needed"
	if ex.SentToken {
		authDetail = "token accepted"
	}
	v.set(RowAuth, StatusPass, authDetail, nil, ms)
	if ex.Status < 200 || ex.Status > 299 {
		var fp *Failure
		if f, ok := statusFailure(ex.Status, hdr, ex.SentToken, vars); ok {
			fp = &f
		} else if ex.Status == 404 || ex.Status == 405 {
			f := Lookup(FailPath, map[string]string{"method": ex.Method, "path": pathOf(ex.URL), "status": fmt.Sprint(ex.Status), "other": "nothing else", "kind": "a different route"})
			fp = &f
		} else if ex.Status == 400 || ex.Status == 422 {
			if field := fieldFromError(ex.Doc, ex.Body); field != "" {
				f := Lookup(FailField, map[string]string{"field": field, "type": "string", "fix": field})
				fp = &f
			}
		}
		v.set(RowShape, StatusFail, fmt.Sprintf("HTTP %d: %s", ex.Status, capText(string(ex.Body), 160)), fp, ms)
		v.skipRest(RowShape, "skipped: the request was refused")
		return ex, false
	}
	v.set(RowShape, StatusPass, fmt.Sprintf("HTTP %d", ex.Status), nil, ms)
	switch {
	case strings.Contains(ex.Answer, prompt):
		f := Lookup(FailEcho, nil)
		v.set(RowAnswer, StatusFail, "the answer is the prompt itself", &f, ms)
	case !ex.Found:
		f := Lookup(FailNoAnswer, map[string]string{"code": nonce})
		if ex.Doc != nil {
			_, _, f.Options = LocateNonce(ex.Doc, nonce, prompt)
		}
		v.set(RowAnswer, StatusFail, "the answer path matched nothing", &f, ms)
	case !strings.Contains(ex.Answer, nonce):
		f := Lookup(FailNoAnswer, map[string]string{"code": nonce})
		if ex.Doc != nil {
			path, _, fields := LocateNonce(ex.Doc, nonce, prompt)
			f.Options = fields
			if path != "" {
				f.Fix.Value = path
			}
		}
		v.set(RowAnswer, StatusFail, "answered: "+capText(ex.Answer, 160), &f, ms)
	default:
		v.set(RowAnswer, StatusPass, "the check code "+nonce+" came back", nil, ms)
		return ex, true
	}
	v.skipRest(RowAnswer, "skipped: the answer was not found")
	return ex, false
}

func (v *verifier) latency(ex Exchange) {
	detail := fmt.Sprintf("first byte %d ms, answer %d ms", ex.TTFBMS, ex.TotalMS)
	if ex.Streamed {
		detail += ", streamed"
		v.res.Suggest.Streams = true
	}
	st := StatusPass
	if ex.TotalMS > 30000 {
		st = StatusWarn
		detail += ": slow for a chat agent; long tasks may need a longer timeout"
	}
	t := int(ex.TotalMS/1000)*10 + 60
	if t > 900 {
		t = 900
	}
	v.res.Suggest.TimeoutS = t
	v.set(RowLatency, st, detail, nil, ex.TotalMS)
}

const recallQuestion = "What code did I ask you to remember? Reply with the code only."

// state decides the history mode: the agent keeps the conversation by
// session id (server), needs it in every request (client), or neither.
func (v *verifier) state() {
	if v.cfg.OpenAI != nil && v.cfg.Adapter == "openai_agent" {
		v.res.Suggest.History = "client"
		v.set(RowState, StatusPass, "chat completions send the whole conversation in every request (client history)", nil, 0)
		v.set(RowIsolation, StatusSkip, "no server-side memory to leak", nil, 0)
		return
	}
	v.res.Row(RowState).Status = StatusRunning
	v.emit()
	code := NewNonce()
	session := "heimdall-verify-" + newID()
	first := "Remember this code for later in our conversation: " + code + ". Reply with OK."
	t1 := v.send(first, session, 1, nil)
	if t1.Err != nil || t1.Status < 200 || t1.Status > 299 {
		v.set(RowState, StatusWarn, "the first turn failed, memory not checked", nil, t1.TotalMS)
		v.set(RowIsolation, StatusSkip, "memory not checked", nil, 0)
		return
	}
	t2 := v.send(recallQuestion, session, 2, nil)
	if t2.Err == nil && strings.Contains(t2.Answer, code) {
		v.res.Suggest.History = "server"
		yes := true
		v.res.Suggest.Sessions = &yes
		v.set(RowState, StatusPass, "remembered the code by session id alone (server history)", nil, t1.TotalMS+t2.TotalMS)
		v.isolation(code)
		return
	}
	hist := []map[string]string{{"role": "user", "content": first}, {"role": "assistant", "content": t1.Answer}}
	t3 := v.send(recallQuestion, session, 2, hist)
	if t3.Err == nil && strings.Contains(t3.Answer, code) {
		v.res.Suggest.History = "client"
		yes := true
		v.res.Suggest.Sessions = &yes
		v.set(RowState, StatusPass, "remembered the code when the conversation was sent (client history)", nil, t1.TotalMS+t2.TotalMS+t3.TotalMS)
		v.set(RowIsolation, StatusPass, "keeps no memory of its own between requests", nil, 0)
		return
	}
	v.res.Suggest.History = "none"
	no := false
	v.res.Suggest.Sessions = &no
	f := Lookup(FailOneShot, nil)
	v.set(RowState, StatusWarn, "remembered nothing, with or without the conversation", &f, t1.TotalMS+t2.TotalMS+t3.TotalMS)
	v.set(RowIsolation, StatusSkip, "one-shot agent", nil, 0)
}

func (v *verifier) isolation(code string) {
	ex := v.send(recallQuestion, "heimdall-verify-"+newID(), 1, nil)
	if ex.Err != nil || ex.Status < 200 || ex.Status > 299 {
		v.set(RowIsolation, StatusWarn, "the check failed, isolation not known", nil, ex.TotalMS)
		return
	}
	if strings.Contains(ex.Answer, code) {
		field := "user_id"
		if v.cfg.Remote != nil && v.cfg.Remote.Mapping != nil && v.cfg.Remote.Mapping.Body != "" {
			for k := range userFields {
				if strings.Contains(v.cfg.Remote.Mapping.Body, `"`+k+`"`) {
					field = k
				}
			}
		}
		f := Lookup(FailMemoryLeak, map[string]string{"field": field, "fix": field})
		v.set(RowIsolation, StatusFail, "a new session recalled the code of another session", &f, ex.TotalMS)
		return
	}
	v.set(RowIsolation, StatusPass, "a new session did not know the code", nil, ex.TotalMS)
}

func (v *verifier) version(ex Exchange) {
	if ex.Version != "" {
		v.res.Version = ex.Version
		v.set(RowVersion, StatusPass, "the agent reports version "+capText(ex.Version, 64), nil, 0)
		return
	}
	v.set(RowVersion, StatusWarn, "no X-Agent-Version header or version field: each evaluation records the config version only", nil, 0)
}

func (v *verifier) toolsAndGateway() {
	tools := false
	switch {
	case v.cfg.Remote != nil:
		tools = v.cfg.Remote.Tools == schemas.RemoteToolsExpose
	case v.cfg.A2A != nil:
		tools = v.cfg.A2A.Tools == schemas.RemoteToolsExpose
	case v.cfg.OpenAI != nil:
		tools = v.cfg.OpenAI.Tools != "off"
	}
	if tools {
		v.set(RowTools, StatusSkip, "tools are published only during a run: the real task row checks that a call reaches the task's tools", nil, 0)
	} else {
		v.set(RowTools, StatusSkip, "tool access is off: benchmarks that need tools are left out", nil, 0)
	}
	if v.cfg.Remote != nil && v.cfg.Remote.ModelTraffic == "gateway" {
		v.set(RowGateway, StatusSkip, "the run key exists only during a run: the real task row checks that a model call reaches Heimdall", nil, 0)
		return
	}
	if v.cfg.OpenAI != nil && v.cfg.Adapter == "openai_agent" {
		v.set(RowGateway, StatusSkip, "your endpoint picks its own model; Heimdall measures the calls it makes to it", nil, 0)
		return
	}
	v.set(RowGateway, StatusSkip, "your agent calls its own model provider: cost and tokens are what it reports", nil, 0)
}
