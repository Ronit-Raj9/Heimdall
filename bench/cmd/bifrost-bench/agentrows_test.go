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
		{name: "helper without python3", mutate: func(in *checkInput) {
			lacks := "helper image acme/node:22 has no python3; set images.helper to the bench helper image"
			in.net, in.netErr = nil, "observer exited before it was ready: "+lacks
			in.gates[0].Fired, in.gates[0].Detail = true, `egress probe is "not_run", not "blocked": egress probe not run: `+lacks
		}, want: map[string]string{rowSession: checkWarn, rowEgress: checkFail}, detail: "acme/node:22 has no python3"},
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
		{name: "protocol trace with malformed records", mutate: func(in *checkInput) {
			in.ev.AgentTrace = &schemas.ParseDiagnostics{Source: schemas.TraceSourceProtocol, Quality: schemas.TraceQualityPartial, Emitted: 2, Malformed: 1}
		}, want: map[string]string{rowEvidence: checkWarn}, detail: "protocol trace: partial"},
		{name: "no captured trace", mutate: func(in *checkInput) {
			in.ev.AgentTrace = &schemas.ParseDiagnostics{Source: schemas.TraceSourceNone, Quality: schemas.TraceQualityUnavailable}
		}, want: map[string]string{rowEvidence: checkWarn}, detail: "none trace: unavailable"},
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
		rowSession:   "x-bf-session-id: r_1 on every gateway request seen",
		rowExit:      "exited 0 after 3s (budget 900s)",
		rowModelCall: "1 model call through Bifrost with the run key",
		rowDiff:      "exported, 1 file changed",
		rowEgress:    "only the gateway bifrost",
		rowEvidence:  "native: parser codex read 1 event,",
		rowClaim:     `"All tests pass.", success claimed (all tests pass)`,
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

func TestHelperFix(t *testing.T) {
	in := goodCheckInput()
	lacks := "helper image acme/node:22 has no python3; set images.helper to the bench helper image"
	in.net, in.netErr = nil, "observer exited before it was ready: "+lacks
	in.gates[0].Fired, in.gates[0].Detail = true, `egress probe is "not_run", not "blocked": egress probe not run: `+lacks
	for _, r := range checkRows(in) {
		if (r.ID == rowSession || r.ID == rowEgress) && !strings.HasPrefix(r.Fix, "set images.helper in deploy/bench.yaml") {
			t.Errorf("row %s fix = %q, want the images.helper hint", r.ID, r.Fix)
		}
	}
	if _, ok := helperFix("egress probe is \"open\""); ok {
		t.Error("an open probe is not the helper's fault")
	}
}

// scenarioCheckInput is a passing check run of an MCP task with two sessions
// and an answer output: every row, the scenario rows included, passes.
func scenarioCheckInput() *checkInput {
	in := goodCheckInput()
	start := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	s := &schemas.Suite{ID: "desk", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}
	task := &schemas.Task{ID: "t1",
		Services: []schemas.ServiceSpec{{ID: "desk", Role: schemas.ServiceRoleMCP, Port: 8000}},
		Sessions: []schemas.SessionSpec{{ID: "learn"}, {ID: "recall"}}}
	in.agent.Memory = &schemas.AgentMemory{Paths: []string{"/home/agent/.claude"}}
	in.plan = schemas.RunPlan{
		Env:   map[string]string{"BENCH_MCP_SERVERS": `{"desk":{"url":"http://toolproxy:9200/s/desk/mcp"}}`},
		Files: map[string]string{"/home/agent/.claude/.claude.json": `{"mcpServers":{"desk":{"type":"http","url":"http://toolproxy:9200/s/desk/mcp"}}}`},
	}
	in.net = append(in.net,
		environments.NetWatchRecord{T: environments.NetWatchDNS, Name: "toolproxy"},
		environments.NetWatchRecord{T: environments.NetWatchDNSAnswer, Name: "toolproxy", IPs: []string{"172.21.0.5"}},
		environments.NetWatchRecord{T: environments.NetWatchSYN, Dst: "172.21.0.5", Port: 9200},
		environments.NetWatchRecord{T: environments.NetWatchHTTP, Dst: "172.21.0.5", Port: 9200, Method: "POST", Path: "/s/desk/mcp", Host: "toolproxy:9200"})
	in.scenario = &checkScenario{suite: s, task: task,
		services: []schemas.ServiceResult{{ID: "desk", Role: "mcp", Ready: true, Proxied: true}},
		tools:    []schemas.ToolCall{{Seq: 1, Server: "desk", Tool: "tools/list", Protocol: "mcp"}, {Seq: 2, Server: "desk", Tool: "get_ticket", Protocol: "mcp"}},
		sessions: []schemas.SessionResult{
			{ID: "learn", Index: 1, Started: start, Ended: start.Add(time.Second)},
			{ID: "recall", Index: 2, Started: start.Add(2 * time.Second), Ended: start.Add(3 * time.Second)}},
		answer: &schemas.Answer{Text: "2026-04-09\n", Source: "file"},
	}
	return in
}

func TestScenarioCheckRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *checkInput)
		want   map[string]string // row id -> status; every other row PASS
		detail string            // substring of the first non-PASS row's detail
	}{
		{name: "all pass", mutate: func(*checkInput) {}},
		{name: "service not ready", mutate: func(in *checkInput) {
			in.scenario.services[0].Ready, in.scenario.services[0].Error = false, "ready check timed out after 30s"
		}, want: map[string]string{rowServices: checkFail}, detail: "ready check timed out"},
		{name: "no service record", mutate: func(in *checkInput) { in.scenario.services = nil },
			want: map[string]string{rowServices: checkFail}, detail: "unknown whether the services started"},
		{name: "no mcp env", mutate: func(in *checkInput) { in.plan.Env = nil },
			want: map[string]string{rowMCP: checkFail}, detail: "not told about the MCP server"},
		{name: "env but no config file", mutate: func(in *checkInput) { in.plan.Files = nil },
			want: map[string]string{rowMCP: checkWarn}, detail: "no config file"},
		{name: "env format", mutate: func(in *checkInput) {
			in.plan.Files, in.agent.MCP = nil, &schemas.AgentMCP{Format: "env"}
		}},
		{name: "only tools/list", mutate: func(in *checkInput) { in.scenario.tools = in.scenario.tools[:1] }},
		{name: "proxy recorded nothing", mutate: func(in *checkInput) { in.scenario.tools = []schemas.ToolCall{} },
			want: map[string]string{rowToolCall: checkFail}, detail: "never contacted the MCP server"},
		{name: "no ledger", mutate: func(in *checkInput) {
			in.scenario.tools, in.scenario.toolsErr = nil, "tools.jsonl missing from the run folder"
		}, want: map[string]string{rowToolCall: checkFail}, detail: "unknown, not zero"},
		{name: "session out of order", mutate: func(in *checkInput) {
			s := in.scenario.sessions
			s[0], s[1] = s[1], s[0]
		}, want: map[string]string{rowSessions: checkFail}, detail: `want "learn"`},
		{name: "session crashed", mutate: func(in *checkInput) { in.scenario.sessions[1].ExitCode = 3 },
			want: map[string]string{rowSessions: checkFail}, detail: "recall exited with code 3"},
		{name: "one session missing", mutate: func(in *checkInput) { in.scenario.sessions = in.scenario.sessions[:1] },
			want: map[string]string{rowSessions: checkFail}, detail: "1 of 2 sessions ran"},
		{name: "no memory paths", mutate: func(in *checkInput) { in.agent.Memory = nil },
			want: map[string]string{rowSessions: checkWarn}, detail: "declares no memory.paths"},
		{name: "answer from final message", mutate: func(in *checkInput) {
			in.scenario.answer = &schemas.Answer{Text: "It is 2026-04-09.", Source: "final_message"}
		}, want: map[string]string{rowAnswer: checkWarn}, detail: "final message was used"},
		{name: "no answer", mutate: func(in *checkInput) { in.scenario.answer = nil },
			want: map[string]string{rowAnswer: checkFail}, detail: "neither answer.txt"},
		{name: "unexpected host", mutate: func(in *checkInput) {
			in.net = append(in.net, environments.NetWatchRecord{T: environments.NetWatchDNS, Name: "pypi.org"})
		}, want: map[string]string{rowEgress: checkFail}, detail: "pypi.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := scenarioCheckInput()
			tt.mutate(in)
			rows := checkRows(in)
			want := withScenarioRows(rowOrder[1:], in.scenario.suite, in.scenario.task)
			if len(rows) != len(want) {
				t.Fatalf("got %d rows, want %d", len(rows), len(want))
			}
			first := ""
			for i, r := range rows {
				if r.ID != want[i] || r.Title == "" {
					t.Errorf("row %d = %s %q, want %s", i, r.ID, r.Title, want[i])
				}
				status := checkPass
				if s, ok := tt.want[r.ID]; ok {
					status = s
				}
				if r.Status != status {
					t.Errorf("%s = %s (%s), want %s", r.ID, r.Status, r.Detail, status)
				}
				if r.Status != checkPass && first == "" {
					first = r.Detail
					if r.Fix == "" {
						t.Errorf("%s: %s without a fix", r.ID, r.Status)
					}
				}
			}
			if tt.detail != "" && !strings.Contains(first, tt.detail) {
				t.Errorf("detail %q does not contain %q", first, tt.detail)
			}
		})
	}
}

func TestScenarioRowIDs(t *testing.T) {
	repo := &schemas.Task{ID: "t"}
	if got := withScenarioRows(rowOrder[1:], &schemas.Suite{}, repo); len(got) != len(rowOrder)-1 {
		t.Errorf("repository task got scenario rows: %v", got)
	}
	tool := &schemas.Task{Services: []schemas.ServiceSpec{{ID: "inv", Role: schemas.ServiceRoleTool}}}
	got := scenarioRowIDs(&schemas.Suite{Output: &schemas.OutputSpec{Kind: schemas.OutputState}}, tool)
	if strings.Join(got, ",") != rowServices+","+rowToolCall {
		t.Errorf("tool task rows = %v", got)
	}
	hidden := &schemas.Task{Services: []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleMCP, Hidden: true}}}
	if got := scenarioRowIDs(&schemas.Suite{}, hidden); strings.Join(got, ",") != rowServices {
		t.Errorf("hidden service rows = %v", got)
	}
}
