package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/replayrec"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/shadowreplay"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// Lane W4-47: shadow replay. The settings (the org's opt-in), the list of
// replays, one replay's report, and sending a replayed session to the
// failure inbox. Replays are started with POST /jobs kind replay.run.

func init() {
	RegisterOrgArea("replays", registerReplays)
}

type replaysHandler struct{ env OrgEnv }

func registerReplays(env OrgEnv, r *router.Router) {
	h := &replaysHandler{env: env}
	r.GET(APIPrefix+"/replays/settings", env.R(h.getSettings))
	r.PUT(APIPrefix+"/replays/settings", env.W(h.putSettings))
	r.GET(APIPrefix+"/replays", env.R(h.list))
	r.GET(APIPrefix+"/replays/{id}", env.R(h.get))
	r.POST(APIPrefix+"/replays/{id}/sessions/{recording}/to-inbox", env.W(h.toInbox))
}

func (h *replaysHandler) root(ctx *fasthttp.RequestCtx) (string, bool) {
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "project files are not available on this server")
		return "", false
	}
	return h.env.Cfg.Root, true
}

func (h *replaysHandler) getSettings(ctx *fasthttp.RequestCtx) {
	root, ok := h.root(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, shadowreplay.LoadSettings(root))
}

func (h *replaysHandler) putSettings(ctx *fasthttp.RequestCtx) {
	root, ok := h.root(ctx)
	if !ok {
		return
	}
	var s shadowreplay.Settings
	if err := json.Unmarshal(ctx.PostBody(), &s); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the settings are not valid JSON")
		return
	}
	by := ""
	if p := auth.PrincipalFrom(ctx); p != nil {
		by = p.Email
	}
	out, err := shadowreplay.SaveSettings(root, s, by, time.Now())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// ReplayList is the answer of GET /replays.
type ReplayList struct {
	Replays []shadowreplay.ReportRow `json:"replays"`
}

func (h *replaysHandler) list(ctx *fasthttp.RequestCtx) {
	root, ok := h.root(ctx)
	if !ok {
		return
	}
	rows, err := shadowreplay.ListReports(root)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list replays: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ReplayList{Replays: rows})
}

func (h *replaysHandler) report(ctx *fasthttp.RequestCtx, root string) (*shadowreplay.RunReport, bool) {
	id, _ := ctx.UserValue("id").(string)
	rep, err := shadowreplay.LoadReport(root, id)
	if errors.Is(err, shadowreplay.ErrNotFound) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "There is no replay with this id.")
		return nil, false
	}
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the replay: "+err.Error())
		return nil, false
	}
	return rep, true
}

func (h *replaysHandler) get(ctx *fasthttp.RequestCtx) {
	root, ok := h.root(ctx)
	if !ok {
		return
	}
	if rep, ok := h.report(ctx, root); ok {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, rep)
	}
}

// replayInboxSource names the replay as the source of an inbox item.
const replayInboxSource = "shadow-replay"

func (h *replaysHandler) toInbox(ctx *fasthttp.RequestCtx) {
	root, ok := h.root(ctx)
	if !ok {
		return
	}
	rep, ok := h.report(ctx, root)
	if !ok {
		return
	}
	recID, _ := ctx.UserValue("recording").(string)
	idx := -1
	for i, s := range rep.Sessions {
		if s.Recording == recID {
			idx = i
		}
	}
	if idx < 0 {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "This replay has no such session.")
		return
	}
	sess := rep.Sessions[idx]
	rec, err := shadowreplay.LoadRecording(root, recID)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "The recording of this session is no longer stored.")
		return
	}
	trace, err := replayConversation(rec)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	}
	step := len(sess.Steps)
	if sess.DivergedAt != nil {
		step = *sess.DivergedAt + 1
	}
	reason := fmt.Sprintf("shadow replay: %s at step %d with %s", sess.Final, step, rep.Candidate)
	dir := h.env.Cfg.Path(traceinbox.Dir)
	cfg, err := traceinbox.LoadConfig(h.env.Cfg.Path(traceinbox.Dir + "/" + traceinbox.SourcesFile))
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	}
	ib := traceinbox.New(dir, cfg, h.env.Cfg.Path(h.env.Cfg.BenchmarksRoot))
	ib.ProjectRoot = root
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	res, err := ib.Ingest(work, &traceinbox.Source{Name: replayInboxSource, Kind: traceinbox.KindWebhook},
		traceinbox.IngestRequest{Trace: trace, Format: "chat", Reason: reason, Source: replayInboxSource})
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	rep.Sessions[idx].InboxItem = res.ItemID
	_ = shadowreplay.SaveReport(root, rep)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, schemas.InboxIngestResult{ItemID: res.ItemID, Created: res.Created, Occurrences: res.Occurrences, Skipped: res.Skipped})
}

// replayConversation is the recording as one chat conversation: the last
// recorded request's messages plus the recorded final answer.
func replayConversation(rec *replayrec.Recording) (json.RawMessage, error) {
	if len(rec.Calls) == 0 {
		return nil, errors.New("The recording has no model calls.")
	}
	last := rec.Calls[len(rec.Calls)-1]
	var body struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(last.Request, &body) != nil || len(body.Messages) == 0 {
		return nil, errors.New("The recording has no stored request for its last call.")
	}
	final := map[string]any{"role": "assistant", "content": last.Answer.Text}
	if len(last.Answer.ToolCalls) > 0 {
		var calls []any
		for _, c := range last.Answer.ToolCalls {
			calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": c.Arguments}})
		}
		final["tool_calls"] = calls
	}
	fb, _ := json.Marshal(final)
	msgs := append(body.Messages, fb)
	return json.Marshal(map[string]any{"id": rec.SessionID, "messages": msgs})
}
