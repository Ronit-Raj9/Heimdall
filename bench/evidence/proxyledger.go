package evidence

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// ProxyLedger is the name of the fault proxy ledger source.
const ProxyLedger = "proxy_ledger"

// ledgerFile is the run folder file holding the run's ledger entries.
const ledgerFile = "proxy.ledger.jsonl"

var _ schemas.EvidenceSource = (*proxyLedgerSource)(nil)

// proxyLedgerSource copies the fault proxy's record of every upstream request
// of the run. It runs first: the gateway fetch uses the ledger to decide when
// the gateway's log is complete.
type proxyLedgerSource struct {
	fault  schemas.FaultController
	logger schemas.Logger
}

// GetName implements schemas.Part.
func (s *proxyLedgerSource) GetName() string { return ProxyLedger }

// GetVersion implements schemas.Part.
func (s *proxyLedgerSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. Without a fault controller there
// is no proxy and nothing is recorded. A broken hash chain is recorded as an
// error event, a warning and Evidence.LedgerBreaks, never as a collect
// failure: the entries are kept as the record, but nothing derived from them
// is trusted (audit 3-B-07): the gateway log is marked incomplete, cost and
// probe receipts read unknown, and the sealed run is an evidence problem.
func (s *proxyLedgerSource) Collect(ctx context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	if s.fault == nil {
		return nil
	}
	id := runID(rc)
	entries, err := s.fault.Ledger(ctx, id)
	if err != nil {
		return fmt.Errorf("fetch proxy ledger for run %s: %w", id, err)
	}
	ev.Ledger = entries
	if err := writeJSONL(rc.Dir, ledgerFile, entries); err != nil {
		return fmt.Errorf("write proxy ledger for run %s: %w", id, err)
	}
	breaks := LedgerChainBreaks(entries)
	ev.LedgerBreaks = breaks
	for _, b := range breaks {
		pickLogger(s.logger, rc).Warn("proxy ledger chain broken", "run_id", id, "detail", b)
	}
	ev.Events = append(ev.Events, ledgerEvents(rc, entries, breaks)...)
	return writeEvents(rc, ev)
}

// LedgerChainBreaks checks the hash chain of one run's ledger entries and
// returns one description per break, empty when the chain holds. Each entry's
// hash must equal the sha256 of its canonical JSON with Hash empty (the fault
// proxy's rule), and each entry must point at the previous entry of the same
// slot. The first entry of a slot in the run is not checked against its
// predecessor, because the slot's chain began in an earlier run whose entries
// are not part of this set.
func LedgerChainBreaks(entries []schemas.LedgerEntry) []string {
	var breaks []string
	last := map[string]string{}
	seen := map[string]int{}
	for i, e := range entries {
		n := i + 1
		if e.Hash == "" {
			breaks = append(breaks, fmt.Sprintf("entry %d (slot %q, call %d): empty hash", n, e.Slot, e.Call))
		} else if want, err := ledgerEntryHash(e); err != nil {
			breaks = append(breaks, fmt.Sprintf("entry %d (slot %q, call %d): %v", n, e.Slot, e.Call, err))
		} else if want != e.Hash {
			breaks = append(breaks, fmt.Sprintf("entry %d (slot %q, call %d): hash does not match content", n, e.Slot, e.Call))
		}
		if prev, ok := last[e.Slot]; ok && e.PrevHash != prev {
			breaks = append(breaks, fmt.Sprintf("entry %d (slot %q, call %d): prev_hash does not point at the previous entry of the slot", n, e.Slot, e.Call))
		}
		if j, dup := seen[e.Hash]; dup && e.Hash != "" {
			breaks = append(breaks, fmt.Sprintf("entry %d (slot %q, call %d): hash repeats entry %d", n, e.Slot, e.Call, j))
		}
		last[e.Slot] = e.Hash
		seen[e.Hash] = n
	}
	return breaks
}

// ledgerEntryHash recomputes an entry's hash the way the fault proxy writes
// it: the sha256 of the entry's canonical JSON with Hash empty.
func ledgerEntryHash(e schemas.LedgerEntry) (string, error) {
	e.Hash = ""
	h, err := utils.HashJSON(e)
	if err != nil {
		return "", fmt.Errorf("hash ledger entry: %w", err)
	}
	return h, nil
}

// ledgerEvents returns a fault event per injected response, then one error
// event per chain break. The nonce is never copied into an event: it is the
// probe's secret and only the receipt compares it.
func ledgerEvents(rc *schemas.RunContext, entries []schemas.LedgerEntry, breaks []string) []schemas.Event {
	seq := newEventSeq(rc, schemas.SourceProxyLedger, schemas.EvidenceGateway)
	var out []schemas.Event
	for _, e := range entries {
		if e.Action != actionInject {
			continue
		}
		evt := seq.event(schemas.EventFault, e.At)
		evt.IsError = true
		evt.Target = e.Path
		evt.Summary = fmt.Sprintf("proxy injected status %d on %s call %d", e.Status, e.Kind, e.Call)
		out = append(out, evt)
	}
	for _, b := range breaks {
		evt := seq.event(schemas.EventError, timeOfLast(entries))
		evt.IsError = true
		evt.Summary = clip("ledger hash chain broken: "+b, 300)
		out = append(out, evt)
	}
	return out
}

// actionInject is the ledger action of a response the proxy made up.
const actionInject = "inject"

// timeOfLast stamps chain-break events with the last entry's time, since a
// break has no time of its own.
func timeOfLast(entries []schemas.LedgerEntry) time.Time {
	if len(entries) == 0 {
		return time.Time{}
	}
	return entries[len(entries)-1].At
}

// newProxyLedgerSource returns the proxy_ledger source.
func newProxyLedgerSource(deps Deps) *proxyLedgerSource {
	return &proxyLedgerSource{fault: deps.Fault, logger: deps.Logger}
}
