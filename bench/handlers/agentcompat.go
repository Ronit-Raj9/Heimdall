package handlers

// Agent compatibility (agent-first contract, section 1): which benchmarks an
// agent can run. GET /agents/{id}/compatibility checks the agent's
// capabilities (its capabilities: list or its adapter's defaults) against
// every benchmark the org sees, built-in library benchmarks included, with
// compat.Full: the planner's capability rule (compat.Check) plus the output
// rules it applies per task to agents outside the workspace.
//
// What a benchmark needs comes from suite.yaml alone (suite.Requires reads
// requires, track and environment), so the check reads each pack's head
// (suite.yaml and suite.lock, cached by stamp) instead of loading every task,
// and the whole answer is cached per agent config hash and pack stamps. Only
// an agent outside the workspace (compat.ChecksTasks) needs the output rules
// per task, so for it the full pack is loaded, cached by stamp the same way.
//
// A benchmark that runs inside each task's own environment (a snapshot
// verifier on tasks with their own image, every Harbor import) also needs an
// agent that can be rebuilt on the task image. The answer runs the same check
// as the estimate and the launch (deriveTaskImageAgent), so the wizard never
// offers a benchmark the evaluation would then skip.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// BenchmarkCompatibility is one benchmark's row of AgentCompatibility.
type BenchmarkCompatibility struct {
	ID       string   `json:"id"`
	OK       bool     `json:"ok"`
	Requires []string `json:"requires"`
	Missing  []string `json:"missing"`
	Reason   string   `json:"reason"`
	// Note says how many tasks the agent skips when it can run only some
	// (an endpoint without tool access on a mixed benchmark). "" otherwise.
	Note string `json:"note,omitempty"`
}

// AgentCompatibility is GET /agents/{id}/compatibility.
type AgentCompatibility struct {
	Agent        string                   `json:"agent"`
	Capabilities []string                 `json:"capabilities"`
	Defaulted    bool                     `json:"defaulted"` // the agent lists no capabilities: of its own
	Benchmarks   []BenchmarkCompatibility `json:"benchmarks"`
}

type agentCompatHandler struct {
	env   OrgEnv
	mu    sync.Mutex
	cache map[string]*AgentCompatibility // key: agent config hash and every pack stamp
}

// compatCacheMax bounds the answers kept per org; past it the cache restarts.
const compatCacheMax = 256

func init() {
	RegisterOrgArea("agentcompat", registerAgentCompat)
}

// registerAgentCompat registers GET /agents/{id}/compatibility.
func registerAgentCompat(env OrgEnv, r *router.Router) {
	h := &agentCompatHandler{env: env, cache: map[string]*AgentCompatibility{}}
	r.GET(APIPrefix+"/agents/{id}/compatibility", env.R(h.get))
}

func (h *agentCompatHandler) fail(ctx *fasthttp.RequestCtx, status int, typ, msg string) {
	lib.SendError(ctx, h.env.Mode, status, typ, msg)
}

func (h *agentCompatHandler) get(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if !agentOpsNamePattern.MatchString(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", id))
		return
	}
	src := h.env.Services.Suites
	if src == nil {
		sendUnavailable(ctx, h.env.Mode, "benchmarks")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	cfg, err := h.agentConfig(ctx, id)
	if err != nil {
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	ids, err := src.SuiteIDs(work)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "list benchmarks", err)
		return
	}
	key := compatKey(cfg, src, ids)
	if key != "" {
		h.mu.Lock()
		hit := h.cache[key]
		h.mu.Unlock()
		if hit != nil {
			lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, hit)
			return
		}
	}
	out := AgentCompatibility{
		Agent:        cfg.ID,
		Capabilities: schemas.EffectiveCapabilities(cfg).Strings(),
		Defaulted:    len(cfg.Capabilities) == 0,
		Benchmarks:   make([]BenchmarkCompatibility, 0, len(ids)),
	}
	complete := true
	var agents map[string]*schemas.AgentConfig // loaded on the first benchmark that needs the task image check
	for _, sid := range ids {
		row := BenchmarkCompatibility{ID: sid, Requires: []string{}, Missing: []string{}}
		s, err := compatSuite(work, src, sid, compat.ChecksTasks(cfg))
		if err != nil || s == nil {
			row.Reason = "could not load this benchmark"
			out.Benchmarks = append(out.Benchmarks, row)
			complete = false
			continue
		}
		row.Requires = suite.Requires(s).Strings()
		ok, missing, reason, note := compat.Full(cfg, s)
		row.OK, row.Reason, row.Note = ok, reason, note
		if ok && taskImageCheckApplies(cfg, s) {
			if agents == nil {
				agents = (&evaluationsHandler{env: h.env}).agentConfigs(work)
			}
			why, err := h.taskImageReason(work, src, sid, s, cfg, agents)
			if err != nil {
				complete = false
			} else if why != "" {
				row.OK, row.Reason, row.Note = false, why, ""
			}
		}
		row.Missing = schemas.CapabilitySet(missing).Strings()
		out.Benchmarks = append(out.Benchmarks, row)
	}
	if key != "" && complete {
		h.mu.Lock()
		if len(h.cache) >= compatCacheMax {
			h.cache = map[string]*AgentCompatibility{}
		}
		h.cache[key] = &out
		h.mu.Unlock()
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, &out)
}

