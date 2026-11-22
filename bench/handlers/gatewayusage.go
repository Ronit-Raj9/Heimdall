package handlers

// Gateway usage ingest (lane C03). The heimdall Bifrost plugin
// (plugins/heimdall/usage.go) posts one row per model call that went through
// Bifrost: run id, provider, model, tokens, cost, latency, cache hit,
// fallback used and fault applied. The push is machine to machine: the
// plugin sends the shared secret from HEIMDALL_GATEWAY_USAGE_SECRET as a
// bearer token, so the route is public to the org guard and the handler
// checks the secret itself. Rows are kept per run in memory, bounded, for
// the run page's Bifrost panel; the run's own gateway log stays the record
// of truth.

import (
	"crypto/subtle"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// GatewayUsagePath is the ingest (POST) and read (GET ?run_id=) route.
const GatewayUsagePath = APIPrefix + "/gateway/usage"

// GatewayUsageSecretEnv names the shared secret the plugin sends.
const GatewayUsageSecretEnv = "HEIMDALL_GATEWAY_USAGE_SECRET"

// Store bounds: a run past maxGatewayUsagePerRun keeps its first rows and
// counts the rest as dropped; past maxGatewayUsageRuns the oldest run goes.
const (
	maxGatewayUsageBatch  = 1000
	maxGatewayUsagePerRun = 5000
	maxGatewayUsageRuns   = 2000
	maxGatewayUsageIDLen  = 200
)

// GatewayUsageRecord is one model call as the plugin reports it. It mirrors
// plugins/heimdall.UsageRecord (a separate module, so not imported).
type GatewayUsageRecord struct {
	RunID        string    `json:"run_id"`
	EvaluationID string    `json:"evaluation_id,omitempty"`
	OrgID        string    `json:"org_id,omitempty"`
	Arm          string    `json:"arm,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	LatencyMS    int64     `json:"latency_ms"`
	CacheHit     bool      `json:"cache_hit"`
	FallbackUsed bool      `json:"fallback_used"`
	FallbackFrom string    `json:"fallback_from,omitempty"`
	FaultApplied string    `json:"fault_applied,omitempty"`
	FaultPlanID  string    `json:"fault_plan_id,omitempty"`
	StatusCode   int       `json:"status_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	StartedAt    time.Time `json:"started_at"`
}

// GatewayUsageBatch is the POST body.
type GatewayUsageBatch struct {
	Records []GatewayUsageRecord `json:"records"`
}

// GatewayUsageAccepted answers POST.
type GatewayUsageAccepted struct {
	Accepted int `json:"accepted"`
	Dropped  int `json:"dropped"`
}

// GatewayUsageSummary totals a run's rows.
type GatewayUsageSummary struct {
	Calls         int     `json:"calls"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	TotalTokens   int     `json:"total_tokens"`
	CostUSD       float64 `json:"cost_usd"`
	CacheHits     int     `json:"cache_hits"`
	Fallbacks     int     `json:"fallbacks"`
	Faults        int     `json:"faults"`
	Errors        int     `json:"errors"`
	MeanLatencyMS int64   `json:"mean_latency_ms"`
}

// GatewayUsageRun answers GET ?run_id=.
type GatewayUsageRun struct {
	RunID   string               `json:"run_id"`
	Summary GatewayUsageSummary  `json:"summary"`
	Records []GatewayUsageRecord `json:"records"`
	Dropped int                  `json:"dropped,omitempty"`
}

type gatewayUsageStore struct {
	mu      sync.Mutex
	runs    map[string][]GatewayUsageRecord
	dropped map[string]int
	order   []string
}

var gatewayUsage = &gatewayUsageStore{runs: map[string][]GatewayUsageRecord{}, dropped: map[string]int{}}

// GatewayUsageForRun returns the run's rows and totals as reported by the
// heimdall plugin; ok is false when the plugin reported nothing for it.
func GatewayUsageForRun(runID string) (GatewayUsageRun, bool) {
	return gatewayUsage.get(runID, "")
}

func init() {
	RegisterOrgArea("gatewayusage", func(env OrgEnv, r *router.Router) {
		h := &gatewayUsageHandler{env: env, getenv: os.Getenv, store: gatewayUsage}
		r.POST(GatewayUsagePath, env.R(h.ingest))
		r.GET(GatewayUsagePath, env.R(h.read))
	})
}

// GatewayUsageIngest serves the POST on the hosted platform router, where
// the plugin has no org session: the shared secret is the only credential
// and each row carries its org id.
type GatewayUsageIngest struct{ h *gatewayUsageHandler }

// NewGatewayUsageIngest builds the platform ingest handler.
func NewGatewayUsageIngest(mode schemas.Mode) *GatewayUsageIngest {
	return &GatewayUsageIngest{h: &gatewayUsageHandler{env: OrgEnv{Mode: mode}, getenv: os.Getenv, store: gatewayUsage}}
}

// RegisterRoutes registers POST /gateway/usage.
func (g *GatewayUsageIngest) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(GatewayUsagePath, lib.ChainMiddlewares(g.h.ingest, middlewares...))
}

type gatewayUsageHandler struct {
	env    OrgEnv
	getenv func(string) string
	store  *gatewayUsageStore
}

func (h *gatewayUsageHandler) ingest(ctx *fasthttp.RequestCtx) {
	secret := h.getenv(GatewayUsageSecretEnv)
	if secret == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"gateway usage ingest is off: set "+GatewayUsageSecretEnv+" on the server and in the heimdall plugin config")
		return
	}
	got, _ := strings.CutPrefix(string(ctx.Request.Header.Peek("Authorization")), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized,
			"gateway usage ingest needs Authorization: Bearer <shared secret>")
		return
	}
	var in GatewayUsageBatch
	if !decodeStrict(ctx, h.env.Mode, &in, "gateway usage batch") {
		return
	}
	if len(in.Records) > maxGatewayUsageBatch {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest,
			"at most 1000 records per batch")
		return
	}
	for _, r := range in.Records {
		if r.RunID == "" || len(r.RunID) > maxGatewayUsageIDLen || len(r.OrgID) > maxGatewayUsageIDLen {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
				"every record needs a run_id of at most 200 characters")
			return
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.store.add(in.Records))
}

func (h *gatewayUsageHandler) read(ctx *fasthttp.RequestCtx) {
	runID := string(ctx.QueryArgs().Peek("run_id"))
	if runID == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "run_id is required")
		return
	}
	orgID := ""
	if o, ok := tenancy.OrgFrom(ctx); ok {
		orgID = o.ID
	} else if h.env.Services.Hosted {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "request not resolved to an organization")
		return
	}
	out, ok := h.store.get(runID, orgID)
	if !ok {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
			"no Bifrost usage reported for this run (the heimdall plugin is off or the run made no model calls)")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (s *gatewayUsageStore) add(recs []GatewayUsageRecord) GatewayUsageAccepted {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out GatewayUsageAccepted
	for _, r := range recs {
		cur, seen := s.runs[r.RunID]
		if !seen {
			s.order = append(s.order, r.RunID)
			for len(s.order) > maxGatewayUsageRuns {
				old := s.order[0]
				s.order = s.order[1:]
				delete(s.runs, old)
				delete(s.dropped, old)
			}
		}
		if len(cur) >= maxGatewayUsagePerRun {
			s.dropped[r.RunID]++
			out.Dropped++
			if !seen {
				s.runs[r.RunID] = cur
			}
			continue
		}
		s.runs[r.RunID] = append(cur, r)
		out.Accepted++
	}
	return out
}

// get returns a copy of the run's rows. orgID non-empty hides rows tagged
// with another organization, so one org never reads another's usage.
func (s *gatewayUsageStore) get(runID, orgID string) (GatewayUsageRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, ok := s.runs[runID]
	if !ok {
		return GatewayUsageRun{}, false
	}
	out := GatewayUsageRun{RunID: runID, Records: []GatewayUsageRecord{}, Dropped: s.dropped[runID]}
	var latency int64
	for _, r := range rows {
		if orgID != "" && r.OrgID != orgID {
			continue
		}
		out.Records = append(out.Records, r)
		sm := &out.Summary
		sm.Calls++
		sm.InputTokens += r.InputTokens
		sm.OutputTokens += r.OutputTokens
		sm.TotalTokens += r.TotalTokens
		sm.CostUSD += r.CostUSD
		latency += r.LatencyMS
		if r.CacheHit {
			sm.CacheHits++
		}
		if r.FallbackUsed {
			sm.Fallbacks++
		}
		if r.FaultApplied != "" {
			sm.Faults++
		}
		if r.Error != "" || r.StatusCode >= 400 {
			sm.Errors++
		}
	}
	if out.Summary.Calls == 0 {
		return GatewayUsageRun{}, false
	}
	out.Summary.MeanLatencyMS = latency / int64(out.Summary.Calls)
	return out, true
}
