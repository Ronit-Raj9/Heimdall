package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/modelswitch"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W4 Bifrost feature tests (audit/W4_DESIGN_3_BIFROST.md section 3.6): draft
// a comparison that changes one gateway setting, read its feature effect,
// and the org's saved connection to its own Bifrost.

// Draft limits of POST /gateway/tests/draft. A larger budget needs the
// normal New comparison form.
const (
	gatewayDraftMaxUSD        = 50
	gatewayDraftDefaultTrials = 2
	gatewayDraftMaxTrials     = 5
)

// gatewayRunHTTP is the client for the run gateway's management reads; tests
// may replace it.
var gatewayRunHTTP = &http.Client{Timeout: 4 * time.Second}

// gatewaySecret reads one org secret by name; tests may replace it.
var gatewaySecret = func(ctx context.Context, s Services, name string) string {
	return OrgSecretGetenv(ctx, s)(name)
}

// gatewaySecretName is an org secret name.
var gatewaySecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

// GatewaySourceInfo is GET /gateway/source: never the credential, never the
// full address. Mode is the first field: lib.SendJSON then keeps it instead of adding the
// server's mode under the same key.
type GatewaySourceInfo struct {
	Mode      string `json:"mode"` // saved | run_gateway | none
	Connected bool   `json:"connected"`
	Host      string `json:"host"`
	Error     string `json:"error"`
}

func init() {
	RegisterOrgArea("gatewaytests", func(env OrgEnv, r *router.Router) {
		newGatewayTestsHandler(env).routes(r)
	})
}

type gatewayTestsHandler struct {
	env    OrgEnv
	source func() leaderboard.Source // nil result: no run index on this server
}

func newGatewayTestsHandler(env OrgEnv) *gatewayTestsHandler {
	h := &gatewayTestsHandler{env: env}
	h.source = func() leaderboard.Source {
		if env.Services.Index == nil {
			return nil
		}
		return LeaderboardSource(env.Services.Index)
	}
	return h
}

func (h *gatewayTestsHandler) routes(r *router.Router) {
	env := h.env
	r.GET(APIPrefix+"/gateway/features", env.R(h.features))
	r.POST(APIPrefix+"/gateway/tests/draft", env.W(h.draft))
	r.GET(APIPrefix+"/experiments/{id}/gateway-effect", env.R(h.effect))
	r.GET(APIPrefix+"/gateway/effects", env.R(h.effects))
	r.GET(APIPrefix+"/gateway/report-card", env.R(h.reportCard))
	r.GET(APIPrefix+"/gateway/source", env.R(h.getSource))
	r.POST(APIPrefix+"/gateway/source", env.W(h.saveSource))
	r.POST(APIPrefix+"/gateway/source/remove", env.W(h.removeSource))
}

