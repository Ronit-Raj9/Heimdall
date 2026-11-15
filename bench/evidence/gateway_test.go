package evidence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeGateway serves a fixed request log and records the expectation.
type fakeGateway struct {
	log    *schemas.RequestLog
	err    error
	expect schemas.FetchExpectation
}

func TestNormalizeRequestRecordsSeparatesLogicalAttemptsAndTools(t *testing.T) {
	log := &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
		{ID: "r1", Kind: "message", Status: "error", Provider: "p", ModelRequested: "m", Attempts: []schemas.Attempt{{N: 1, StatusCode: 503}, {N: 2, StatusCode: 200, FailReason: "retry"}}},
		{ID: "r2", ParentID: "r1", Kind: "message", Status: "success", Provider: "p2", ModelRequested: "m", ModelServed: "m2", FallbackIndex: 1,
			ToolCalls: []schemas.ToolCallRef{{ID: "tool-1", Name: "shell"}}},
	}}
	logical, attempts, retries, fallbacks, tools := NormalizeRequestRecords(log)
	if len(logical) != 1 || len(attempts) != 3 || len(retries) != 1 || len(fallbacks) != 1 || len(tools) != 1 {
		t.Fatalf("records = logical %d attempts %d retries %d fallbacks %d tools %d", len(logical), len(attempts), len(retries), len(fallbacks), len(tools))
	}
	if attempts[0].LogicalRequestID != logical[0].LogicalRequestID || tools[0].ExecutionSource != schemas.EvidenceSourceUnknown {
		t.Fatalf("records not linked: %+v %+v", attempts, tools)
	}
}

func (g *fakeGateway) GetName() string    { return "fake" }
func (g *fakeGateway) GetVersion() string { return "0" }
func (g *fakeGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return &schemas.GatewayManifest{}, nil
}
func (g *fakeGateway) MintKey(context.Context, *schemas.RunContext) (*schemas.RunKey, error) {
	return &schemas.RunKey{}, nil
}
func (g *fakeGateway) RevokeKey(context.Context, *schemas.RunKey) error { return nil }
func (g *fakeGateway) RevokeStale(context.Context, map[string]bool) (int, error) {
	return 0, nil
}
func (g *fakeGateway) FetchRequests(_ context.Context, _ *schemas.RunKey, expect schemas.FetchExpectation) (*schemas.RequestLog, error) {
	g.expect = expect
	return g.log, g.err
}

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// requestLogFixture is a run of three turns, listed newest first the way the
// gateway's log API returns rows:
//
//	turn 1: req1, a 503 retried once, answered with two parallel tool calls
//	turn 2: req2 fails on the primary model; fallback row req2fb answers
//	        with one tool call; both rows resend the two tool results
//	turn 3: req3 carries all three tool results and ends the run
//
// A token-counting row in between is not a turn.
func requestLogFixture() *schemas.RequestLog {
	tok := &schemas.Tokens{Fresh: 1000, CacheRead: 2000, CacheWrite: 100, Out: 500}
	rows := []schemas.GatewayRequest{
		{ID: "req3", Timestamp: t0.Add(20 * time.Second), Status: "success", Kind: "message", Provider: "anthropic",
			ModelRequested: "claude-x", ModelServed: "claude-x", LatencyMS: 900, Tokens: tok,
			ToolResultIDs: []string{"toolu_a", "toolu_b", "toolu_c"}},
		{ID: "ct1", Timestamp: t0.Add(12 * time.Second), Status: "success", Kind: "count_tokens", Provider: "anthropic", ModelRequested: "claude-x"},
		{ID: "req2fb", ParentID: "req2", Timestamp: t0.Add(11 * time.Second), Status: "success", Kind: "message", Provider: "anthropic",
			ModelRequested: "claude-x", ModelServed: "claude-y", FallbackIndex: 1, LatencyMS: 500, Tokens: tok,
			ToolResultIDs: []string{"toolu_a", "toolu_b"},
			ToolCalls:     []schemas.ToolCallRef{{ID: "toolu_c", Name: "Edit", ArgsHash: "sha256:c", Target: "app.py"}}},
		{ID: "req2", Timestamp: t0.Add(10 * time.Second), Status: "error", Kind: "message", Provider: "anthropic",
			ModelRequested: "claude-x", ErrorStatus: 503, ErrorMessage: "overloaded",
			ToolResultIDs: []string{"toolu_a", "toolu_b"}},
		{ID: "req1", Timestamp: t0, Status: "success", Kind: "message", Provider: "anthropic",
			ModelRequested: "claude-x", ModelServed: "claude-x", LatencyMS: 1500, Tokens: tok,
			Attempts: []schemas.Attempt{{N: 1, StatusCode: 503, FailReason: "overloaded"}, {N: 2, StatusCode: 200}},
			ToolCalls: []schemas.ToolCallRef{
				{ID: "toolu_a", Name: "Bash", ArgsHash: "sha256:a", Target: "pytest -q"},
				{ID: "toolu_b", Name: "Read", ArgsHash: "sha256:b", Target: "app.py"},
			}},
	}
	return &schemas.RequestLog{Requests: rows, Complete: true, RowIDs: []string{"req3", "ct1", "req2fb", "req2", "req1"}}
}

