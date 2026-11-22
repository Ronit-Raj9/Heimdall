package handlers

// Connect from traces (lane Z15, INFRA_I2 4.6 and 5.5): an org points its
// OpenTelemetry exporter at Heimdall, the first traces arrive, and Heimdall
// proposes the agent they describe (name, framework, model, tools seen, the
// route it is served on) as a draft for POST /agents/from-template and as a
// .heimdall/agent.yaml. Hosted, the ingest is
// POST /api/bench/orgs/{org}/otlp/v1/traces with one of the org's ingest
// tokens (the inbox tokens, hmi_...); local mode takes
// POST /api/bench/otlp/v1/traces with the write token. Only what inference
// reads is kept (agentinfer.Compact): never prompts, answers or tool
// arguments.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentevents"
	"github.com/maximhq/bifrost/bench/agentinfer"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// Routes.
const (
	// OTLPOrgIngestPath is the hosted OTLP/HTTP traces endpoint of one org.
	OTLPOrgIngestPath = APIPrefix + "/orgs/{org}/otlp/v1/traces"
	// OTLPLocalIngestPath is the local-mode OTLP/HTTP traces endpoint.
	OTLPLocalIngestPath = APIPrefix + "/otlp/v1/traces"
	// ConnectTracesPath is what the org's traces show so far.
	ConnectTracesPath = APIPrefix + "/connect/traces"
	// ConnectTracesInferPath infers an agent from one posted export.
	ConnectTracesInferPath = APIPrefix + "/connect/traces/infer"
	// AgentFileCheckPath checks a .heimdall/agent.yaml.
	AgentFileCheckPath = APIPrefix + "/connect/agent-file/check"
)

// OTLPMaxBytes caps one OTLP export request.
const OTLPMaxBytes = 4 << 20

// OTLPRatePerMinute caps the exports per org per minute.
const OTLPRatePerMinute = 120

// OTLPOrgIngestURL is the ingest path of one org.
func OTLPOrgIngestURL(orgID string) string {
	return strings.Replace(OTLPOrgIngestPath, "{org}", orgID, 1)
}

