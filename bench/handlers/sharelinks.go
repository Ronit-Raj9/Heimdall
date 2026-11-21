package handlers

// Share links (lane X14, docs/sharing.md): a signed, read-only link to one
// report card, evaluation, verdict, run trace or leaderboard view, opened
// without an account until it expires or is revoked; plus report card
// exports (HTML with a print stylesheet, Markdown, JSON), a badge SVG and a
// "reproduce this" bundle.
//
//	POST /api/bench/shares                         member: create a link
//	GET  /api/bench/shares                         viewer: list links (?kind=&target=)
//	POST /api/bench/shares/{id}/revoke             member: end a link now
//	GET  /api/bench/shared/{token}                 anyone with the link: the item, redacted
//	GET  /api/bench/shared/{token}/badge.svg       anyone with the link: a score or verdict badge
//	GET  /api/bench/shared/{token}/export          anyone with the link: report card as html or md
//	GET  /api/bench/evaluations/{id}/report/export viewer: report card as html, md or json
//
// A shared item is served by running the org's own read route for it
// inside this request (the same handler, the same held-out rules), then
// removing secrets and, when the link says so, prompts (sharelinks.Redact).
// Only the routes named per kind are ever reached: a link to an evaluation
// reads that evaluation and its report, nothing else.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharelinks"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// ShareView is one link as the API shows it to members.
type ShareView struct {
	sharelinks.Share
	State string `json:"state"`
	Token string `json:"token,omitempty"`
	// Path is the page that opens the link, relative to the server
	// (/s/<token>); URL is the same on auth.public_url when it is set.
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

// ShareList is GET /shares.
type ShareList struct {
	Shares []ShareView `json:"shares"`
}

// SharedMeta describes a shared item to its visitor.
type SharedMeta struct {
	Kind        string     `json:"kind"`
	Target      string     `json:"target"`
	Query       string     `json:"query,omitempty"`
	Label       string     `json:"label,omitempty"`
	HidePrompts bool       `json:"hide_prompts"`
	Org         string     `json:"org,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// Exports lists the formats GET /shared/{token}/export serves for this
	// item; Badge says whether badge.svg shows a value.
	Exports []string `json:"exports,omitempty"`
	Badge   bool     `json:"badge"`
}

// SharedItem is GET /shared/{token}: the link and the item's data, keyed by
// part (evaluation and report; report_card; experiment and verdict; run and
// trace; leaderboard).
type SharedItem struct {
	Share SharedMeta     `json:"share"`
	Data  map[string]any `json:"data"`
}

type shareHandler struct {
	env  OrgEnv
	self func() fasthttp.RequestHandler
	now  func() time.Time
}

func init() {
	RegisterOrgArea("sharelinks", registerShareLinks)
}

func registerShareLinks(env OrgEnv, r *router.Router) {
	h := &shareHandler{env: env, self: func() fasthttp.RequestHandler { return r.Handler }, now: func() time.Time { return time.Now().UTC() }}
	r.POST(APIPrefix+"/shares", env.W(h.create))
	r.GET(APIPrefix+"/shares", env.R(h.list))
	r.POST(APIPrefix+"/shares/{id}/revoke", env.W(h.revoke))
	r.GET(APIPrefix+"/shared/{token}", env.R(h.open))
	r.GET(APIPrefix+"/shared/{token}/badge.svg", env.R(h.badge))
	r.GET(APIPrefix+"/shared/{token}/export", env.R(h.export))
	r.GET(APIPrefix+"/evaluations/{id}/report/export", env.R(h.reportExport))
}

func (h *shareHandler) store() *sharelinks.Store {
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		return nil
	}
	return sharelinks.Open(h.env.Cfg.Root).WithClock(h.now)
}

// orgID is the organization the request acts in: the principal's (hosted:
// set by the dispatcher; local: the local org).
func shareOrgID(ctx *fasthttp.RequestCtx) string {
	if id := auth.PrincipalFrom(ctx).OrgID; id != "" {
		return id
	}
	return auth.LocalOrgID
}

func (h *shareHandler) view(ctx *fasthttp.RequestCtx, st *sharelinks.Store, sh sharelinks.Share) ShareView {
	v := ShareView{Share: sh, State: sh.State(h.now())}
	if v.State != sharelinks.StateActive {
		return v
	}
	tok, err := st.Token(shareOrgID(ctx), sh.ID)
	if err != nil {
		return v
	}
	v.Token, v.Path = tok, "/s/"+tok
	if h.env.Cfg != nil && h.env.Cfg.Auth.PublicURL != "" {
		v.URL = strings.TrimRight(h.env.Cfg.Auth.PublicURL, "/") + v.Path
	}
	return v
}

func (h *shareHandler) create(ctx *fasthttp.RequestCtx) {
	st := h.store()
	if st == nil {
		sendUnavailable(ctx, h.env.Mode, "share links")
		return
	}
	var req sharelinks.NewShare
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be JSON: "+err.Error())
		return
	}
	if _, err := req.Validate(); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	// The item must exist and be readable now: a link to nothing is refused.
	q, _ := sharelinks.CleanQuery(req.Kind, strings.TrimSpace(req.Target), req.Query)
	parts := sharedParts(strings.TrimSpace(req.Kind), strings.TrimSpace(req.Target), q)
	if len(parts) > 0 {
		status, body, _ := h.sub(ctx, parts[0].path)
		if status != fasthttp.StatusOK {
			msg := fmt.Sprintf("cannot share %s %s: it was not found", req.Kind, req.Target)
			if status != fasthttp.StatusNotFound {
				msg = fmt.Sprintf("cannot share %s %s: %s", req.Kind, req.Target, errorMessage(body, status))
				lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, msg)
				return
			}
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, msg)
			return
		}
	}
	p := auth.PrincipalFrom(ctx)
	sh, err := st.Create(req, p.ActorID())
	switch {
	case errors.Is(err, sharelinks.ErrInvalid):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	case errors.Is(err, sharelinks.ErrTooMany):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	case err != nil:
		sendDepError(ctx, h.env.Mode, "share links", err)
		return
	}
	tenancy.SetCreated(ctx, sh.ID)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, h.view(ctx, st, *sh))
}

func (h *shareHandler) list(ctx *fasthttp.RequestCtx) {
	st := h.store()
	if st == nil {
		sendUnavailable(ctx, h.env.Mode, "share links")
		return
	}
	args := ctx.QueryArgs()
	shares, err := st.List(strings.TrimSpace(string(args.Peek("kind"))), strings.TrimSpace(string(args.Peek("target"))))
	if err != nil {
		sendDepError(ctx, h.env.Mode, "share links", err)
		return
	}
	out := ShareList{Shares: make([]ShareView, 0, len(shares))}
	for _, sh := range shares {
		out.Shares = append(out.Shares, h.view(ctx, st, sh))
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *shareHandler) revoke(ctx *fasthttp.RequestCtx) {
	st := h.store()
	if st == nil {
		sendUnavailable(ctx, h.env.Mode, "share links")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	sh, err := st.Revoke(id, auth.PrincipalFrom(ctx).ActorID())
	if errors.Is(err, sharelinks.ErrNotFound) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no share link "+id)
		return
	}
	if err != nil {
		sendDepError(ctx, h.env.Mode, "share links", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.view(ctx, st, *sh))
}

// sharedPart is one org read route a kind of link reaches.
type sharedPart struct {
	name string
	path string // with query
}

// sharedParts lists, per kind, the read routes a link reaches. The first is
// the item itself: when it is gone the link answers 404.
func sharedParts(kind, target, query string) []sharedPart {
	t := url.PathEscape(target)
	withQuery := func(p string) string {
		if query == "" {
			return p
		}
		return p + "?" + query
	}
	switch kind {
	case sharelinks.KindEvaluation:
		return []sharedPart{{"evaluation", APIPrefix + "/evaluations/" + t}, {"report", APIPrefix + "/evaluations/" + t + "/report"}}
	case sharelinks.KindReportCard:
		return []sharedPart{{"report_card", withQuery(APIPrefix + "/agents/" + t + "/report-card")}}
	case sharelinks.KindVerdict:
		return []sharedPart{{"experiment", APIPrefix + "/experiments/" + t}, {"verdict", APIPrefix + "/experiments/" + t + "/verdict"}}
	case sharelinks.KindRunTrace:
		return []sharedPart{{"run", APIPrefix + "/runs/" + t}, {"trace", APIPrefix + "/runs/" + t + "/trace?format=json"}}
	case sharelinks.KindLeaderboard:
		if target == "agents" {
			return []sharedPart{{"leaderboard", withQuery(APIPrefix + "/leaderboard/agents")}}
		}
		return []sharedPart{{"leaderboard", withQuery(APIPrefix + "/leaderboard")}}
	}
	return nil
}

// sub runs one of this org's GET routes inside the current request, with
// the current principal and org, and returns its answer.
func (h *shareHandler) sub(ctx *fasthttp.RequestCtx, pathAndQuery string) (int, []byte, string) {
	var req fasthttp.Request
	req.Header.SetMethod(fasthttp.MethodGet)
	req.SetRequestURI(pathAndQuery)
	req.Header.SetHostBytes(ctx.Host())
	var sub fasthttp.RequestCtx
	sub.Init(&req, ctx.RemoteAddr(), nil)
	ctx.VisitUserValues(func(k []byte, v any) { sub.SetUserValueBytes(k, v) })
	h.self()(&sub)
	return sub.Response.StatusCode(), append([]byte(nil), sub.Response.Body()...), string(sub.Response.Header.ContentType())
}

// errorMessage is the message of a bench error body, else the status text.
func errorMessage(body []byte, status int) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		if e.Error.Message != "" {
			return e.Error.Message
		}
		if e.Message != "" {
			return e.Message
		}
	}
	return fasthttp.StatusMessage(status)
}

// verify checks the visitor's token, answering the request when it fails.
// A token that is not ours or not signed is 404; a real link that expired
// or was revoked is 410, so its holder knows to ask for a new one.
func (h *shareHandler) verify(ctx *fasthttp.RequestCtx) (*sharelinks.Share, bool) {
	st := h.store()
	if st == nil {
		sendUnavailable(ctx, h.env.Mode, "share links")
		return nil, false
	}
	token, _ := ctx.UserValue("token").(string)
	sh, err := st.Verify(shareOrgID(ctx), token)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Robots-Tag", "noindex, nofollow")
	switch {
	case err == nil:
		return sh, true
	case errors.Is(err, sharelinks.ErrExpired):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusGone, lib.ErrTypeNotFound, "this share link has expired: ask the person who sent it for a new one")
	case errors.Is(err, sharelinks.ErrRevoked):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusGone, lib.ErrTypeNotFound, "this share link was turned off: ask the person who sent it for a new one")
	case errors.Is(err, sharelinks.ErrBadToken), errors.Is(err, sharelinks.ErrNotFound):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "this share link does not exist")
	default:
		sendDepError(ctx, h.env.Mode, "share links", err)
	}
	return nil, false
}

func (h *shareHandler) meta(ctx *fasthttp.RequestCtx, sh *sharelinks.Share) SharedMeta {
	m := SharedMeta{Kind: sh.Kind, Target: sh.Target, Query: sh.Query, Label: sh.Label, HidePrompts: sh.HidePrompts, CreatedAt: sh.CreatedAt, ExpiresAt: sh.ExpiresAt}
	if o, ok := tenancy.OrgFrom(ctx); ok {
		m.Org = o.Name
	}
	switch sh.Kind {
	case sharelinks.KindEvaluation:
		m.Exports, m.Badge = []string{sharelinks.FormatHTML, sharelinks.FormatMarkdown}, true
	case sharelinks.KindReportCard, sharelinks.KindVerdict:
		m.Badge = true
	}
	return m
}

// open handles GET /shared/{token}.
func (h *shareHandler) open(ctx *fasthttp.RequestCtx) {
	sh, ok := h.verify(ctx)
	if !ok {
		return
	}
	out := SharedItem{Share: h.meta(ctx, sh), Data: map[string]any{}}
	for i, p := range sharedParts(sh.Kind, sh.Target, sh.Query) {
		status, body, _ := h.sub(ctx, p.path)
		if status != fasthttp.StatusOK {
			if i == 0 {
				lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "the shared item no longer exists")
				return
			}
			out.Data[p.name] = nil
			continue
		}
		out.Data[p.name] = sharelinks.RedactJSON(body, sh.HidePrompts)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// badge handles GET /shared/{token}/badge.svg: the overall score of a
// report card or evaluation, or a comparison's action. It may be embedded
// on any site (a README), so it is the one answer marked cross-origin.
func (h *shareHandler) badge(ctx *fasthttp.RequestCtx) {
	sh, ok := h.verify(ctx)
	if !ok {
		return
	}
	label := "heimdall"
	if sh.Label != "" && len(sh.Label) <= 40 {
		label = sh.Label
	}
	var svg string
	switch sh.Kind {
	case sharelinks.KindEvaluation, sharelinks.KindReportCard:
		parts := sharedParts(sh.Kind, sh.Target, sh.Query)
		p := parts[len(parts)-1]
		status, body, _ := h.sub(ctx, p.path)
		if status != fasthttp.StatusOK {
			svg = sharelinks.Badge(label, "not found", "")
			break
		}
		var rep *reportcard.Report
		if sh.Kind == sharelinks.KindReportCard {
			var card AgentReportCard
			if json.Unmarshal(body, &card) == nil {
				rep = card.Report
			}
		} else {
			rep = &reportcard.Report{}
			if json.Unmarshal(body, rep) != nil {
				rep = nil
			}
		}
		svg = sharelinks.ReportBadge(rep, label)
	case sharelinks.KindVerdict:
		status, body, _ := h.sub(ctx, APIPrefix+"/experiments/"+url.PathEscape(sh.Target)+"/verdict")
		var v schemas.VerdictPayload
		if status != fasthttp.StatusOK || json.Unmarshal(body, &v) != nil {
			svg = sharelinks.Badge("verdict", "pending", "")
			break
		}
		svg = sharelinks.VerdictBadge(string(v.Verdict.Action), string(v.Verdict.Decision))
	default:
		svg = sharelinks.Badge(label, sh.Kind, "")
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("image/svg+xml; charset=utf-8")
	ctx.Response.Header.Set("Cache-Control", "no-cache, max-age=0")
	ctx.Response.Header.Set(lib.CORPHeader, lib.CORPCrossOrigin)
	ctx.SetBodyString(svg)
}

// export handles GET /shared/{token}/export?format=html|md: the shared
// evaluation's report card as a standalone document.
func (h *shareHandler) export(ctx *fasthttp.RequestCtx) {
	sh, ok := h.verify(ctx)
	if !ok {
		return
	}
	if sh.Kind != sharelinks.KindEvaluation {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "only a shared evaluation can be exported")
		return
	}
	format := exportFormat(ctx)
	if format == sharelinks.FormatJSON {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "format must be html or md")
		return
	}
	status, body, ctype := h.sub(ctx, APIPrefix+"/evaluations/"+url.PathEscape(sh.Target)+"/report/export?format="+format)
	if status != fasthttp.StatusOK {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "the shared item no longer exists")
		return
	}
	sendDocument(ctx, ctype, sh.Target+"-report-card."+format, body)
}

func exportFormat(ctx *fasthttp.RequestCtx) string {
	switch f := strings.ToLower(strings.TrimSpace(string(ctx.QueryArgs().Peek("format")))); f {
	case "markdown", "md":
		return sharelinks.FormatMarkdown
	case "json":
		return sharelinks.FormatJSON
	}
	return sharelinks.FormatHTML
}

func sendDocument(ctx *fasthttp.RequestCtx, contentType, filename string, body []byte) {
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType(contentType)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if !bytes.ContainsAny([]byte(filename), "\"\\\r\n") {
		disp := "attachment"
		if string(ctx.QueryArgs().Peek("inline")) == "1" {
			disp = "inline"
		}
		ctx.Response.Header.Set("Content-Disposition", disp+`; filename="`+filename+`"`)
	}
	ctx.SetBody(body)
}

// reportExport handles GET /evaluations/{id}/report/export?format=html|md|json.
// html is a standalone page with a print stylesheet (the browser's Print,
// Save as PDF makes the PDF); ?inline=1 shows it instead of downloading.
func (h *shareHandler) reportExport(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "evaluation")
	if !ok {
		return
	}
	rc := &reportCardHandler{env: h.env}
	if h.env.Cfg != nil && h.env.Cfg.Root != "" {
		rc.evals = NewFileEvaluations(h.env.Cfg.Root)
	}
	if !rc.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ev, err := rc.evals.Get(work, id)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluation", err)
		return
	}
	rep, err := reportcard.Build(work, LeaderboardSource(h.env.Services.Index), ev, rc.options(work))
	if err != nil {
		sendDepError(ctx, h.env.Mode, "report card", err)
		return
	}
	clean := *ev
	clean.CreatedBy = ""
	doc := sharelinks.ReportDoc{Evaluation: &clean, Report: rep, Generated: h.now()}
	if h.env.Cfg != nil {
		doc.Source = h.env.Cfg.Auth.PublicURL
	}
	switch exportFormat(ctx) {
	case sharelinks.FormatMarkdown:
		sendDocument(ctx, "text/markdown; charset=utf-8", id+"-report-card.md", []byte(doc.Markdown()))
	case sharelinks.FormatJSON:
		body, err := json.MarshalIndent(struct {
			Evaluation *schemas.Evaluation `json:"evaluation"`
			Report     *reportcard.Report  `json:"report"`
		}{&clean, sharelinks.Visible(rep)}, "", "  ")
		if err != nil {
			sendDepError(ctx, h.env.Mode, "report card", err)
			return
		}
		sendDocument(ctx, "application/json; charset=utf-8", id+"-report-card.json", body)
	default:
		sendDocument(ctx, "text/html; charset=utf-8", id+"-report-card.html", []byte(doc.HTML()))
	}
}