// describe renders an event as "seq type turn detail" for compact checks.
func describe(e schemas.Event) string {
	detail := e.RequestID
	switch e.Type {
	case schemas.EventToolCall, schemas.EventToolResult:
		detail = e.ToolCallID
	case schemas.EventRetry:
		detail = fmt.Sprintf("%s#%d", e.RequestID, *e.AttemptNo)
	}
	return fmt.Sprintf("%d %s t%d %s", e.Seq, e.Type, e.TurnIndex, detail)
}

func TestNormalizeRequests(t *testing.T) {
	rc := testRC(t)
	events := NormalizeRequests(rc, requestLogFixture())
	want := []string{
		"1 model_request t1 req1",
		"2 retry t1 req1#2",
		"3 model_response t1 req1",
		"4 tool_call t1 toolu_a",
		"5 tool_call t1 toolu_b",
		"6 tool_result t2 toolu_a",
		"7 tool_result t2 toolu_b",
		"8 model_request t2 req2",
		"9 model_response t2 req2",
		"10 model_request t2 req2fb",
		"11 fallback t2 req2fb",
		"12 model_response t2 req2fb",
		"13 tool_call t2 toolu_c",
		"14 tool_result t3 toolu_c",
		"15 model_request t3 req3",
		"16 model_response t3 req3",
	}
	var got []string
	for _, e := range events {
		got = append(got, describe(e))
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	byID := map[string]schemas.Event{}
	for _, e := range events {
		byID[e.EventID] = e
	}
	checks := []struct {
		name string
		ok   func() bool
	}{
		{"event ids", func() bool { return events[0].EventID == "r_1:gateway:1" && events[15].EventID == "r_1:gateway:16" }},
		{"run ids stamped", func() bool { return events[3].RunID == "r_1" && events[3].SlotID == "s_1" && events[3].PairID == "p_1" }},
		{"source and level", func() bool {
			return events[0].Source == schemas.SourceGateway && events[0].Evidence == schemas.EvidenceGateway
		}},
		{"tool call target and args", func() bool {
			return events[3].Target == "pytest -q" && events[3].ToolName == "Bash" && events[3].ArgsHash == "sha256:a"
		}},
		{"tool call parent is response", func() bool { return events[3].ParentEventID == events[2].EventID }},
		{"tool result parent is its call", func() bool {
			return events[5].ParentEventID == events[3].EventID && events[13].ParentEventID == events[12].EventID
		}},
		{"response tokens and attempt", func() bool {
			return events[2].Tokens != nil && events[2].Tokens.CacheRead == 2000 && *events[2].AttemptNo == 2
		}},
		{"response time adds latency", func() bool { return events[2].TS.Equal(t0.Add(1500 * time.Millisecond)) }},
		{"error response flagged", func() bool { return events[8].IsError && strings.Contains(events[8].Summary, "503") }},
		{"fallback points at original request", func() bool { return events[10].ParentEventID == events[7].EventID }},
		{"fallback models", func() bool { return events[10].ModelReq == "claude-x" && events[10].ModelServed == "claude-y" }},
		{"retry reason", func() bool { return strings.Contains(events[1].Summary, "503") }},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok() {
				t.Errorf("check failed; events: %+v", events)
			}
		})
	}
}

