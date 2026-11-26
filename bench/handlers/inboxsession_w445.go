package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// Wave 4 (W4-45): a Claude Code or Codex session file uploaded from the
// inbox page lands in the failure inbox like any pushed trace (parsed,
// redacted, deduplicated), and the model's draft of what should have
// happened is read back from inbox/items/<id>/proposal.json.

// InboxImportSessionPath and InboxProposalPath are the two routes.
const (
	InboxImportSessionPath = APIPrefix + "/inbox/import-session"
	InboxProposalPath      = APIPrefix + "/inbox/{id}/proposal"
)

// SessionImportMaxBytes caps an uploaded session file.
const SessionImportMaxBytes = 32 << 20

// sessionImportSource is the inbox source an uploaded session is kept under.
const sessionImportSource = "session-file"

const (
	msgSessionTooLarge = "This file is larger than 32 MB. Export a shorter session."
	msgNotSessionFile  = "This is not a Claude Code or Codex session file."
)

// uploadChain is middlewares with the trailing body limit raised to fit a
// session file (the body limit is always the chain's last middleware, as
// server.uploadMiddlewares assumes). The route itself refuses a body over
// SessionImportMaxBytes with a plain sentence.
func (h *InboxHandler) uploadChain(middlewares []lib.Middleware) []lib.Middleware {
	out := slices.Clone(middlewares)
	if len(out) > 0 {
		out[len(out)-1] = lib.BodyLimit(h.mode, SessionImportMaxBytes+1<<20)
	}
	return append(out, h.write)
}

// postImportSession handles POST /api/bench/inbox/import-session: the body
// is the raw session file, X-File-Name names it.
func (h *InboxHandler) postImportSession(ctx *fasthttp.RequestCtx) {
	if h.inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	if !h.liveOnly(ctx) {
		return
	}
	svc, ok := h.inbox.(*traceinbox.Service)
	if !ok || svc.Inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	data := ctx.PostBody()
	if len(data) > SessionImportMaxBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, msgSessionTooLarge)
		return
	}
	tr, err := traceimport.Parse(data, "")
	if err != nil || (tr.Format != traceimport.FormatClaudeCode && tr.Format != traceimport.FormatCodex) {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, msgNotSessionFile)
		return
	}
	lines, err := sessionLinesArray(data)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, msgNotSessionFile)
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	src := &traceinbox.Source{Name: sessionImportSource, Kind: traceinbox.KindWebhook}
	res, err := svc.Inbox.Ingest(work, src, traceinbox.IngestRequest{Trace: lines, Format: tr.Format, Reason: "imported session file", Source: sessionImportSource})
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "import the session: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.InboxIngestResult{ItemID: res.ItemID, Created: res.Created, Occurrences: res.Occurrences, Skipped: res.Skipped})
}

// sessionLinesArray turns the JSON values of a JSON Lines file into one JSON
// array, because a pushed trace is one JSON value.
func sessionLinesArray(data []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var lines []json.RawMessage
	for {
		var v json.RawMessage
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		lines = append(lines, v)
	}
	return json.Marshal(lines)
}