func (h *gatewayTestsHandler) root() string {
	if p := h.env.Services.Projects; p != nil {
		return p.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

func (h *gatewayTestsHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *gatewayTestsHandler) features(ctx *fasthttp.RequestCtx) {
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := []item{}
	for _, f := range gwfeature.Features() {
		out = append(out, item{ID: f.ID, Name: f.Name})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"features": out})
}

func (h *gatewayTestsHandler) draft(ctx *fasthttp.RequestCtx) {
	var req gwfeature.DraftRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.bad(ctx, "the test request could not be read: "+err.Error())
		return
	}
	f, ok := gwfeature.Lookup(strings.TrimSpace(req.Feature))
	if !ok || f.Draft == nil {
		h.bad(ctx, fmt.Sprintf("this Heimdall does not know the feature %s", req.Feature))
		return
	}
	if !(req.BudgetUSD > 0) || req.BudgetUSD > gatewayDraftMaxUSD {
		h.bad(ctx, fmt.Sprintf("Give a budget above 0 and at most %d USD. For a larger budget use the New comparison form.", gatewayDraftMaxUSD))
		return
	}
	if req.Trials == 0 {
		req.Trials = gatewayDraftDefaultTrials
	}
	if req.Trials < 1 || req.Trials > gatewayDraftMaxTrials {
		h.bad(ctx, fmt.Sprintf("Trials must be between 1 and %d.", gatewayDraftMaxTrials))
		return
	}
	if strings.TrimSpace(req.Suite) == "" || strings.TrimSpace(req.Base) == "" {
		h.bad(ctx, "Pick a benchmark and an agent.")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	denv := gwfeature.DraftEnv{Root: h.root(), SourceGet: gatewaySourceGet(work, h.env), GatewayGet: gatewayRunGet(work, h.env)}
	exp, notes, err := f.Draft(work, denv, req)
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if exp == nil {
		h.bad(ctx, "this feature could not build a test")
		return
	}
	if exp.GatewayTest == nil {
		exp.GatewayTest = &schemas.GatewayTest{Feature: f.ID, Control: "A", Options: req.Options}
		for _, id := range []string{"B", "C", "D", "E"} {
			if _, ok := exp.Arms[id]; ok {
				exp.GatewayTest.Variants = append(exp.GatewayTest.Variants, id)
			}
		}
	}
	exp.Budget.TotalUSD = req.BudgetUSD
	if exp.Name == "" {
		exp.Name = "Bifrost test: " + f.Name
	}
	if notes == nil {
		notes = []string{}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(map[string]any{"experiment": exp, "notes": notes}))
}

func (h *gatewayTestsHandler) effect(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "comparison")
	if !ok {
		return
	}
	src := h.source()
	if src == nil {
		sendUnavailable(ctx, h.env.Mode, "the run index")
		return
	}
	if _, ok := src.Manifest(id); !ok {
		sendNotFound(ctx, h.env.Mode, "comparison", id)
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	e, err := gwfeature.Compute(work, src, h.root(), id, string(ctx.QueryArgs().Peek("refresh")) == "1")
	if errors.Is(err, gwfeature.ErrNotFeatureTest) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the feature report could not be built: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(e))
}

func (h *gatewayTestsHandler) effects(ctx *fasthttp.RequestCtx) {
	args := ctx.QueryArgs()
	limit := 20
	if raw := strings.TrimSpace(string(args.Peek("limit"))); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			h.bad(ctx, "limit must be a whole number from 1 to 100")
			return
		}
		limit = n
	}
	feature := strings.TrimSpace(string(args.Peek("feature")))
	list, err := gwfeature.List(h.root())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "saved feature reports could not be read")
		return
	}
	out := []gwfeature.Effect{}
	for _, e := range list {
		if feature != "" && e.Feature != feature {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, e)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(map[string]any{"effects": out}))
}

// sourceInfo is the answer of the source routes.
func (h *gatewayTestsHandler) sourceInfo() GatewaySourceInfo {
	return gatewaySourceInfo(h.env, h.root())
}

func gatewaySourceInfo(env OrgEnv, root string) GatewaySourceInfo {
	c, err := gwfeature.LoadSource(root)
	if err != nil {
		return GatewaySourceInfo{Mode: "none", Error: "the saved Bifrost connection could not be read"}
	}
	if c != nil {
		return GatewaySourceInfo{Connected: true, Host: urlHost(c.GatewayURL), Mode: "saved"}
	}
	if !env.Services.Hosted && env.Cfg != nil && strings.TrimSpace(env.Cfg.Gateway.URL) != "" {
		return GatewaySourceInfo{Connected: true, Host: urlHost(env.Cfg.Gateway.URL), Mode: "run_gateway"}
	}
	return GatewaySourceInfo{Mode: "none"}
}

func urlHost(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil {
		return u.Host
	}
	return ""
}

func (h *gatewayTestsHandler) getSource(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.sourceInfo())
}

