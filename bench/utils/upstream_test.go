package utils

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Ledger line builders for the tally tests. dnsErr is the transport error the
// fault proxy logged when Docker DNS broke on a live experiment.
const dnsErr = "Post: dial tcp: lookup api.openai.com on 127.0.0.11:53: read udp 127.0.0.1:41000->127.0.0.11:53: i/o timeout"

func answered(call int) schemas.LedgerEntry {
	return schemas.LedgerEntry{RunID: "r_1", Call: call, Kind: "message", Action: "pass", Upstream: "real", Status: 200,
		ResponseID: "resp_" + strconv.Itoa(call), Tokens: &schemas.Tokens{Fresh: 10, Out: 5}}
}

func unreachable(call int) schemas.LedgerEntry {
	return schemas.LedgerEntry{RunID: "r_1", Call: call, Kind: "message", Action: "pass", Upstream: "real", Status: 502, UpstreamError: dnsErr}
}

// legacyUnreachable is the same failure written by a proxy older than
// upstream_error: a pass line with the proxy's 502 and nothing else.
func legacyUnreachable(call int) schemas.LedgerEntry {
	return schemas.LedgerEntry{RunID: "r_1", Call: call, Kind: "message", Action: "pass", Upstream: "real", Status: 502, UpstreamMS: 10000}
}

func injected(call int) schemas.LedgerEntry {
	return schemas.LedgerEntry{RunID: "r_1", Call: call, Kind: "message", Action: "inject", Upstream: "real", Status: 503, Nonce: "n"}
}

func gatewayRows(msgs ...string) *schemas.RequestLog {
	rl := &schemas.RequestLog{Complete: true}
	for _, m := range msgs {
		row := schemas.GatewayRequest{Kind: "message", Status: "success"}
		if m != "" {
			row.Status, row.ErrorStatus, row.ErrorMessage = "error", 502, m
		}
		rl.Requests = append(rl.Requests, row)
	}
	return rl
}

func TestTallyUpstream(t *testing.T) {
	providerBadGateway := schemas.LedgerEntry{RunID: "r_1", Call: 2, Kind: "message", Action: "pass", Upstream: "real", Status: 502}
	cases := []struct {
		name          string
		ledger        []schemas.LedgerEntry
		reqs          *schemas.RequestLog
		want          UpstreamTally
		never, ending bool
	}{
		{name: "no ledger"},
		{name: "every call answered", ledger: []schemas.LedgerEntry{answered(1), answered(2)},
			want: UpstreamTally{Answered: 2}},
		{name: "provider unreachable from the first call",
			ledger: []schemas.LedgerEntry{unreachable(1), unreachable(2), unreachable(3)},
			want:   UpstreamTally{Errors: 3, Calls: []int{1, 2, 3}, LastFailed: true, FirstError: dnsErr},
			never:  true, ending: true},
		{name: "provider lost after answers",
			ledger: []schemas.LedgerEntry{answered(1), answered(2), unreachable(3), unreachable(4)},
			want:   UpstreamTally{Errors: 2, Calls: []int{3, 4}, Answered: 2, LastFailed: true, FirstError: dnsErr},
			ending: true},
		{name: "agent recovered after a blip",
			ledger: []schemas.LedgerEntry{answered(1), unreachable(2), answered(3)},
			want:   UpstreamTally{Errors: 1, Calls: []int{2}, Answered: 2, FirstError: dnsErr}},
		{name: "planned faults are not upstream errors",
			ledger: []schemas.LedgerEntry{injected(1), injected(2), injected(3)}},
		{name: "run ended on a planned fault after an upstream error",
			ledger: []schemas.LedgerEntry{answered(1), unreachable(2), injected(3)},
			want:   UpstreamTally{Errors: 1, Calls: []int{2}, Answered: 1, FirstError: dnsErr}},
		{name: "never answered behind a persistent fault",
			ledger: []schemas.LedgerEntry{unreachable(1), injected(2), injected(3)},
			want:   UpstreamTally{Errors: 1, Calls: []int{1}, FirstError: dnsErr},
			never:  true},
		{name: "uncounted kinds and rejected lines are skipped",
			ledger: []schemas.LedgerEntry{
				{RunID: "r_1", Kind: "count_tokens", Action: "pass", Status: 502, UpstreamError: dnsErr},
				{Kind: "message", Action: "reject", Status: 401},
				answered(1),
			},
			want: UpstreamTally{Answered: 1}},
		{name: "older proxy, gateway shows the proxy's message",
			ledger: []schemas.LedgerEntry{legacyUnreachable(1), legacyUnreachable(2)},
			reqs:   gatewayRows(ProxyUnreachable, ProxyUnreachable),
			want:   UpstreamTally{Errors: 2, Calls: []int{1, 2}, LastFailed: true, Inferred: true},
			never:  true, ending: true},
		{name: "older proxy, provider's own 502 is not an upstream error",
			ledger: []schemas.LedgerEntry{answered(1), providerBadGateway},
			reqs:   gatewayRows("", "upstream server error"),
			want:   UpstreamTally{Answered: 1}},
		{name: "marked ledger never infers from the gateway",
			ledger: []schemas.LedgerEntry{answered(1), providerBadGateway, unreachable(3)},
			reqs:   gatewayRows("", ProxyUnreachable, ProxyUnreachable),
			want:   UpstreamTally{Errors: 1, Calls: []int{3}, Answered: 1, LastFailed: true, FirstError: dnsErr},
			ending: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TallyUpstream(c.ledger, c.reqs)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("tally\n got %+v\nwant %+v", got, c.want)
			}
			if got.NeverAnswered() != c.never || got.EndedOnError() != c.ending {
				t.Fatalf("never answered %v ended on error %v, want %v %v", got.NeverAnswered(), got.EndedOnError(), c.never, c.ending)
			}
		})
	}
}
