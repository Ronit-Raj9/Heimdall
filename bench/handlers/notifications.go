package handlers

// Email notifications (lane Y05): each member chooses for themselves which
// events reach them by email and whether they get the weekly digest
// (package notify). Routes act on the caller only; nobody sets another
// member's email.

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/mail"
	"github.com/maximhq/bifrost/bench/notify"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// EmailSettings is GET /notifications/email: the caller's own choice.
type EmailSettings struct {
	// Available is false on a server without accounts (local) or without
	// a signed-in member (API tokens); Reason says why.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Delivers is false when the server has no mail transport: messages
	// are only written to the server log until the operator sets one.
	Delivers bool         `json:"delivers"`
	Provider string       `json:"provider"`
	Email    string       `json:"email,omitempty"`
	Events   []string     `json:"events"` // what can be chosen
	Prefs    notify.Prefs `json:"prefs"`
}

// EmailSettingsInput is PUT /notifications/email.
type EmailSettingsInput struct {
	Events []string `json:"events"`
	Digest bool     `json:"digest"`
}

// EmailTestResult is POST /notifications/email/test.
type EmailTestResult struct {
	To       string `json:"to"`
	Delivers bool   `json:"delivers"`
	Provider string `json:"provider"`
}

type notificationsHandler struct {
	mode schemas.Mode
	root string
}

func init() {
	RegisterOrgArea("notifications", func(env OrgEnv, r *router.Router) {
		h := &notificationsHandler{mode: env.Mode}
		if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		p := APIPrefix + "/notifications/email"
		r.GET(p, env.R(h.get))
		r.PUT(p, env.W(h.put))
		r.POST(p+"/test", env.W(h.test))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/notifications/email", Tag: "integrations", Summary: "Your own email notifications: which events reach you by email, the weekly digest, and whether this server can send email", Response: EmailSettings{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/notifications/email", Tag: "integrations", Summary: "Choose which events reach you by email (evaluation.finished, evaluation.regressed, release.blocked, schedule.failed, worker.offline) and whether you get the weekly digest",
			Auth: AuthWrite, Request: EmailSettingsInput{}, Response: EmailSettings{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/notifications/email/test", Tag: "integrations", Summary: "Send a test email to your own address now",
			Auth: AuthWrite, Response: EmailTestResult{}},
	)
}

// caller is the signed-in member, or why email settings are not available.
func (h *notificationsHandler) caller(ctx *fasthttp.RequestCtx) (auth.Principal, *notify.Hub, string) {
	var p auth.Principal
	if pp := auth.PrincipalFrom(ctx); pp != nil {
		p = *pp
	}
	hub := notify.Default()
	switch {
	case h.root == "":
		return p, hub, "this server has no project folder"
	case !hub.EmailAvailable():
		return p, hub, "this server has no member accounts, so it sends no member email"
	case p.UserID == "" || p.Email == "":
		return p, hub, "email settings belong to a signed-in member, not to an API token"
	}
	return p, hub, ""
}

func (h *notificationsHandler) view(p auth.Principal, hub *notify.Hub, reason string) (EmailSettings, error) {
	out := EmailSettings{Available: reason == "", Reason: reason, Events: notify.EmailEvents, Prefs: notify.DefaultPrefs(), Provider: mail.ProviderLog}
	if hub != nil && hub.Mail != nil {
		out.Delivers, out.Provider = mail.Delivers(hub.Mail), mail.ProviderOf(hub.Mail)
	}
	if reason != "" {
		return out, nil
	}
	out.Email = p.Email
	prefs, err := notify.Open(h.root).Prefs(p.UserID)
	out.Prefs = prefs
	return out, err
}

func (h *notificationsHandler) get(ctx *fasthttp.RequestCtx) {
	p, hub, reason := h.caller(ctx)
	out, err := h.view(p, hub, reason)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "email settings: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *notificationsHandler) put(ctx *fasthttp.RequestCtx) {
	p, hub, reason := h.caller(ctx)
	if reason != "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "email notifications are not available: "+reason)
		return
	}
	var in EmailSettingsInput
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be JSON {events, digest}: "+err.Error())
		return
	}
	if _, err := notify.Open(h.root).SetPrefs(p.UserID, notify.Prefs{Events: in.Events, Digest: in.Digest}); err != nil {
		status, typ := fasthttp.StatusInternalServerError, lib.ErrTypeInternal
		if errors.Is(err, notify.ErrInvalid) {
			status, typ = fasthttp.StatusBadRequest, lib.ErrTypeBadRequest
		}
		lib.SendError(ctx, h.mode, status, typ, err.Error())
		return
	}
	out, err := h.view(p, hub, "")
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "email settings: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *notificationsHandler) test(ctx *fasthttp.RequestCtx) {
	p, hub, reason := h.caller(ctx)
	if reason != "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "email notifications are not available: "+reason)
		return
	}
	wctx, cancel := workContext(ctx, 40*time.Second)
	defer cancel()
	if err := hub.SendTest(wctx, p.OrgID, p.Email); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, "the test email was not sent: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, EmailTestResult{To: p.Email, Delivers: mail.Delivers(hub.Mail), Provider: mail.ProviderOf(hub.Mail)})
}
