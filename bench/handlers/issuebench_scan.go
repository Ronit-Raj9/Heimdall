package handlers

// Issues to benchmark, scan step (lane G01): a free dry run that reads a
// GitHub repository's merged pull requests that close one issue and says
// which would become benchmark tasks, with a funnel of why the rest were
// dropped. GitHub API calls only; no model calls, no cost.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/scan"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// IssueBenchPrefix is the prefix of the issues-to-benchmark routes.
const IssueBenchPrefix = APIPrefix + "/issuebench"

const issueScanTimeout = 5 * time.Minute

// IssueScanRequest is POST /issuebench/scan.
type IssueScanRequest struct {
	Repo     string     `json:"repo"`               // owner/name or https://github.com/owner/name
	MaxPRs   int        `json:"max_prs,omitempty"`  // merged pull requests to read; default 200, max 2000
	Since    *time.Time `json:"since,omitempty"`    // only pull requests opened after this
	Language string     `json:"language,omitempty"` // keep only this language, e.g. python
	// Token is an optional GitHub token for a public repository when the
	// organization has not connected GitHub. It is used for this scan only
	// and never stored or returned.
	Token string `json:"token,omitempty"`
}

// IssueBenchHandler serves the issues-to-benchmark routes.
type IssueBenchHandler struct {
	mode    schemas.Mode
	keys    keystore.Store
	keysErr string
	apiURL  string
	http    *http.Client
}

// NewIssueBenchHandler builds the handler. keys nil still allows scans with
// a token in the request. apiURL "" is api.github.com.
func NewIssueBenchHandler(mode schemas.Mode, keys keystore.Store, keysErr, apiURL string) *IssueBenchHandler {
	return &IssueBenchHandler{mode: mode, keys: keys, keysErr: keysErr, apiURL: apiURL}
}

// RegisterRoutes registers the routes.
func (h *IssueBenchHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	r.POST(IssueBenchPrefix+"/scan", lib.ChainMiddlewares(h.postScan, mw...))
}

func (h *IssueBenchHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case http.StatusForbidden:
		t = lib.ErrTypeForbidden
	case http.StatusNotFound:
		t = lib.ErrTypeNotFound
	case http.StatusConflict:
		t = lib.ErrTypeConflict
	case http.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case http.StatusBadGateway, http.StatusInternalServerError, http.StatusGatewayTimeout:
		t = lib.ErrTypeInternal
	}
	lib.SendError(ctx, h.mode, status, t, msg)
}

func (h *IssueBenchHandler) postScan(ctx *fasthttp.RequestCtx) {
	o, ok := tenancy.OrgFrom(ctx)
	if !ok {
		h.fail(ctx, http.StatusForbidden, "request not resolved to an organization")
		return
	}
	var req IssueScanRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		h.fail(ctx, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if _, _, err := scan.SplitRepo(req.Repo); err != nil {
		h.fail(ctx, http.StatusBadRequest, "repo must be owner/name, for example psf/requests")
		return
	}
	if req.MaxPRs < 0 || req.MaxPRs > 2000 {
		h.fail(ctx, http.StatusBadRequest, "max_prs must be between 1 and 2000")
		return
	}
	wctx, cancel := workContext(ctx, issueScanTimeout)
	defer cancel()
	tok := strings.TrimSpace(req.Token)
	if tok == "" {
		if h.keys == nil {
			h.fail(ctx, http.StatusServiceUnavailable, "the key store is not available ("+h.keysErr+"); give a GitHub token for a public repository instead")
			return
		}
		t, _, err := auth.FindRepoToken(wctx, h.keys, o.ID)
		if errors.Is(err, keystore.ErrNotFound) {
			h.fail(ctx, http.StatusConflict, "GitHub is not connected for this organization; an admin can connect it in Settings > GitHub, or give a GitHub token for a public repository")
			return
		}
		if err != nil {
			h.fail(ctx, http.StatusInternalServerError, "read the stored GitHub token failed")
			return
		}
		tok = t
	}
	c := &scan.Client{APIURL: h.apiURL, Token: tok, HTTP: h.http}
	opt := scan.Options{Repo: req.Repo, MaxPRs: req.MaxPRs, Language: req.Language}
	if req.Since != nil {
		opt.Since = *req.Since
	}
	res, err := scan.Scan(wctx, c, opt)
	if err != nil {
		var ae *scan.APIError
		switch {
		case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
			h.fail(ctx, http.StatusNotFound, ae.Error())
		case errors.As(err, &ae) && ae.Status == http.StatusUnauthorized:
			h.fail(ctx, http.StatusConflict, ae.Error())
		case errors.As(err, &ae) && (ae.Status == http.StatusForbidden || ae.Status == http.StatusTooManyRequests):
			h.fail(ctx, http.StatusBadGateway, ae.Error())
		case errors.Is(wctx.Err(), context.DeadlineExceeded):
			h.fail(ctx, http.StatusGatewayTimeout, "the scan took longer than 5 minutes; lower max_prs")
		default:
			h.fail(ctx, http.StatusBadGateway, "GitHub request failed: "+err.Error())
		}
		return
	}
	if res.Candidates == nil {
		res.Candidates = []issuebench.Candidate{}
	}
	if res.Dropped == nil {
		res.Dropped = []issuebench.Candidate{}
	}
	lib.SendJSON(ctx, h.mode, http.StatusOK, res)
}
