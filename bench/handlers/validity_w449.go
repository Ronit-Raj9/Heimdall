package handlers

import (
	"context"
	"fmt"
	"sort"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/validity"
	"github.com/maximhq/bifrost/bench/webhooks"
	"github.com/valyala/fasthttp"
)

// W4-49 E: the validity ledger. Each read compares the re-scores that have
// no entry yet (at most 20 per request), stores the entries, and queues one
// result.changed event per new flipped entry. It never changes which result
// the lists show.

// ValidityList answers GET /validity.
type ValidityList struct {
	Entries []validity.Entry `json:"entries"`
	Unseen  int              `json:"unseen"`
	Stale   []validity.Stale `json:"stale"`
}

// ExperimentValidity answers GET /experiments/{id}/validity.
type ExperimentValidity struct {
	Entries []validity.Entry `json:"entries"`
	Stale   *validity.Stale  `json:"stale"`
}

// ValidityMaxList caps the entries of GET /validity.
const ValidityMaxList = 200

// ResultChangedBody is the data of a result.changed event.
type ResultChangedBody struct {
	ExpID     string        `json:"exp_id"`
	RegradeID string        `json:"regrade_id"`
	Before    validity.Side `json:"before"`
	After     validity.Side `json:"after"`
	Note      string        `json:"note,omitempty"`
}

type validityHandler struct {
	mode     schemas.Mode
	orgID    string
	index    ReadIndex
	analyzer Analyzer
	store    validity.Store
	hooks    *webhooks.Store
}

func init() {
	RegisterOrgArea("validity_w449", func(env OrgEnv, r *router.Router) {
		root := ""
		if env.Cfg != nil {
			root = env.Cfg.Root
		}
		h := &validityHandler{mode: env.Mode, orgID: env.Services.OrgID, index: env.Services.Index, analyzer: env.Services.Analyzer,
			store: validity.Open(root), hooks: webhooks.OpenWith(root, env.Services.WebhookCipher)}
		h.register(r, env.Common...)
	})
}

func (h *validityHandler) register(r *router.Router, mw ...lib.Middleware) {
	r.GET(APIPrefix+"/validity", lib.ChainMiddlewares(h.list, mw...))
	r.GET(APIPrefix+"/experiments/{id}/validity", lib.ChainMiddlewares(h.experiment, mw...))
	r.POST(APIPrefix+"/validity/{id}/seen", lib.ChainMiddlewares(h.seen, mw...))
}

func (h *validityHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.index == nil || h.analyzer == nil {
		sendUnavailable(ctx, h.mode, "validity ledger")
		return false
	}
	return true
}

func (h *validityHandler) regrades(ctx context.Context, expID string) ([]validity.RegradeMeta, error) {
	ri, ok := h.index.(RegradeIndex)
	if !ok {
		return nil, nil
	}
	rs, err := ri.Regrades(ctx, expID)
	if err != nil {
		return nil, err
	}
	out := make([]validity.RegradeMeta, 0, len(rs))
	for _, r := range rs {
		out = append(out, validity.RegradeMeta{ID: r.ID, CreatedAt: r.CreatedAt, Note: r.Note})
	}
	return out, nil
}

// fill brings the ledger up to date for expIDs and tells hooks about each
// new flip. A failed fill still answers with what is stored.
func (h *validityHandler) fill(ctx context.Context, expIDs []string) {
	made, err := validity.Fill(ctx, h.store, expIDs, h.regrades, h.analyzer.Analyze, validity.MaxFillPerRequest)
	if err != nil {
		return
	}
	h.notify(made)
}

// notify queues one result.changed event per flipped entry.
func (h *validityHandler) notify(made []validity.Entry) {
	if h.hooks == nil {
		return
	}
	for _, e := range made {
		if !e.Flipped {
			continue
		}
		ev := webhooks.NewEvent(webhooks.EventResultChanged, h.orgID, ResultChangedBody{ExpID: e.ExpID, RegradeID: e.RegradeID, Before: e.Before, After: e.After, Note: e.Note})
		ev.Summary = &webhooks.Summary{Title: "A result changed after a re-score",
			Text:     fmt.Sprintf("Comparison %s was %q and is now %q after a re-score.", e.ExpID, e.Before.Decision, e.After.Decision),
			Severity: webhooks.SeverityWarning}
		_, _ = h.hooks.Enqueue(ev)
	}
}

func userOf(ctx *fasthttp.RequestCtx) string {
	if p := auth.PrincipalFrom(ctx); p != nil {
		return p.ActorID()
	}
	return ""
}

func (h *validityHandler) list(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	h.fill(work, h.index.Experiments())
	es, err := h.store.Entries()
	if err != nil {
		sendDepError(ctx, h.mode, "validity ledger", err)
		return
	}
	stale, err := h.store.Stale()
	if err != nil {
		sendDepError(ctx, h.mode, "validity ledger", err)
		return
	}
	user := userOf(ctx)
	flippedOnly := string(ctx.QueryArgs().Peek("flipped")) == "1"
	out := ValidityList{Entries: []validity.Entry{}, Stale: stale}
	sort.SliceStable(es, func(i, j int) bool { return es[i].At.After(es[j].At) })
	for _, e := range es {
		if e.Flipped && !e.Seen(user) {
			out.Unseen++
		}
		if flippedOnly && !e.Flipped {
			continue
		}
		if len(out.Entries) < ValidityMaxList {
			out.Entries = append(out.Entries, e)
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, plainServed(out))
}

func (h *validityHandler) experiment(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	if _, found := h.index.Manifest(id); !found {
		sendNotFound(ctx, h.mode, "experiment", id)
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	h.fill(work, []string{id})
	es, err := h.store.Entries()
	if err != nil {
		sendDepError(ctx, h.mode, "validity ledger", err)
		return
	}
	out := ExperimentValidity{Entries: []validity.Entry{}}
	for _, e := range es {
		if e.ExpID == id {
			out.Entries = append(out.Entries, e)
		}
	}
	sort.SliceStable(out.Entries, func(i, j int) bool { return out.Entries[i].At.After(out.Entries[j].At) })
	if stale, err := h.store.Stale(); err == nil {
		for i := range stale {
			if stale[i].ExpID == id {
				out.Stale = &stale[i]
			}
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, plainServed(out))
}

func (h *validityHandler) seen(ctx *fasthttp.RequestCtx) {
	id := pathParam(ctx, "id")
	user := userOf(ctx)
	if user == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "sign in to dismiss an alert")
		return
	}
	ok, err := h.store.MarkSeen(id, user)
	if err != nil {
		sendDepError(ctx, h.mode, "validity ledger", err)
		return
	}
	if !ok {
		sendNotFound(ctx, h.mode, "changed result", id)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
