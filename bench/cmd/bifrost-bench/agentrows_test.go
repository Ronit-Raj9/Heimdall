package main

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// goodCheckInput is a check run where every row passes.
func goodCheckInput() *checkInput {
	code := 0
	start := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Second)
	return &checkInput{
		agent: &schemas.AgentConfig{ID: "mine", Argv: []string{"bench-agent"}, Model: "claude-x",
			Env: map[string]string{"BENCH_SESSION_ID": "{{run.session}}"}, Gateway: schemas.GatewayPolicy{Provider: "anthropic"}},
		image: environments.ImageInfo{ID: "sha256:abc", User: "1000"},
		run: &schemas.Run{RunID: "r_1", Gateway: schemas.RunGatewayInfo{SessionID: "r_1"},
			Agent:  schemas.RunAgentInfo{ExitCode: &code},
			Timing: schemas.RunTiming{AgentStarted: &start, AgentExited: &end},
			Claim:  &schemas.Claim{Text: "step\nAll tests pass.", Made: true, Matched: "all tests pass", Source: schemas.EvidenceOutcome}},
		ev: schemas.Evidence{
			Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{Kind: "message", Status: "success"}, {Kind: "models", Status: "success"}}},
			Ledger:   []schemas.LedgerEntry{{Upstream: schemas.LedgerUpstreamScripted, Action: "pass"}},
			Diff:     &schemas.Diff{Files: []schemas.DiffFile{{Path: "a.py", Status: "modified"}}, Rejected: []schemas.DiffReject{{Path: "__pycache__/x", Reason: environments.RejectCache}}},
		},
		gates: []schemas.GateResult{{Gate: "egress"}},
		net: []environments.NetWatchRecord{
			{T: environments.NetWatchDNS, Name: "bifrost"},
			{T: environments.NetWatchDNSAnswer, Name: "bifrost", IPs: []string{"172.20.0.3"}},
			{T: environments.NetWatchSYN, Dst: "172.20.0.3", Port: 8080},
			{T: environments.NetWatchHTTP, Dst: "172.20.0.3", Port: 8080, Method: "POST", Path: "/openai/v1/chat/completions", Host: "bifrost:8080", Session: "r_1"},
			{T: environments.NetWatchSYN, Dst: "127.0.0.1", Port: 8765},
			{T: environments.NetWatchHTTP, Dst: "127.0.0.1", Port: 8765, Method: "POST", Path: "/run", Host: "127.0.0.1:8765"},
		},
		gatewayHost: "bifrost", gatewayPort: 8080, wallS: 900,
	}
}

func TestCheckRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *checkInput)
		want   map[string]string // row id -> status; every other row PASS
		detail string            // substring of the first non-PASS row's detail
	}{
		{name: "all pass", mutate: func(*checkInput) {}},
		{name: "agent never ran", mutate: func(in *checkInput) {
			in.run.Agent.ExitCode = nil
			in.run.Failures = []schemas.PartFailure{{Stage: schemas.StageRun, Part: "environment", Error: "create agent container: no such image"}}
		}, want: map[string]string{rowUser: checkFail, rowExit: checkFail}, detail: "no such image"},
		{name: "command not found", mutate: func(in *checkInput) { c := 127; in.run.Agent.ExitCode = &c },
			want: map[string]string{rowUser: checkFail, rowExit: checkFail}, detail: `"bench-agent" was not found`},
		{name: "root image", mutate: func(in *checkInput) { in.image.User = "" }, want: map[string]string{rowUser: checkWarn}, detail: `"root"`},
		{name: "real upstream", mutate: func(in *checkInput) {
			in.ev.Ledger = append(in.ev.Ledger, schemas.LedgerEntry{Upstream: schemas.LedgerUpstreamReal, Action: "pass"})
		}, want: map[string]string{rowModelCall: checkFail}, detail: "forwarded to a real provider"},
		{name: "requests seen, none logged", mutate: func(in *checkInput) { in.ev.Requests = &schemas.RequestLog{Complete: true} },
			want: map[string]string{rowModelCall: checkFail}, detail: "Bifrost logged none"},
		{name: "no model call at all", mutate: func(in *checkInput) {
			in.ev.Requests, in.net = nil, nil
		}, want: map[string]string{rowModelCall: checkFail, rowSession: checkWarn}, detail: "no model call reached Bifrost"},
		{name: "every call refused", mutate: func(in *checkInput) {
			in.ev.Requests.Requests = []schemas.GatewayRequest{{Kind: "message", Status: "error", ErrorMessage: "model not allowed"}}
		}, want: map[string]string{rowModelCall: checkFail}, detail: "model not allowed"},
		{name: "some calls refused", mutate: func(in *checkInput) {
			in.ev.Requests.Requests = append(in.ev.Requests.Requests, schemas.GatewayRequest{Kind: "message", Status: "error", ErrorStatus: 403})
		}, want: map[string]string{rowModelCall: checkWarn}, detail: "status 403"},
		{name: "no session header", mutate: func(in *checkInput) { in.net[3].Session = "" },
			want: map[string]string{rowSession: checkWarn}, detail: "carried x-bf-session-id"},
		{name: "own session id", mutate: func(in *checkInput) { in.net[3].Session = "mine-1" },
			want: map[string]string{rowSession: checkWarn}, detail: `"mine-1"`},
		{name: "observer did not run", mutate: func(in *checkInput) { in.net, in.netErr = nil, "the network observer did not start: no python3" },
			want: map[string]string{rowSession: checkWarn, rowEgress: checkWarn}, detail: "no python3"},
		{name: "timed out", mutate: func(in *checkInput) { in.run.Agent.TimedOut = true }, want: map[string]string{rowExit: checkFail}, detail: "wall budget (900s)"},
		{name: "non-zero exit", mutate: func(in *checkInput) {
			c := 2
			in.run.Agent.ExitCode, in.stderr = &c, "Traceback\nValueError: boom\n"
		}, want: map[string]string{rowExit: checkFail}, detail: "code 2: ValueError: boom"},
		{name: "export failed", mutate: func(in *checkInput) {
			in.ev.Diff = nil
			in.run.Failures = []schemas.PartFailure{{Stage: schemas.StageExport, Part: "environment", Error: "workspace has no .git folder"}}
		}, want: map[string]string{rowDiff: checkFail}, detail: "no .git folder"},
		{name: "paths dropped", mutate: func(in *checkInput) {
			in.ev.Diff.Rejected = append(in.ev.Diff.Rejected, schemas.DiffReject{Path: "link", Reason: "symlink"})
		}, want: map[string]string{rowDiff: checkWarn}, detail: "link (symlink)"},
		{name: "egress gate fired", mutate: func(in *checkInput) { in.gates[0].Fired, in.gates[0].Detail = true, "egress probe is \"open\"" },
			want: map[string]string{rowEgress: checkFail}, detail: "isolation not proven"},
		{name: "egress attempts", mutate: func(in *checkInput) {
			in.net = append(in.net,
				environments.NetWatchRecord{T: environments.NetWatchDNS, Name: "telemetry.example.com"},
				environments.NetWatchRecord{T: environments.NetWatchDNS, Name: "telemetry.example.com.corp.lan"},
				environments.NetWatchRecord{T: environments.NetWatchSYN, Dst: "172.20.0.9", Port: 443})
		}, want: map[string]string{rowEgress: checkFail}, detail: "2 hosts other than the gateway: 172.20.0.9:443, telemetry.example.com"},
		{name: "parser read nothing", mutate: func(in *checkInput) { in.agent.Parser = "codex" }, want: map[string]string{rowEvidence: checkWarn}, detail: "parser codex read no events"},
		{name: "incomplete gateway rows", mutate: func(in *checkInput) {
			in.ev.Requests.Complete, in.ev.Requests.Reason = false, "1 of 2 ledger entries have no row"
		}, want: map[string]string{rowEvidence: checkWarn}, detail: "1 of 2 ledger entries"},
		{name: "no final message", mutate: func(in *checkInput) { in.run.Claim = nil }, want: map[string]string{rowClaim: checkWarn}, detail: "no final message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := goodCheckInput()
			tt.mutate(in)
			rows := checkRows(in)
			if len(rows) != len(rowOrder)-1 {
				t.Fatalf("%d rows", len(rows))
			}
			detailSeen := tt.detail == ""
			for i, r := range rows {
				if r.ID != rowOrder[i+1] || r.Title == "" {
					t.Errorf("row %d = %s %q, want %s", i, r.ID, r.Title, rowOrder[i+1])
				}
				want := tt.want[r.ID]
				if want == "" {
					want = checkPass
				}
				if r.Status != want {
					t.Errorf("row %s = %s (%s), want %s", r.ID, r.Status, r.Detail, want)
				}
				if r.Status != checkPass && r.Fix == "" {
					t.Errorf("row %s is %s with no fix hint", r.ID, r.Status)
				}
				if tt.detail != "" && strings.Contains(r.Detail, tt.detail) {
					detailSeen = true
				}
			}
			if !detailSeen {
				t.Errorf("no row detail contains %q: %+v", tt.detail, rows)
			}
		})
	}
}