func TestNormalizeRequestsEdges(t *testing.T) {
	tests := []struct {
		name string
		log  *schemas.RequestLog
		want []string
	}{
		{name: "nil log", log: nil, want: nil},
		{name: "processing row has no response", log: &schemas.RequestLog{Requests: []schemas.GatewayRequest{
			{ID: "a", Timestamp: t0, Status: "processing", Kind: "message"},
		}}, want: []string{"1 model_request t1 a"}},
		{name: "rows without ids are separate turns", log: &schemas.RequestLog{Requests: []schemas.GatewayRequest{
			{Timestamp: t0, Status: "success", Kind: "message"},
			{Timestamp: t0.Add(time.Second), Status: "success", Kind: "message"},
		}}, want: []string{"1 model_request t1 ", "2 model_response t1 ", "3 model_request t2 ", "4 model_response t2 "}},
		{name: "parent cycle terminates", log: &schemas.RequestLog{Requests: []schemas.GatewayRequest{
			{ID: "a", ParentID: "b", Timestamp: t0, Status: "success", Kind: "message"},
			{ID: "b", ParentID: "a", Timestamp: t0.Add(time.Second), Status: "success", Kind: "message"},
		}}, want: []string{"1 model_request t1 a", "2 model_response t1 a", "3 model_request t1 b", "4 model_response t1 b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, e := range NormalizeRequests(testRC(t), tt.log) {
				got = append(got, describe(e))
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestGatewayCollect(t *testing.T) {
	t.Run("fetches with ledger, writes rows, appends events", func(t *testing.T) {
		rc := testRC(t)
		rc.Key = &schemas.RunKey{ID: "vk_1"}
		g := &fakeGateway{log: requestLogFixture()}
		ledger := []schemas.LedgerEntry{{Slot: "a", Call: 1, Kind: "message"}}
		ev := &schemas.Evidence{Ledger: ledger, Events: []schemas.Event{{EventID: "r_1:proxy_ledger:1"}}}
		if err := newGatewaySource(Deps{Gateway: g}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if len(g.expect.LedgerEntries) != 1 {
			t.Errorf("expectation carried %d ledger entries, want 1", len(g.expect.LedgerEntries))
		}
		if ev.Requests == nil || len(ev.Requests.Requests) != 5 {
			t.Fatalf("requests = %+v", ev.Requests)
		}
		if len(ev.Events) != 17 || ev.Events[0].EventID != "r_1:proxy_ledger:1" {
			t.Errorf("got %d events, want the earlier one plus 16", len(ev.Events))
		}
		b, err := os.ReadFile(filepath.Join(rc.Dir, requestsFile))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), "\n"); n != 5 {
			t.Errorf("%s has %d lines, want 5", requestsFile, n)
		}
	})
	t.Run("incomplete log adds an error event", func(t *testing.T) {
		rc := testRC(t)
		rc.Key = &schemas.RunKey{ID: "vk_1"}
		log := &schemas.RequestLog{Complete: false, Reason: "timeout after 60s"}
		ev := &schemas.Evidence{}
		if err := newGatewaySource(Deps{Gateway: &fakeGateway{log: log}}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if len(ev.Events) != 1 || ev.Events[0].Type != schemas.EventError || !strings.Contains(ev.Events[0].Summary, "timeout after 60s") {
			t.Errorf("events = %+v", ev.Events)
		}
	})
	t.Run("no gateway leaves requests nil", func(t *testing.T) {
		ev := &schemas.Evidence{}
		if err := newGatewaySource(Deps{}).Collect(context.Background(), testRC(t), ev); err != nil {
			t.Fatal(err)
		}
		if ev.Requests != nil || ev.Events != nil {
			t.Errorf("evidence changed: %+v", ev)
		}
	})
	t.Run("errors", func(t *testing.T) {
		withKey := testRC(t)
		withKey.Key = &schemas.RunKey{ID: "vk_1"}
		cases := []struct {
			name string
			rc   *schemas.RunContext
			g    *fakeGateway
			want string
		}{
			{"no key", testRC(t), &fakeGateway{log: &schemas.RequestLog{}}, "no virtual key"},
			{"fetch fails", withKey, &fakeGateway{err: errors.New("bifrost down")}, "bifrost down"},
			{"nil log", withKey, &fakeGateway{}, "no request log"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				err := newGatewaySource(Deps{Gateway: c.g}).Collect(context.Background(), c.rc, &schemas.Evidence{})
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("err = %v, want %q", err, c.want)
				}
			})
		}
	})
}
