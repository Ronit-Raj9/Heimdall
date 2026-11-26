package handlers

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// ProposalFile is the file name of a model draft inside an item's folder
// (inbox/items/<id>/proposal.json), written by the trace.draft job (W4-45).
const ProposalFile = "proposal.json"

const msgNoDraft = "No draft yet."

// getProposal handles GET /api/bench/inbox/{id}/proposal: the model's draft
// for an item, written only by the trace.draft job.
func (h *InboxHandler) getProposal(ctx *fasthttp.RequestCtx) {
	id, ok := h.itemID(ctx)
	if !ok {
		return
	}
	svc, ok := h.inbox.(*traceinbox.Service)
	if !ok || svc.Inbox == nil {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	fs, ok := svc.Inbox.Store.(*traceinbox.FileStore)
	if !ok {
		sendUnavailable(ctx, h.mode, "the failure inbox")
		return
	}
	data, err := os.ReadFile(filepath.Join(fs.Dir(), traceinbox.ItemsDir, id, ProposalFile))
	if errors.Is(err, os.ErrNotExist) {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, msgNoDraft)
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "the draft", err)
		return
	}
	var v json.RawMessage
	if json.Unmarshal(data, &v) != nil {
		sendDepError(ctx, h.mode, "the draft", errors.New("the draft file is not valid JSON"))
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
}