func TestCheckRowsPassDetails(t *testing.T) {
	in := goodCheckInput()
	in.agent.Parser = "codex"
	in.ev.Events = []schemas.Event{{Evidence: schemas.EvidenceNative}, {Evidence: schemas.EvidenceGateway}}
	got := map[string]string{}
	for _, r := range checkRows(in) {
		got[r.ID] = r.Detail
	}
	for id, want := range map[string]string{
		rowSession:  "x-bf-session-id: r_1 on every gateway request seen",
		rowExit:     "exited 0 after 3s (budget 900s)",
		rowDiff:     "exported, 1 files changed",
		rowEgress:   "only the gateway bifrost",
		rowEvidence: "native: parser codex read 1 events",
		rowClaim:    `"All tests pass.", success claimed (all tests pass)`,
	} {
		if !strings.Contains(got[id], want) {
			t.Errorf("%s detail %q, want %q", id, got[id], want)
		}
	}
}

func TestSummarizeNet(t *testing.T) {
	recs := []environments.NetWatchRecord{
		{T: environments.NetWatchDNS, Name: "bifrost"},
		{T: environments.NetWatchDNS, Name: "bifrost.corp.lan"},
		{T: environments.NetWatchDNS, Name: "localhost"},
		{T: environments.NetWatchHTTP, Dst: "172.20.0.3", Port: 8080, Host: "bifrost:8080", Method: "POST", Path: "/anthropic/v1/messages"},
		{T: environments.NetWatchSYN, Dst: "172.20.0.3", Port: 8080},
		{T: environments.NetWatchSYN, Dst: "172.20.0.3", Port: 22},
		{T: environments.NetWatchUDP, Dst: "8.8.8.8", Port: 53},
		{T: environments.NetWatchUDP, Dst: "127.0.0.1", Port: 9},
		{T: environments.NetWatchHTTP, Dst: "10.0.0.7", Port: 80, Host: "pypi.org", Path: "/simple"},
		{T: environments.NetWatchHTTP, Dst: "127.0.0.1", Port: 8765, Path: "/health"},
	}
	s := summarizeNet(recs, "bifrost", 8080)
	if len(s.gatewayReqs) != 1 || s.gatewayReqs[0].Path != "/anthropic/v1/messages" {
		t.Errorf("gateway requests %+v", s.gatewayReqs)
	}
	want := "172.20.0.3:22,http://pypi.org/simple,udp 8.8.8.8:53"
	if got := strings.Join(s.attempts, ","); got != want {
		t.Errorf("attempts %q, want %q", got, want)
	}
	if got := summarizeNet(nil, "bifrost", 8080); len(got.attempts) != 0 || len(got.gatewayReqs) != 0 {
		t.Errorf("empty summary %+v", got)
	}
}

func TestRowHelpers(t *testing.T) {
	if got := listed([]string{"a", "b", "c", "d", "e", "f", "g"}); got != "a, b, c, d, e and 2 more" {
		t.Errorf("listed = %q", got)
	}
	if got := hostOnly("Bifrost:8080"); got != "bifrost" {
		t.Errorf("hostOnly = %q", got)
	}
	if got := lastLineOf("one\ntwo\n\n"); got != "two" {
		t.Errorf("lastLineOf = %q", got)
	}
	if got := plural(1, "host") + "," + plural(2, "host"); got != "1 host,2 hosts" {
		t.Errorf("plural = %q", got)
	}
	cfg := &schemas.AgentConfig{Files: map[string]string{"/x": "id={{run.session}}"}}
	if !configUses(cfg, sessionVar) || configUses(&schemas.AgentConfig{}, sessionVar) {
		t.Error("configUses")
	}
}
