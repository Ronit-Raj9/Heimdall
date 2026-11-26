package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// W4-49 F: the user feedback endpoint. The owner's app sends one request
// when a user presses thumbs down (or up); the session lands in the org's
// Inbox. It takes the same per-org ingest tokens as the hosted ingest route
// and checks them in the same order: token 401, body 413, rate 429.

// FeedbackPath is the hosted feedback route; {org} is an org id or slug.
const FeedbackPath = APIPrefix + "/orgs/{org}/feedback"

// FeedbackMaxBytes caps one feedback body.
const FeedbackMaxBytes = 64 << 10

// FeedbackMaxComment caps the comment, in characters.
const FeedbackMaxComment = 2000

// FeedbackLookback is how far back the Bifrost logs are searched.
const FeedbackLookback = 7 * 24 * time.Hour

// FeedbackFile is the feedback log under the inbox folder.
const FeedbackFile = "feedback.jsonl"

// FeedbackNoSession is the note of a feedback saved with no session found.
const FeedbackNoSession = "Saved. No session was found to attach: send the trace with the feedback, or add a Bifrost source."

// FeedbackRequest is the body of POST /orgs/{org}/feedback.
type FeedbackRequest struct {
	SessionID string          `json:"session_id"`
	Rating    string          `json:"rating,omitempty"` // down or up
	Score     *float64        `json:"score,omitempty"`  // 0 to 10: 0 to 3 is down, 8 to 10 is up
	Comment   string          `json:"comment,omitempty"`
	Trace     json.RawMessage `json:"trace,omitempty"`
	Source    string          `json:"source,omitempty"`
	User      string          `json:"user,omitempty"`
}

// FeedbackResult answers the feedback route.
type FeedbackResult struct {
	ItemID  string `json:"item_id"`
	Created bool   `json:"created"`
	Note    string `json:"note"`
}

// feedbackLine is one line of inbox/feedback.jsonl.
type feedbackLine struct {
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id"`
	Rating    string    `json:"rating"`
	Comment   string    `json:"comment,omitempty"`
	ItemID    string    `json:"item_id"`
	Source    string    `json:"source,omitempty"`
	UserHash  string    `json:"user_hash,omitempty"`
}

// FeedbackHandler serves the feedback route next to the hosted inbox token
// routes, sharing their token store, org directory and inbox opener.
type FeedbackHandler struct {
	*InboxTokensHandler
	fbMu    sync.Mutex
	fbGates map[string]*traceinbox.Gate
	fileMu  sync.Mutex
}

// NewFeedbackHandler wraps the inbox tokens handler; RegisterRoutes
// registers both.
func NewFeedbackHandler(h *InboxTokensHandler) *FeedbackHandler {
	return &FeedbackHandler{InboxTokensHandler: h, fbGates: map[string]*traceinbox.Gate{}}
}

// RegisterRoutes registers the inbox token routes and the feedback route.
func (h *FeedbackHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	h.InboxTokensHandler.RegisterRoutes(r, middlewares...)
	r.POST(FeedbackPath, lib.ChainMiddlewares(h.feedback, middlewares...))
}

func (h *FeedbackHandler) fbGate(orgID string) *traceinbox.Gate {
	h.fbMu.Lock()
	defer h.fbMu.Unlock()
	g, ok := h.fbGates[orgID]
	if !ok {
		g = traceinbox.NewGate("", h.RatePerMinute, FeedbackMaxBytes)
		h.fbGates[orgID] = g
	}
	return g
}

// feedbackRating maps rating or score to down, up or "" (neither).
func feedbackRating(req FeedbackRequest) (string, bool) {
	if req.Score != nil {
		s := *req.Score
		switch {
		case s < 0 || s > 10:
			return "", false
		case s <= 3:
			return "down", true
		case s >= 8:
			return "up", true
		}
		return "", true
	}
	switch req.Rating {
	case "down", "up":
		return req.Rating, true
	}
	return "", false
}

// feedbackUserHash is the first 16 hex characters of the user's sha256.
func feedbackUserHash(u string) string {
	if u == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])[:16]
}