// taskImageCheckApplies reports whether s may need cfg rebuilt on each
// task's own image: its verifier grades a snapshot of the agent's container,
// and cfg is not already built on a task image (scripted, or build.base_arg).
// It reads the head only, so a pack that does not need it is never loaded.
func taskImageCheckApplies(cfg *schemas.AgentConfig, s *schemas.Suite) bool {
	if cfg == nil || s == nil || s.Verifier.Mode != schemas.VerifierSnapshot {
		return false
	}
	return cfg.Adapter != scriptedAdapterName && (cfg.Build == nil || cfg.Build.BaseArg == "")
}

// taskImageReason is the plain reason cfg cannot run benchmark sid inside
// each task's own environment, with the technical text after " Details: ",
// or "" when it can. It runs deriveTaskImageAgent on the full pack (the
// head has no tasks), as the estimate does.
func (h *agentCompatHandler) taskImageReason(ctx context.Context, src SuiteSource, sid string, head *schemas.Suite, cfg *schemas.AgentConfig, agents map[string]*schemas.AgentConfig) (string, error) {
	full := head
	if len(head.Tasks) == 0 {
		var err error
		if full, err = compatSuite(ctx, src, sid, true); err != nil || full == nil {
			return "", fmt.Errorf("load benchmark %s: %v", sid, err)
		}
	}
	v := deriveTaskImageAgent(cfg, full, nil, agents, h.root())
	if v.Skip == "" {
		return "", nil
	}
	return fmt.Sprintf("Runs inside each task's own environment, and %s cannot join it (its image is not built on the benchmark's base image). Details: %s", cfg.ID, v.Skip), nil
}

// root is the bench root the agent Dockerfiles resolve against, as the
// estimate reads it.
func (h *agentCompatHandler) root() string {
	if p := h.env.Services.Projects; p != nil {
		return p.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

// suiteHeads caches pack heads process-wide by folder and stamp: library
// packs are shared by every org.
var suiteHeads = struct {
	mu sync.Mutex
	m  map[string]headEntry
}{m: map[string]headEntry{}}

type headEntry struct {
	stamp string
	s     *schemas.Suite
}

// compatSuite is what compat.Full needs of pack id: its head (suite.yaml
// and suite.lock) when the source can read one, cached by stamp, else the
// full load. With tasks set (an agent outside the workspace, whose output
// rules are checked per task) it is the full load, cached by stamp too.
func compatSuite(ctx context.Context, src SuiteSource, id string, tasks bool) (*schemas.Suite, error) {
	st, ok := src.(suiteStamper)
	if !ok {
		return src.LoadSuite(ctx, id)
	}
	dir, stamp := st.SuiteStamp(id)
	key := dir
	if tasks {
		key = "full\x00" + dir
	}
	suiteHeads.mu.Lock()
	e, hit := suiteHeads.m[key]
	suiteHeads.mu.Unlock()
	if hit && e.stamp == stamp {
		return e.s, nil
	}
	var s *schemas.Suite
	var err error
	if tasks {
		s, err = src.LoadSuite(ctx, id)
	} else {
		s, err = st.SuiteHead(id)
	}
	if err != nil {
		return nil, err
	}
	if s.ID == "" {
		s.ID = id
	}
	suiteHeads.mu.Lock()
	suiteHeads.m[key] = headEntry{stamp: stamp, s: s}
	suiteHeads.mu.Unlock()
	return s, nil
}

// compatKey is the cache key of one answer: the agent config hash and every
// pack's folder and stamp. "" when the source has no stamps (no caching).
func compatKey(cfg *schemas.AgentConfig, src SuiteSource, ids []string) string {
	st, ok := src.(suiteStamper)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(agentConfigHash(cfg))
	for _, id := range ids {
		dir, stamp := st.SuiteStamp(id)
		b.WriteString("\x00" + id + "\x00" + dir + "\x00" + stamp)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// agentConfig finds agent id: through the org's agent source (which holds
// built-in library agents too) when the server has one, else the project's
// agents/<id>.yaml.
func (h *agentCompatHandler) agentConfig(ctx *fasthttp.RequestCtx, id string) (*schemas.AgentConfig, error) {
	if src := h.env.Services.Agents; src != nil {
		if files, err := src.AgentConfigs(ctx); err == nil {
			for _, f := range files {
				if f.Config != nil && f.Config.ID == id {
					return f.Config, nil
				}
			}
		}
	}
	if ps := h.env.Services.Projects; ps != nil {
		for _, ext := range []string{".yaml", ".yml"} {
			data, _, err := ps.Read(ctx, projectfs.AreaAgents, id+ext)
			if err != nil {
				continue
			}
			var cfg schemas.AgentConfig
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("agent %q: could not read its config: %v", id, err)
			}
			if cfg.ID == "" {
				cfg.ID = id
			}
			return &cfg, nil
		}
	}
	return nil, fmt.Errorf("agent %q not found: create it first (Agents, New agent) or check agents/%s.yaml", id, id)
}

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/compatibility", Tag: "agents",
			Summary: "Which benchmarks an agent can run: its capabilities against what each benchmark needs, with the missing capabilities and the reason", Response: AgentCompatibility{}},
	)
}