// TraceConnectInfo answers GET /connect/traces.
type TraceConnectInfo struct {
	// Endpoint is the full traces URL to export to; Base is the same
	// without /v1/traces, for SDKs that append it (OpenLLMetry).
	Endpoint string `json:"endpoint"`
	Base     string `json:"base"`
	Hosted   bool   `json:"hosted"`
	// TokenHint says which credential goes in the Authorization header.
	TokenHint string `json:"token_hint"`
	// Setup is copy-ready configuration per instrumentation.
	Setup []TraceSetup `json:"setup"`

	Exports       int       `json:"exports"`
	SpansKept     int       `json:"spans_kept"`
	SpansDropped  int       `json:"spans_dropped,omitempty"`
	FirstReceived time.Time `json:"first_received,omitzero"`
	LastReceived  time.Time `json:"last_received,omitzero"`

	// Draft is nil until a span arrived.
	Draft     *agentinfer.Draft `json:"draft,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	AgentYAML string            `json:"agent_yaml,omitempty"`
	// Request is the draft as the POST /agents/from-template body.
	Request *agentinfer.FromTemplate `json:"request,omitempty"`
}

// TraceSetup is the configuration for one instrumentation.
type TraceSetup struct {
	ID      string `json:"id"` // otel | openinference | openllmetry | logfire
	Label   string `json:"label"`
	Install string `json:"install,omitempty"`
	Env     string `json:"env"`
	Code    string `json:"code,omitempty"`
}

// TraceInferResult answers POST /connect/traces/infer.
type TraceInferResult struct {
	Draft     agentinfer.Draft         `json:"draft"`
	Summary   string                   `json:"summary"`
	AgentYAML string                   `json:"agent_yaml"`
	Request   *agentinfer.FromTemplate `json:"request,omitempty"`
}

// TracesForgotten answers DELETE /connect/traces.
type TracesForgotten struct {
	Forgotten bool `json:"forgotten"`
}

// AgentFileCheckRequest is the body of POST /connect/agent-file/check.
type AgentFileCheckRequest struct {
	YAML string `json:"yaml"`
}

// AgentFileCheckResult says whether an agent file is valid and what it
// becomes: a from-template request, or a full config for PUT /agents/{id}/config.
type AgentFileCheckResult struct {
	OK         bool                     `json:"ok"`
	Error      string                   `json:"error,omitempty"`
	File       *agentinfer.AgentFile    `json:"file,omitempty"`
	Request    *agentinfer.FromTemplate `json:"request,omitempty"`
	ConfigYAML string                   `json:"config_yaml,omitempty"`
}

// OTLPExportResult is the JSON answer to an accepted export (the OTLP
// ExportTraceServiceResponse, with Heimdall's counts beside it).
type OTLPExportResult struct {
	PartialSuccess *OTLPPartialSuccess `json:"partialSuccess,omitempty"`
	Accepted       int                 `json:"accepted"`
}

// OTLPPartialSuccess is the OTLP partial success field.
type OTLPPartialSuccess struct {
	RejectedSpans int    `json:"rejectedSpans,omitempty"`
	ErrorMessage  string `json:"errorMessage,omitempty"`
}

func init() {
	RegisterOrgArea("connecttraces", func(env OrgEnv, r *router.Router) {
		h := &connectTracesHandler{env: env}
		r.GET(ConnectTracesPath, env.R(h.get))
		r.DELETE(ConnectTracesPath, env.W(h.forget))
		r.POST(ConnectTracesInferPath, env.U(h.infer))
		r.POST(AgentFileCheckPath, env.W(h.checkFile))
		r.POST(OTLPLocalIngestPath, env.U(h.localIngest))
	})
}

type connectTracesHandler struct{ env OrgEnv }

func (h *connectTracesHandler) store() agentinfer.Store {
	return agentinfer.NewStore(h.env.Cfg.Root)
}

func (h *connectTracesHandler) get(ctx *fasthttp.RequestCtx) {
	st, err := h.store().Load()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "trace store", err)
		return
	}
	info := TraceConnectInfo{Hosted: h.env.Services.Hosted, Exports: st.Exports, SpansKept: len(st.Spans), SpansDropped: st.Dropped,
		FirstReceived: st.FirstSeen, LastReceived: st.LastSeen}
	base := strings.TrimSuffix(ingestURL(ctx), APIPrefix+"/inbox/ingest")
	if info.Hosted && h.env.Services.OrgID != "" {
		info.Endpoint = base + OTLPOrgIngestURL(h.env.Services.OrgID)
		info.TokenHint = "an ingest token of this org (hmi_...): Inbox, Push failures in, Generate token"
	} else {
		info.Endpoint = base + OTLPLocalIngestPath
		info.TokenHint = "the server's write token"
	}
	info.Base = strings.TrimSuffix(info.Endpoint, "/v1/traces")
	info.Setup = traceSetup(info.Endpoint, info.Base)
	if len(st.Spans) > 0 {
		d := agentinfer.InferAgent(st.Spans)
		info.Draft = &d
		info.Summary = d.Summary()
		info.AgentYAML, info.Request = draftOutputs(d)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, info)
}

// draftOutputs renders a draft as agent.yaml text and, when it names a
// template, the from-template request.
func draftOutputs(d agentinfer.Draft) (string, *agentinfer.FromTemplate) {
	f := agentinfer.FileFromDraft(d)
	y, _ := f.Marshal(d.Questions...)
	if d.Template == "" {
		return string(y), nil
	}
	req, err := f.ToRequest()
	if err != nil {
		return string(y), nil
	}
	return string(y), &req
}

// traceSetup is the copy-ready configuration of each instrumentation. The
// token is a placeholder; the page fills it after creating one.
func traceSetup(endpoint, base string) []TraceSetup {
	tok := "$HEIMDALL_INGEST_TOKEN"
	otelEnv := "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=" + endpoint + "\n" +
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=Bearer%20" + tok + "\n" +
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf\n" +
		"OTEL_SEMCONV_STABILITY_OPT_IN=gen_ai_latest_experimental"
	return []TraceSetup{
		{ID: "otel", Label: "OpenTelemetry SDK", Install: "pip install opentelemetry-sdk opentelemetry-exporter-otlp-proto-http", Env: otelEnv},
		{ID: "openinference", Label: "OpenInference (LangChain, LangGraph, CrewAI, LlamaIndex, OpenAI Agents SDK)",
			Install: "pip install openinference-instrumentation-langchain opentelemetry-sdk opentelemetry-exporter-otlp-proto-http",
			Env:     otelEnv,
			Code: "from opentelemetry.sdk.trace import TracerProvider\nfrom opentelemetry.sdk.trace.export import BatchSpanProcessor\n" +
				"from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter\n" +
				"from openinference.instrumentation.langchain import LangChainInstrumentor\n\n" +
				"provider = TracerProvider()\nprovider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))\n" +
				"LangChainInstrumentor().instrument(tracer_provider=provider)"},
		{ID: "openllmetry", Label: "OpenLLMetry (Traceloop SDK)", Install: "pip install traceloop-sdk",
			Env:  "TRACELOOP_BASE_URL=" + base + "\nTRACELOOP_HEADERS=Authorization=Bearer%20" + tok,
			Code: "from traceloop.sdk import Traceloop\nTraceloop.init(app_name=\"my-agent\")"},
		{ID: "logfire", Label: "Pydantic Logfire", Install: "pip install logfire", Env: otelEnv,
			Code: "import logfire\nlogfire.configure(send_to_logfire=False)\nlogfire.instrument_pydantic_ai()"},
	}
}

func (h *connectTracesHandler) forget(ctx *fasthttp.RequestCtx) {
	if err := h.store().Clear(); err != nil {
		sendDepError(ctx, h.env.Mode, "trace store", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, TracesForgotten{Forgotten: true})
}

func (h *connectTracesHandler) infer(ctx *fasthttp.RequestCtx) {
	body := ctx.PostBody()
	if len(body) == 0 {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "post one OTLP trace export (JSON or protobuf)")
		return
	}
	if len(body) > OTLPMaxBytes {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "export larger than 4 MiB; post fewer spans")
		return
	}
	d, err := agentinfer.InferFromOTLP(body, string(ctx.Request.Header.ContentType()))
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, otlpFileError(body, err))
		return
	}
	y, req := draftOutputs(d)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, TraceInferResult{Draft: d, Summary: d.Summary(), AgentYAML: y, Request: req})
}

func (h *connectTracesHandler) checkFile(ctx *fasthttp.RequestCtx) {
	var req AgentFileCheckRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, CheckAgentFile([]byte(req.YAML)))
}

// CheckAgentFile parses an agent file and says what it becomes.
func CheckAgentFile(b []byte) AgentFileCheckResult {
	f, err := agentinfer.ParseAgentFile(b)
	if err != nil {
		return AgentFileCheckResult{Error: err.Error()}
	}
	out := AgentFileCheckResult{OK: true, File: f}
	if len(f.Config) > 0 {
		y, err := f.ConfigYAML()
		if err != nil {
			return AgentFileCheckResult{Error: err.Error()}
		}
		out.ConfigYAML = y
		return out
	}
	r, err := f.ToRequest()
	if err != nil {
		return AgentFileCheckResult{Error: err.Error()}
	}
	out.Request = &r
	return out
}

func (h *connectTracesHandler) localIngest(ctx *fasthttp.RequestCtx) {
	if h.env.Mode != schemas.ModeLive {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "trace ingest is refused in "+string(h.env.Mode)+" mode")
		return
	}
	if len(ctx.PostBody()) > OTLPMaxBytes {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "export larger than 4 MiB; lower the batch size")
		return
	}
	acceptOTLP(ctx, h.env.Mode, h.store(), time.Now())
}

// acceptOTLP decodes one export, keeps its spans and answers as an OTLP
// collector does: an empty protobuf ExportTraceServiceResponse for a
// protobuf request, JSON otherwise.
func acceptOTLP(ctx *fasthttp.RequestCtx, mode schemas.Mode, st agentinfer.Store, now time.Time) {
	ct := string(ctx.Request.Header.ContentType())
	spans, err := agentevents.DecodeSpans(ctx.PostBody(), ct)
	if err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, otlpFileError(ctx.PostBody(), err))
		return
	}
	if len(spans) > 0 {
		if _, err := st.Add(spans, now); err != nil {
			sendDepError(ctx, mode, "trace store", err)
			return
		}
	}
	if strings.Contains(strings.ToLower(ct), "protobuf") {
		ctx.SetContentType("application/x-protobuf")
		ctx.SetStatusCode(fasthttp.StatusOK)
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, OTLPExportResult{Accepted: len(spans)})
}

// OTLPOrgRoot opens the project root of an org for its trace store.
type OTLPOrgRoot func(ctx context.Context, org tenancy.Org) (string, error)

// OTLPIngestHandler serves the hosted per-org OTLP ingest route. Tokens
// are the org's inbox ingest tokens.
type OTLPIngestHandler struct {
	mode      schemas.Mode
	tokens    inboxtoken.Store
	tokensErr string
	dir       tenancy.Directory
	root      OTLPOrgRoot
	now       func() time.Time

	RatePerMinute int
	MaxBytes      int64

	mu    sync.Mutex
	gates map[string]*traceinbox.Gate
}

// NewOTLPIngestHandler builds the hosted ingest handler.
func NewOTLPIngestHandler(mode schemas.Mode, tokens inboxtoken.Store, tokensErr string, dir tenancy.Directory, root OTLPOrgRoot) *OTLPIngestHandler {
	return &OTLPIngestHandler{mode: mode, tokens: tokens, tokensErr: tokensErr, dir: dir, root: root, now: time.Now,
		RatePerMinute: OTLPRatePerMinute, MaxBytes: OTLPMaxBytes, gates: map[string]*traceinbox.Gate{}}
}

// RegisterRoutes registers POST /orgs/{org}/otlp/v1/traces. Its body limit
// is OTLPMaxBytes instead of the platform default (the last middleware).
func (h *OTLPIngestHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	mw := middlewares
	if len(mw) > 0 {
		mw = append(append([]lib.Middleware{}, mw[:len(mw)-1]...), lib.BodyLimit(h.mode, OTLPMaxBytes))
	}
	r.POST(OTLPOrgIngestPath, lib.ChainMiddlewares(h.ingest, mw...))
}

func (h *OTLPIngestHandler) unauthorized(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("WWW-Authenticate", `Bearer realm="heimdall-otlp"`)
	lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized,
		"the traces endpoint needs one of this org's ingest tokens: OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=Bearer%20hmi_... (Agents, Connect from traces)")
}

// ingest: token, size, rate, then the spans land in the org's store.
func (h *OTLPIngestHandler) ingest(ctx *fasthttp.RequestCtx) {
	if h.mode != schemas.ModeLive {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "trace ingest is refused in "+string(h.mode)+" mode")
		return
	}
	if h.tokens == nil || h.dir == nil || h.root == nil {
		msg := "the traces endpoint is not configured on this server: it needs the server database"
		if h.tokensErr != "" {
			msg += " (" + h.tokensErr + ")"
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, msg)
		return
	}
	raw := strings.TrimSpace(string(ctx.Request.Header.Peek("Authorization")))
	raw, hasBearer := strings.CutPrefix(raw, "Bearer ")
	raw = strings.TrimSpace(raw)
	if dec, err := url.PathUnescape(raw); err == nil { // a header copied with %20 still encoded
		raw = dec
	}
	ref := pathParam(ctx, tenancy.OrgPathParam)
	if !hasBearer || raw == "" || ref == "" {
		h.unauthorized(ctx)
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	org, err := h.dir.OrgByID(work, ref)
	if err != nil {
		org, err = h.dir.OrgBySlug(work, ref)
	}
	if err != nil {
		h.unauthorized(ctx)
		return
	}
	if _, err := h.tokens.Verify(work, org.ID, raw, h.now()); err != nil {
		if !errors.Is(err, inboxtoken.ErrNotFound) {
			sendDepError(ctx, h.mode, "ingest token", err)
			return
		}
		h.unauthorized(ctx)
		return
	}
	if int64(len(ctx.PostBody())) > h.MaxBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "export larger than 4 MiB; lower the exporter's batch size")
		return
	}
	if !h.gate(org.ID).Allow(h.now()) {
		ctx.Response.Header.Set("Retry-After", "60")
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeUnavailable, "this org's traces rate limit was reached; retry later")
		return
	}
	root, err := h.root(work, org)
	if err != nil {
		sendDepError(ctx, h.mode, "open the org's project", err)
		return
	}
	acceptOTLP(ctx, h.mode, agentinfer.NewStore(root), h.now())
}

func (h *OTLPIngestHandler) gate(orgID string) *traceinbox.Gate {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.gates[orgID]
	if !ok {
		g = traceinbox.NewGate("", h.RatePerMinute, h.MaxBytes)
		h.gates[orgID] = g
	}
	return g
}

// otlpFileErrorHint ends every otlpFileError sentence.
const otlpFileErrorHint = " Export your traces as OTLP JSON, for example with the OpenTelemetry Collector file exporter, and upload that file."

// otlpFileError is the one plain sentence shown for a body that is not an
// OTLP trace export: broken JSON (with the line and column of the first
// problem), JSON without resourceSpans, or a body neither encoding reads.
// The decoder's own text never reaches the user.
func otlpFileError(body []byte, err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		msg := "This file is not an OTLP trace export: it is not valid JSON."
		if line, col, ok := otlpLineCol(body, syn.Offset); ok {
			msg += fmt.Sprintf(" The first problem is at line %d, column %d.", line, col)
		}
		return msg + otlpFileErrorHint
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "This file is not an OTLP trace export: it is not valid JSON (it ends too early)." + otlpFileErrorHint
	case errors.Is(err, agentevents.ErrNoResourceSpans), errors.As(err, &typ):
		return "This file is valid JSON but has no resourceSpans, so it is not an OTLP trace export." + otlpFileErrorHint
	}
	return "This file could not be read as an OTLP trace export (JSON or protobuf)." + otlpFileErrorHint
}

// otlpLineCol turns a json.SyntaxError offset (bytes read, the bad byte
// included) into a 1-based line and column.
func otlpLineCol(body []byte, offset int64) (line, col int, ok bool) {
	if offset < 1 || offset > int64(len(body)) {
		return 0, 0, false
	}
	line, col = 1, 1
	for _, b := range body[:offset-1] {
		if b == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col, true
}
