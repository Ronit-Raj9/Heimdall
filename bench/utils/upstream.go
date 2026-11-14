package utils

import (
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ProxyUnreachable is the error message the fault proxy answers with, as a
// 502, when it gets no response from a provider's API. Bifrost passes it on
// as the request's error message, which is how a run recorded by a proxy
// older than LedgerEntry.UpstreamError is still recognised.
const ProxyUnreachable = "fault proxy could not reach the upstream"

// Ledger values the tally reads. They mirror the fault proxy's, which this
// package cannot import.
const (
	ledgerKindMessage = "message"
	ledgerActionPass  = "pass"
)

// UpstreamTally counts a run's model calls that the fault proxy passed on
// with no fault planned, split by whether the provider answered. Injected
// calls are a probe's planned faults and never count here.
type UpstreamTally struct {
	Errors     int    // passed calls that got no response from the provider
	Calls      []int  // ledger call numbers of those calls, in ledger order
	Answered   int    // passed calls the provider answered with a 2xx status
	LastFailed bool   // the run's last model call is one of Errors
	FirstError string // the first recorded transport error; empty when inferred
	Inferred   bool   // Errors read from the gateway's error text: an older proxy wrote the ledger
}

// TallyUpstream counts the upstream errors of one run from its fault proxy
// ledger. A pass line with UpstreamError set is an upstream error. When no
// line of the run carries the field (a proxy older than it, or no error at
// all) and the gateway log shows the proxy's ProxyUnreachable message on a
// model request, every pass line with the proxy's 502 and no response id is
// read as an upstream error instead. A run with no ledger has no tally.
func TallyUpstream(ledger []schemas.LedgerEntry, reqs *schemas.RequestLog) UpstreamTally {
	var t UpstreamTally
	marked := false
	for _, e := range ledger {
		if e.UpstreamError != "" {
			marked = true
			break
		}
	}
	inferred := !marked && gatewaySawUnreachable(reqs)
	for _, e := range ledger {
		if e.Kind != ledgerKindMessage || e.RunID == "" {
			continue
		}
		failed := false
		if e.Action == ledgerActionPass {
			switch {
			case e.UpstreamError != "":
				failed = true
				if t.FirstError == "" {
					t.FirstError = e.UpstreamError
				}
			case inferred && e.Status == http.StatusBadGateway && e.ResponseID == "" && e.Tokens == nil:
				failed = true
			case e.Status >= 200 && e.Status < 300:
				t.Answered++
			}
		}
		if failed {
			t.Errors++
			t.Calls = append(t.Calls, e.Call)
		}
		t.LastFailed = failed
	}
	t.Inferred = inferred && t.Errors > 0
	return t
}

// NeverAnswered reports a run whose agent never got a model response because
// the provider could not be reached: at least one upstream error and no
// answered call. Nothing the agent did can depend on the model.
func (t UpstreamTally) NeverAnswered() bool {
	return t.Errors > 0 && t.Answered == 0
}

// EndedOnError reports a run whose last model call got no response from the
// provider, so the agent stopped because the provider was unreachable.
func (t UpstreamTally) EndedOnError() bool {
	return t.Errors > 0 && t.LastFailed
}

// gatewaySawUnreachable reports whether a model request in the gateway log
// failed with the fault proxy's ProxyUnreachable message.
func gatewaySawUnreachable(reqs *schemas.RequestLog) bool {
	if reqs == nil {
		return false
	}
	for _, r := range reqs.Requests {
		if r.Kind == ledgerKindMessage && strings.Contains(r.ErrorMessage, ProxyUnreachable) {
			return true
		}
	}
	return false
}