func (h *gatewayTestsHandler) saveSource(ctx *fasthttp.RequestCtx) {
	var req struct {
		GatewayURL       string `json:"gateway_url"`
		CredentialSecret string `json:"credential_secret"`
	}
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.bad(ctx, "the connection could not be read: "+err.Error())
		return
	}
	hosted := h.env.Services.Hosted
	u, err := modelswitch.CheckGatewayURL(req.GatewayURL, hosted)
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	name := strings.TrimSpace(req.CredentialSecret)
	if name != "" && !gatewaySecretName.MatchString(name) {
		h.bad(ctx, "Pick the org secret that holds the admin credential.")
		return
	}
	work, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cred := ""
	if name != "" {
		if cred = gatewaySecret(work, h.env.Services, name); cred == "" {
			h.bad(ctx, fmt.Sprintf("The org has no secret named %s.", name))
			return
		}
	}
	gw := modelswitch.Gateway{BaseURL: u.String(), Credential: cred, Client: modelswitch.GatewayClient(hosted)}
	var version string
	if err := gw.Get(work, "/api/version", &version); err != nil {
		h.bad(ctx, "Heimdall could not reach your Bifrost: "+plainGatewayError(err))
		return
	}
	saved := gwfeature.SourceConfig{GatewayURL: u.String(), CredentialSecret: name, SavedAt: time.Now().UTC()}
	if p := auth.PrincipalFrom(ctx); p != nil {
		saved.SavedBy = p.UserID
	}
	if err := gwfeature.SaveSource(h.root(), saved); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the connection could not be saved")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.sourceInfo())
}

func (h *gatewayTestsHandler) removeSource(ctx *fasthttp.RequestCtx) {
	if err := gwfeature.RemoveSource(h.root()); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the connection could not be removed")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.sourceInfo())
}

// plainGatewayError keeps a gateway error to plain words without the
// address it was sent to.
func plainGatewayError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return "the address did not answer"
	}
	return err.Error()
}

// gatewaySourceGet reads paths of the org's own Bifrost: the saved source,
// else (local mode only) the run gateway; nil when there is neither.
func gatewaySourceGet(ctx context.Context, env OrgEnv) func(ctx context.Context, path string, out any) error {
	root := ""
	if env.Services.Projects != nil {
		root = env.Services.Projects.Root()
	} else if env.Cfg != nil {
		root = env.Cfg.Root
	}
	if root != "" {
		if c, err := gwfeature.LoadSource(root); err == nil && c != nil {
			gw := modelswitch.Gateway{BaseURL: c.GatewayURL, Credential: gatewaySecret(ctx, env.Services, c.CredentialSecret), Client: modelswitch.GatewayClient(env.Services.Hosted)}
			return gw.Get
		}
	}
	if env.Services.Hosted {
		return nil
	}
	return gatewayRunGet(ctx, env)
}

// gatewayRunGet reads management paths of the gateway runs go through, with
// its setup token; nil when none is configured. Hosted servers call only
// read routes and never name another org's objects.
func gatewayRunGet(_ context.Context, env OrgEnv) func(ctx context.Context, path string, out any) error {
	if env.Cfg == nil || strings.TrimSpace(env.Cfg.Gateway.URL) == "" {
		return nil
	}
	base := strings.TrimRight(strings.TrimSpace(env.Cfg.Gateway.URL), "/")
	tokenEnv := env.Cfg.Gateway.AdminTokenEnv
	return func(ctx context.Context, path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if tokenEnv != "" {
			if tok := bifrostUseGetenv(tokenEnv); tok != "" {
				req.Header.Set("X-Bifrost-Setup-Token", tok)
			}
		}
		resp, err := gatewayRunHTTP.Do(req)
		if err != nil {
			return errors.New("the gateway did not answer")
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return errors.New("the gateway answer could not be read")
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return &gatewayStatusError{Status: resp.StatusCode}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("the gateway answer could not be read")
		}
		return nil
	}
}

// gatewayStatusError is a non-2xx answer of the run gateway.
type gatewayStatusError struct{ Status int }

func (e *gatewayStatusError) Error() string {
	return fmt.Sprintf("the gateway answered %d", e.Status)
}
