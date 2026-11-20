package evidence

import (
	"context"
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Cost is the name of the cost ledger source.
const Cost = "cost"

// costFile is the run folder file holding the cost rows.
const costFile = "cost.json"

var _ schemas.EvidenceSource = (*costSource)(nil)

// costSource prices every physical upstream attempt from the pinned snapshot
// (DESIGN.md 11, cost ledger). It runs last because it reads the gateway rows
// and the proxy ledger.
type costSource struct {
	prices *PriceTable
	logger schemas.Logger
}

// GetName implements schemas.Part.
func (s *costSource) GetName() string { return Cost }

// GetVersion implements schemas.Part.
func (s *costSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. Without a gateway log there is
// nothing to price and no rows are written; metrics then read cost as
// unknown. Each request's total is also put on its model_response event so
// the trace shows spend per turn, and events.jsonl is rewritten to carry it.
func (s *costSource) Collect(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if ev.Requests == nil {
		return nil
	}
	if s.prices == nil {
		pickLogger(s.logger, rc).Warn("no price table; cost rows are unknown", "run_id", runID(rc))
	}
	rows := CostRows(ev.Requests, ev.Ledger, s.prices)
	if len(ev.LedgerBreaks) > 0 {
		// Rows were matched against a ledger whose chain does not verify:
		// no price derived from it is known (audit 3-B-07).
		for i := range rows {
			rows[i].USD = schemas.Unknown("proxy ledger hash chain broken", schemas.EvidenceGateway)
		}
	}
	ev.Cost = rows
	if err := writeJSON(rc.Dir, costFile, rows); err != nil {
		return fmt.Errorf("write cost rows for run %s: %w", runID(rc), err)
	}
	totals := requestTotals(rows)
	for i := range ev.Events {
		e := &ev.Events[i]
		if e.Source != schemas.SourceGateway || e.Type != schemas.EventModelResponse {
			continue
		}
		if v, ok := totals[e.RequestID]; ok {
			e.USD = &v
		}
	}
	return writeEvents(rc, ev)
}

// CostRows returns one row per physical attempt of every request in the log,
// in request time order. It is pure.
//
// A request without an attempt trail counts as one attempt. Only the attempt
// that returned the response carries the row's usage and Bifrost's own cost.
// An injected failure (ledger action inject, or a 5xx status) costs 0 with
// FaultInject set, and a request the provider rejected with a 4xx costs 0,
// because providers bill neither. Requests that are not conversation turns
// (token counting, model listing) are not_applicable. Usage that was not
// recorded, or an attempt that failed without a status, is unknown. A model
// the table does not list is unknown, bounded above by the table's highest
// price per bucket when the tokens are known.
//
// Ledger entries are matched to message attempts in order only when the
// ledger comes from one slot (its call counter is then a total order), both
// sides have the same count, and the pairing contradicts no recorded status or
// provider response id; otherwise only by unique provider response id. A matched ledger entry supplies usage the gateway did not record.
func CostRows(log *schemas.RequestLog, ledger []schemas.LedgerEntry, prices *PriceTable) []schemas.CostRow {
	if log == nil {
		return nil
	}
	attempts := physicalAttempts(log.Requests)
	matched := matchLedger(attempts, ledger)
	rows := make([]schemas.CostRow, 0, len(attempts))
	for i, a := range attempts {
		rows = append(rows, priceAttempt(a, matched[i], prices))
	}
	return rows
}

// physAttempt is one physical upstream try of a request.
type physAttempt struct {
	req    schemas.GatewayRequest
	n      int
	status int  // HTTP status, 0 when none was recorded
	last   bool // the row's last attempt: carries Bifrost's cost
	final  bool // the attempt that returned the response: carries usage
}

// physicalAttempts expands every request into its attempts, oldest first.
func physicalAttempts(reqs []schemas.GatewayRequest) []physAttempt {
	sorted := slices.Clone(reqs)
	slices.SortStableFunc(sorted, func(a, b schemas.GatewayRequest) int {
		return a.Timestamp.Compare(b.Timestamp)
	})
	var out []physAttempt
	for _, r := range sorted {
		trail := r.Attempts
		if len(trail) == 0 || r.CacheHit || r.Kind != kindMessage {
			trail = []schemas.Attempt{{N: 1, StatusCode: rowStatus(r)}}
		}
		for i, a := range trail {
			n := a.N
			if n == 0 {
				n = i + 1
			}
			last := i == len(trail)-1
			out = append(out, physAttempt{
				req:    r,
				n:      n,
				status: a.StatusCode,
				last:   last,
				final:  last && r.Status == statusSuccess,
			})
		}
	}
	return out
}

// rowStatus is the HTTP status a row without an attempt trail implies.
func rowStatus(r schemas.GatewayRequest) int {
	switch r.Status {
	case statusSuccess:
		return 200
	case statusError:
		return r.ErrorStatus
	default:
		return 0
	}
}

// matchLedger returns the ledger entry of each attempt, or nil.
func matchLedger(attempts []physAttempt, ledger []schemas.LedgerEntry) []*schemas.LedgerEntry {
	out := make([]*schemas.LedgerEntry, len(attempts))
	var msgs []schemas.LedgerEntry
	for _, e := range ledger {
		if e.Kind == kindMessage {
			msgs = append(msgs, e)
		}
	}
	slices.SortStableFunc(msgs, func(a, b schemas.LedgerEntry) int { return a.Call - b.Call })
	var idx []int
	for i, a := range attempts {
		if a.req.Kind == kindMessage && !a.req.CacheHit {
			idx = append(idx, i)
		}
	}
	if len(idx) == len(msgs) && singleSlot(msgs) && statusesAgree(attempts, idx, msgs) && responseIDsAgree(attempts, idx, msgs) {
		for j, i := range idx {
			out[i] = &msgs[j]
		}
		return out
	}
	byResp := map[string]*schemas.LedgerEntry{}
	dup := map[string]bool{}
	for j := range msgs {
		id := msgs[j].ResponseID
		if id == "" {
			continue
		}
		if _, seen := byResp[id]; seen {
			dup[id] = true // ambiguous: never borrow tokens from either
		}
		byResp[id] = &msgs[j]
	}
	for id := range dup {
		delete(byResp, id)
	}
	for _, i := range idx {
		if a := attempts[i]; a.final && a.req.ResponseID != "" {
			out[i] = byResp[a.req.ResponseID]
		}
	}
	return out
}

// singleSlot reports whether every entry comes from one slot. Call counters
// restart at one per slot, so ordering by Call across slots is not the order
// the requests happened in.
func singleSlot(msgs []schemas.LedgerEntry) bool {
	for _, e := range msgs {
		if e.Slot != msgs[0].Slot {
			return false
		}
	}
	return true
}

// responseIDsAgree reports whether pairing attempts and ledger entries in
// order never contradicts a recorded provider response id.
func responseIDsAgree(attempts []physAttempt, idx []int, msgs []schemas.LedgerEntry) bool {
	for j, i := range idx {
		a, e := attempts[i].req.ResponseID, msgs[j].ResponseID
		if attempts[i].final && a != "" && e != "" && a != e {
			return false
		}
	}
	return true
}

// statusesAgree reports whether pairing attempts and ledger entries in order
// never contradicts a recorded status.
func statusesAgree(attempts []physAttempt, idx []int, msgs []schemas.LedgerEntry) bool {
	for j, i := range idx {
		a, e := attempts[i].status, msgs[j].Status
		if a != 0 && e != 0 && a != e {
			return false
		}
	}
	return true
}

// priceAttempt builds the cost row of one attempt.
func priceAttempt(a physAttempt, led *schemas.LedgerEntry, prices *PriceTable) schemas.CostRow {
	r := a.req
	model := r.ModelServed
	if model == "" {
		model = r.ModelRequested
	}
	row := schemas.CostRow{RequestID: r.ID, Attempt: a.n, Model: model, PriceRef: prices.Ref()}
	if a.last && r.BifrostUSD != nil {
		v := *r.BifrostUSD
		row.BifrostUSD = &v
	}
	// Fault provenance comes only from the proxy witness: a genuine upstream
	// 5xx is recorded with action pass and is an ordinary failed attempt.
	injected := led != nil && led.Action == actionInject
	switch {
	case r.Kind == kindAudio && injected: // X8 voice agents: an injected audio fault (an STT outage) is not billed
		row.USD = schemas.Observed(0, schemas.EvidenceGateway)
		row.FaultInject = true
	case r.Kind == kindAudio && a.status >= 400:
		row.USD = schemas.Observed(0, schemas.EvidenceGateway)
	case r.Kind == kindAudio && r.Status != statusProcessing: // X8 voice agents (priceAudio)
		row.Tokens, row.USD = priceAudio(r, a)
		row.PriceRef = audioPriceRef
	case r.Kind != kindMessage:
		row.USD = schemas.NotApplicable()
		row.USD.Reason = "not billed: " + r.Kind + " request"
	case r.CacheHit:
		row.USD = schemas.Observed(0, schemas.EvidenceGateway)
	case injected:
		row.USD = schemas.Observed(0, schemas.EvidenceGateway)
		row.FaultInject = true
	case a.status >= 400:
		row.USD = schemas.Observed(0, schemas.EvidenceGateway)
	case r.Status == statusProcessing:
		row.USD = schemas.Unknown("request still processing", schemas.EvidenceGateway)
	case !a.final:
		row.USD = schemas.Unknown(fmt.Sprintf("attempt %d failed without a status; usage unknown", a.n), schemas.EvidenceGateway)
	default:
		row.Tokens, row.USD = priceUsage(r, led, model, prices)
	}
	return row
}

// priceUsage prices the usage of the attempt that returned the response.
func priceUsage(r schemas.GatewayRequest, led *schemas.LedgerEntry, model string, prices *PriceTable) (schemas.Tokens, schemas.Value) {
	tok := r.Tokens
	if tok == nil && led != nil {
		tok = led.Tokens
	}
	if tok == nil {
		return schemas.Tokens{}, schemas.Unknown("usage not recorded", schemas.EvidenceGateway)
	}
	if p, ok := prices.Lookup(model); ok {
		return *tok, schemas.Observed(p.Cost(*tok), schemas.EvidenceGateway)
	}
	reason := fmt.Sprintf("no price for model %q", model)
	if prices == nil {
		reason = "no price table"
	} else {
		reason += " in snapshot " + prices.ID
	}
	if hi, ok := prices.Max(); ok {
		return *tok, schemas.UnknownWithin(reason, schemas.EvidenceGateway, 0, hi.Cost(*tok))
	}
	return *tok, schemas.Unknown(reason, schemas.EvidenceGateway)
}

// requestTotals sums the cost rows of each request. Any unknown row makes the
// total unknown, bounded when every unknown row is; rows that do not apply
// add nothing, and a request with only such rows gets no total.
func requestTotals(rows []schemas.CostRow) map[string]schemas.Value {
	type acc struct {
		sum, upper     float64
		unknown, count int
		unbounded      bool
	}
	accs := map[string]*acc{}
	for _, row := range rows {
		if row.USD.State == schemas.ValueNotApplicable {
			continue
		}
		a := accs[row.RequestID]
		if a == nil {
			a = &acc{}
			accs[row.RequestID] = a
		}
		a.count++
		if v, ok := row.USD.Float(); ok {
			a.sum += v
			continue
		}
		a.unknown++
		if row.USD.Bounds == nil {
			a.unbounded = true
		} else {
			a.upper += row.USD.Bounds[1]
		}
	}
	out := make(map[string]schemas.Value, len(accs))
	for id, a := range accs {
		switch {
		case a.unknown == 0:
			out[id] = schemas.Observed(a.sum, schemas.EvidenceGateway)
		case a.unbounded:
			out[id] = schemas.Unknown(fmt.Sprintf("%d of %d attempts unpriced", a.unknown, a.count), schemas.EvidenceGateway)
		default:
			out[id] = schemas.UnknownWithin(fmt.Sprintf("%d of %d attempts unpriced", a.unknown, a.count), schemas.EvidenceGateway, a.sum, a.sum+a.upper)
		}
	}
	return out
}

// newCostSource returns the cost source.
func newCostSource(deps Deps) *costSource {
	return &costSource{prices: deps.Prices, logger: deps.Logger}
}

// X8 voice agents: an audio row (speech, transcription, a Realtime turn) is
// priced by seconds of audio, characters or Realtime tokens, which the
// bench's token price snapshot does not carry. Its cost is Bifrost's own
// price for the row, from the pricing datasheet the gateway pins
// (deploy/bifrost/datasheet/pricing.json), labelled with audioPriceRef; a
// row without one is unknown, never zero.
const audioPriceRef = "bifrost-row-cost"

func priceAudio(r schemas.GatewayRequest, a physAttempt) (schemas.Tokens, schemas.Value) {
	var tok schemas.Tokens
	if r.Tokens != nil {
		tok = *r.Tokens
	}
	if !a.final {
		return tok, schemas.Unknown(fmt.Sprintf("audio attempt %d failed without a status", a.n), schemas.EvidenceGateway)
	}
	if r.BifrostUSD == nil {
		return tok, schemas.Unknown("audio row without Bifrost's price (the bench's token price snapshot has no audio prices)", schemas.EvidenceGateway)
	}
	return tok, schemas.Observed(*r.BifrostUSD, schemas.EvidenceGateway)
}