func (h *FeedbackHandler) feedback(ctx *fasthttp.RequestCtx) {
	if h.mode != schemas.ModeLive {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "inbox writes are refused in "+string(h.mode)+" mode")
		return
	}
	if h.tokens == nil || h.dir == nil || h.open == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the feedback route is not configured on this server: it needs the server database")
		return
	}
	raw := strings.TrimSpace(string(ctx.Request.Header.Peek("Authorization")))
	raw, hasBearer := strings.CutPrefix(raw, "Bearer ")
	raw = strings.TrimSpace(raw)
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
			sendDepError(ctx, h.mode, "inbox token", err)
			return
		}
		h.unauthorized(ctx)
		return
	}
	if len(ctx.PostBody()) > FeedbackMaxBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "feedback larger than 64 KiB; send one session")
		return
	}
	if !h.fbGate(org.ID).Allow(h.now()) {
		ctx.Response.Header.Set("Retry-After", "60")
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeUnavailable, "this org's feedback rate limit was reached; retry later")
		return
	}
	var req FeedbackRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "session_id is required")
		return
	}
	rating, ok := feedbackRating(req)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "rating must be down or up, or score a number from 0 to 10")
		return
	}
	if len([]rune(req.Comment)) > FeedbackMaxComment {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "comment is longer than 2000 characters")
		return
	}
	comment := traceimport.NewRedactor().Text(req.Comment)
	ib, _, err := h.open(work, org)
	if err != nil {
		sendDepError(ctx, h.mode, "open the org's inbox", err)
		return
	}
	src := &traceinbox.Source{Name: traceinbox.FeedbackSourceName, Kind: traceinbox.KindWebhook}
	res, status, err := h.attach(work, ib, src, req, rating, comment)
	if err != nil {
		(&InboxHandler{mode: h.mode}).sendErr(ctx, "feedback", err)
		return
	}
	h.appendFeedback(ib, feedbackLine{At: h.now().UTC(), SessionID: req.SessionID, Rating: rating, Comment: comment,
		ItemID: res.ItemID, Source: oneLineCut(req.Source, 60), UserHash: feedbackUserHash(req.User)})
	lib.SendJSON(ctx, h.mode, status, res)
}

// attach puts the session into the inbox: a sent trace first, else the
// first Bifrost source is asked for the session. A rating that is neither
// down nor up, or no session found, is only logged (202).
func (h *FeedbackHandler) attach(ctx context.Context, ib *traceinbox.Inbox, src *traceinbox.Source, req FeedbackRequest, rating, comment string) (FeedbackResult, int, error) {
	if rating == "" {
		return FeedbackResult{}, fasthttp.StatusAccepted, nil
	}
	reason := "user feedback: thumbs " + rating
	if comment != "" {
		reason += ": " + comment
	}
	passed := rating == "up"
	if len(req.Trace) > 0 && string(req.Trace) != "null" {
		out, err := ib.Ingest(ctx, src, traceinbox.IngestRequest{Trace: req.Trace, Reason: reason, TraceID: req.SessionID, Passed: passed})
		if err != nil {
			return FeedbackResult{}, 0, errors.Join(traceinbox.ErrInvalid, err)
		}
		return feedbackOut(out), feedbackStatus(out), nil
	}
	bsrc, ok := ib.Config.FirstBifrost()
	if !ok {
		return FeedbackResult{Note: FeedbackNoSession}, fasthttp.StatusAccepted, nil
	}
	s, err := ib.BifrostSession(ctx, bsrc, req.SessionID, h.now().Add(-FeedbackLookback))
	if err != nil {
		return FeedbackResult{Note: FeedbackNoSession}, fasthttp.StatusAccepted, nil
	}
	out, err := ib.IngestSession(src, s, req.SessionID, []string{reason}, passed)
	if err != nil {
		return FeedbackResult{}, 0, err
	}
	return feedbackOut(out), feedbackStatus(out), nil
}

func feedbackOut(out *traceinbox.IngestResult) FeedbackResult {
	return FeedbackResult{ItemID: out.ItemID, Created: out.Created}
}

func feedbackStatus(out *traceinbox.IngestResult) int {
	if out.Created {
		return fasthttp.StatusCreated
	}
	return fasthttp.StatusOK
}

// appendFeedback appends one line to inbox/feedback.jsonl. A failed write is
// not the caller's problem: the item, if any, is already in the inbox.
func (h *FeedbackHandler) appendFeedback(ib *traceinbox.Inbox, line feedbackLine) {
	d, ok := ib.Store.(interface{ Dir() string })
	if !ok {
		return
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	h.fileMu.Lock()
	defer h.fileMu.Unlock()
	if err := os.MkdirAll(d.Dir(), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(d.Dir(), FeedbackFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// oneLineCut keeps the first line of s, at most n characters.
func oneLineCut(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
