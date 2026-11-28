package handlers

import (
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tracedraft"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// InboxProposalEstimatePath prices a "Draft with a model" before it starts
// (lane F1, PT-8): GET ?model=<id>&kind=<kind>.
const InboxProposalEstimatePath = APIPrefix + "/inbox/{id}/proposal/estimate"

// ProposalEstimate is the cost of one draft of an item with one model.
// Priced is false when the price list does not know the model; the amounts
// are then 0 and must not be shown as a cost.
type ProposalEstimate struct {
	Model        string  `json:"model"`
	Priced       bool    `json:"priced"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	USD          float64 `json:"usd"`     // one call, the usual case
	MaxUSD       float64 `json:"max_usd"` // with the quote repair round
}

// estimateProposal handles GET InboxProposalEstimatePath.
func (h *InboxHandler) estimateProposal(ctx *fasthttp.RequestCtx) {
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
	model := strings.TrimSpace(string(ctx.QueryArgs().Peek("model")))
	kind := strings.TrimSpace(string(ctx.QueryArgs().Peek("kind")))
	if kind == "" {
		kind = traceimport.Kinds()[0]
	}
	sess, err := fs.Session(id)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "This item has no session to draft from.")
		return
	}
	in, out := tracedraft.EstimateTokens(id, sess, kind)
	est := ProposalEstimate{Model: model, InputTokens: in, OutputTokens: out}
	if price, ok := listPrice(model); ok {
		one := (float64(in)*price.InputPerMTok + float64(out)*price.OutputPerMTok) / 1e6
		// The repair round resends the prompt plus the first reply.
		repair := (float64(in+out)*price.InputPerMTok + float64(out)*price.OutputPerMTok) / 1e6
		est.Priced = true
		est.USD = proposalMilli(one)
		est.MaxUSD = proposalMilli(one + repair)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, est)
}

// proposalMilli rounds up to the next tenth of a cent, so a small cost never shows as 0.
func proposalMilli(v float64) float64 { return math.Ceil(v*1000) / 1000 }
