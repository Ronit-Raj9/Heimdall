package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// maxListed bounds how many hosts or paths a row detail lists.
const maxListed = 5

// sessionVar is the template variable that carries the session id.
const sessionVar = "{{run.session}}"

// checkRows evaluates every row after the image row from one sealed check
// run. It is pure: everything it reads is in in.
func checkRows(in *checkInput) []checkRow {
	net := summarizeNet(in.net, in.gatewayHost, in.gatewayPort, runHosts(in.scenario)...)
	rows := []checkRow{
		userRow(in),
		modelCallRow(in, net),
		sessionRow(in, net),
		exitRow(in),
		diffRow(in),
	}
	rows = append(rows, scenarioRows(in)...)
	rows = append(rows, remoteRows(in)...) // Remote coverage: channel summary of a remote agent (agentremote.go)
	// Agent events area: agentEventsRow (agentevents.go) comes last.
	return append(rows, egressRow(in, net), processObserverRow(in), workspaceObserverRow(in), evidenceRow(in), claimRow(in), agentEventsRow(in))
}

// withScenarioRows inserts the scenario rows the task has after the diff
// row of ids, in the order checkRows reports them.
func withScenarioRows(ids []string, s *schemas.Suite, t *schemas.Task) []string {
	extra := scenarioRowIDs(s, t)
	if len(extra) == 0 {
		return ids
	}
	var out []string
	for _, id := range ids {
		out = append(out, id)
		if id == rowDiff {
			out = append(out, extra...)
		}
	}
	return out
}

// scenarioRowIDs lists the scenario rows a task has: services when it has
// any, the MCP config when one is an mcp service the agent sees, the tool
// call when one is proxied, sessions when there are several, the answer for
// an answer task.
func scenarioRowIDs(s *schemas.Suite, t *schemas.Task) []string {
	var out []string
	services := utils.TaskServices(s, t)
	mcp, proxied := false, false
	for _, sv := range services {
		mcp = mcp || (sv.Role == schemas.ServiceRoleMCP && !sv.Hidden)
		proxied = proxied || (utils.ServiceProxied(sv) && !sv.Hidden)
	}
	if len(services) > 0 {
		out = append(out, rowServices)
	}
	if mcp {
		out = append(out, rowMCP)
	}
	if proxied {
		out = append(out, rowToolCall)
	}
	if len(t.Sessions) > 1 {
		out = append(out, rowSessions)
	}
	if utils.OutputKind(s, t) == schemas.OutputAnswer {
		out = append(out, rowAnswer)
	}
	return out
}

// scenarioRows evaluates the scenario rows of the check run's task.
func scenarioRows(in *checkInput) []checkRow {
	sc := in.scenario
	if sc == nil {
		return nil
	}
	var rows []checkRow
	for _, id := range scenarioRowIDs(sc.suite, sc.task) {
		switch id {
		case rowServices:
			rows = append(rows, servicesRow(sc))
		case rowMCP:
			rows = append(rows, mcpConfigRow(in))
		case rowToolCall:
			rows = append(rows, toolCallRow(sc))
		case rowSessions:
			rows = append(rows, sessionsRow(in))
		case rowAnswer:
			rows = append(rows, answerRow(sc))
		}
	}
	return rows
}

// runHosts are the host names the agent may reach besides the gateway: the
// tool proxy and every service on the run's network that is not hidden.
func runHosts(sc *checkScenario) []string {
	if sc == nil {
		return nil
	}
	var hosts []string
	proxied := false
	for _, sv := range utils.TaskServices(sc.suite, sc.task) {
		if sv.Hidden {
			continue
		}
		hosts = append(hosts, strings.ToLower(sv.ID))
		proxied = proxied || utils.ServiceProxied(sv)
	}
	if proxied {
		hosts = append(hosts, toolProxyHost)
	}
	return hosts
}

// toolProxyHost is the per-run tool proxy's host name on the run network.
const toolProxyHost = "toolproxy"

// netSummary is the observer's records sorted into gateway traffic and
// everything else.
type netSummary struct {
	gatewayReqs []environments.NetWatchRecord // HTTP requests to the gateway
	attempts    []string                      // egress attempts: names, addresses
}

// summarizeNet splits the observer's records. The gateway is its host name
// and the addresses that name resolved to (or that HTTP requests naming it
// were sent to). Loopback traffic (an agent talking to its own server) is
// local, and so is traffic to allowed, the run's own tool proxy and
// services. Any other DNS name, connection attempt, datagram or HTTP request
// is an egress attempt.
func summarizeNet(recs []environments.NetWatchRecord, host string, port int, allowed ...string) netSummary {
	gwIPs := map[string]bool{}
	runIPs := map[string]bool{}
	isRunHost := func(name string) bool {
		for _, a := range allowed {
			if isGatewayName(name, a) {
				return true
			}
		}
		return false
	}
	for _, r := range recs {
		switch {
		case r.T == environments.NetWatchDNSAnswer && isGatewayName(r.Name, host):
			for _, ip := range r.IPs {
				gwIPs[ip] = true
			}
		case r.T == environments.NetWatchHTTP && isGatewayName(hostOnly(r.Host), host):
			gwIPs[r.Dst] = true
		case r.T == environments.NetWatchDNSAnswer && isRunHost(r.Name):
			for _, ip := range r.IPs {
				runIPs[ip] = true
			}
		case r.T == environments.NetWatchHTTP && isRunHost(hostOnly(r.Host)):
			runIPs[r.Dst] = true
		}
	}
	var s netSummary
	seen := map[string]bool{}
	attempt := func(a string) {
		if !seen[a] {
			seen[a] = true
			s.attempts = append(s.attempts, a)
		}
	}
	for _, r := range recs {
		switch r.T {
		case environments.NetWatchDNS:
			if !isGatewayName(r.Name, host) && !isRunHost(r.Name) && r.Name != "localhost" && !strings.HasSuffix(r.Name, ".localhost") {
				attempt(r.Name)
			}
		case environments.NetWatchSYN:
			if !isLoopback(r.Dst) && !(gwIPs[r.Dst] && r.Port == port) && !runIPs[r.Dst] {
				attempt(fmt.Sprintf("%s:%d", r.Dst, r.Port))
			}
		case environments.NetWatchUDP:
			if !isLoopback(r.Dst) {
				attempt(fmt.Sprintf("udp %s:%d", r.Dst, r.Port))
			}
		case environments.NetWatchHTTP:
			switch {
			case gwIPs[r.Dst] || isGatewayName(hostOnly(r.Host), host):
				s.gatewayReqs = append(s.gatewayReqs, r)
			case runIPs[r.Dst] || isRunHost(hostOnly(r.Host)):
			case !isLoopback(r.Dst):
				attempt(fmt.Sprintf("http://%s%s", firstNonEmpty(r.Host, r.Dst), r.Path))
			}
		}
	}
	s.attempts = dropSearchExpansions(s.attempts)
	sort.Strings(s.attempts)
	return s
}

// dropSearchExpansions removes a DNS name that is another attempted name plus
// a resolver search suffix (example.com.corp.lan after example.com): the
// resolver tried it, not the agent.
func dropSearchExpansions(attempts []string) []string {
	out := attempts[:0:0]
	for _, a := range attempts {
		expansion := false
		for _, b := range attempts {
			if b != a && strings.HasPrefix(a, b+".") {
				expansion = true
				break
			}
		}
		if !expansion {
			out = append(out, a)
		}
	}
	return out
}

// userRow: the bench always starts the agent as uid 1000; the row checks the
// container actually started that way and the image is built for it.
func userRow(in *checkInput) checkRow {
	row := newRow(rowUser)
	code := in.run.Agent.ExitCode
	switch {
	case code == nil:
		return row.set(checkFail, "the agent container did not run: "+firstFailure(in.run, schemas.StageRun),
			"check that argv[0] exists in the image and runs as uid 1000 with a read-only root filesystem")
	case *code == 126 || *code == 127:
		return row.set(checkFail, fmt.Sprintf("exit %d: the command %q was not found or not executable", *code, firstArg(in.agent.Argv)),
			"put the agent's command on PATH in the image (chmod 0755) and name it in argv[0]")
	case !in.image.NonRoot():
		return row.set(checkWarn, fmt.Sprintf("ran as uid 1000, but the image USER is %q", firstNonEmpty(in.image.User, "root")),
			"end the Dockerfile with USER 1000 and test the agent as that user; it gets no root at run time")
	default:
		return row.set(checkPass, fmt.Sprintf("ran as uid 1000 (image USER %s), no capabilities, read-only root", in.image.User), "")
	}
}

// modelCallRow: Bifrost logged the run's model calls under the run's key,
// and the fault proxy answered every one from the scripted model.
func modelCallRow(in *checkInput, net netSummary) checkRow {
	row := newRow(rowModelCall)
	real := 0
	for _, e := range in.ev.Ledger {
		if e.Upstream == schemas.LedgerUpstreamReal && e.Action == "pass" {
			real++
		}
	}
	if real > 0 {
		return row.set(checkFail, fmt.Sprintf("%d calls were forwarded to a real provider: the proxy did not keep the run on the scripted model", real),
			"stop and rebuild the fault proxy; plan mode scripted must never forward")
	}
	var ok, failed int
	var firstErr string
	if in.ev.Requests != nil {
		for _, r := range in.ev.Requests.Requests {
			if r.Kind != "message" {
				continue
			}
			if r.Status == "error" {
				failed++
				if firstErr == "" {
					firstErr = firstNonEmpty(r.ErrorMessage, fmt.Sprintf("status %d", r.ErrorStatus))
				}
				continue
			}
			ok++
		}
	}
	switch {
	case ok+failed == 0 && len(net.gatewayReqs) > 0:
		return row.set(checkFail, fmt.Sprintf("%d requests reached the gateway (%s) but Bifrost logged none for the run key", len(net.gatewayReqs), requestList(net.gatewayReqs)),
			"send {{run.key}} as the credential (Authorization: Bearer or x-api-key) on a provider route such as /openai/v1/chat/completions or /anthropic/v1/messages")
	case ok+failed == 0:
		return row.set(checkFail, "no model call reached Bifrost",
			"send every model call to {{gateway.url}}/<route> with {{run.key}} from env; check the base URL variable your SDK reads")
	case ok == 0:
		return row.set(checkFail, fmt.Sprintf("%d model calls reached Bifrost and all failed: %s", failed, firstErr),
			fmt.Sprintf("the run key allows provider %s and model %s only; send that model id (with the provider prefix your route needs)", in.agent.Gateway.Provider, in.agent.Model))
	case failed > 0:
		return row.set(checkWarn, fmt.Sprintf("%d model calls answered, %d failed: %s", ok, failed, firstErr),
			"every call, including title, summary and sub-agent calls, must use the run's one model")
	default:
		return row.set(checkPass, plural(ok, "model call")+" through Bifrost with the run key, answered by the scripted model", "")
	}
}

// sessionRow: the x-bf-session-id header, read on the wire by the observer.
// Bifrost does not log the header, so without the observer the row can only
// say whether the config passes {{run.session}}.
func sessionRow(in *checkInput, net netSummary) checkRow {
	row := newRow(rowSession)
	fix := "send the header x-bf-session-id: {{run.session}} (and x-bf-session-affinity: off) on every model call; rows still join by the run key without it"
	if in.netErr != "" {
		if hf, ok := helperFix(in.netErr); ok {
			return row.set(checkWarn, "not observed on the wire ("+in.netErr+")", hf)
		}
		if configUses(in.agent, sessionVar) {
			return row.set(checkWarn, "not observed on the wire ("+in.netErr+"); the config passes "+sessionVar, "make sure the agent sends it as x-bf-session-id")
		}
		return row.set(checkWarn, "not observed ("+in.netErr+") and the config never passes "+sessionVar, fix)
	}
	want := in.run.Gateway.SessionID
	if want == "" {
		want = in.run.RunID
	}
	var match, other int
	var otherVal string
	for _, r := range net.gatewayReqs {
		switch {
		case r.Session == want:
			match++
		case r.Session != "":
			other++
			otherVal = r.Session
		}
	}
	switch {
	case len(net.gatewayReqs) == 0:
		return row.set(checkWarn, "no request to the gateway was seen", fix)
	case match > 0 && match == len(net.gatewayReqs):
		return row.set(checkPass, fmt.Sprintf("x-bf-session-id: %s on every gateway request seen (%s)", want, requestList(net.gatewayReqs)), "")
	case match > 0:
		return row.set(checkWarn, fmt.Sprintf("x-bf-session-id missing on some gateway requests (%d of %d distinct)", len(net.gatewayReqs)-match, len(net.gatewayReqs)), fix)
	case other > 0:
		return row.set(checkWarn, fmt.Sprintf("x-bf-session-id is %q, not the run's session %q", otherVal, want), "pass "+sessionVar+", not your own id")
	default:
		return row.set(checkWarn, fmt.Sprintf("none of %d gateway requests carried x-bf-session-id", len(net.gatewayReqs)), fix)
	}
}

// exitRow: the agent exited by itself, with code 0, before the wall budget.
func exitRow(in *checkInput) checkRow {
	row := newRow(rowExit)
	switch code := in.run.Agent.ExitCode; {
	case code == nil:
		return row.set(checkFail, "the agent did not run", "see the user row")
	case in.run.Agent.TimedOut:
		return row.set(checkFail, fmt.Sprintf("killed at the wall budget (%ds)", in.wallS),
			"exit when the task is done; cap your own turns and time below budgets.wall_s")
	case *code != 0:
		return row.set(checkFail, fmt.Sprintf("exited with code %d%s", *code, lastLine(in.stderr)),
			"exit 0 when a run finished (solved or not); a non-zero exit means the agent crashed")
	default:
		return row.set(checkPass, fmt.Sprintf("exited 0 after %s (budget %ds)", agentDuration(in.run), in.wallS), "")
	}
}

// diffRow: the trusted export ran on the workspace. With the scripted model
// an empty diff is expected: it asks for no edit.
func diffRow(in *checkInput) checkRow {
	row := newRow(rowDiff)
	d := in.ev.Diff
	if d == nil {
		return row.set(checkFail, "no diff exported: "+firstFailure(in.run, schemas.StageExport),
			"keep /workspace a git work tree: never delete /workspace/.git or replace the folder")
	}
	var rejected []string
	for _, r := range d.Rejected {
		if r.Reason != environments.RejectCache {
			rejected = append(rejected, r.Path+" ("+r.Reason+")")
		}
	}
	detail := "exported, " + plural(len(d.Files), "file") + " changed"
	if len(d.Files) == 0 {
		detail += " (expected: the scripted model asks for no edit)"
	}
	if len(rejected) > 0 {
		return row.set(checkWarn, fmt.Sprintf("%s; %d paths dropped: %s", detail, len(rejected), listed(rejected)),
			"write regular files inside /workspace only: no symlinks, devices or submodules")
	}
	return row.set(checkPass, detail, "")
}

// egressRow: the run stayed isolated (the egress gate) and the observer saw
// nothing leave for a host other than the gateway.
func egressRow(in *checkInput, net netSummary) checkRow {
	row := newRow(rowEgress)
	for _, g := range in.gates {
		if g.Gate == "egress" && g.Fired {
			fix := "run bifrost-bench doctor; the agent must attach only to the internal evaluation network"
			if hf, ok := helperFix(g.Detail); ok {
				fix = hf
			}
			return row.set(checkFail, "isolation not proven: "+g.Detail, fix)
		}
	}
	if in.netErr != "" {
		fix := "the helper image needs python3 and sleep for the observer"
		if hf, ok := helperFix(in.netErr); ok {
			fix = hf
		}
		return row.set(checkWarn, "attempts not observed: "+in.netErr, fix)
	}
	if len(net.attempts) > 0 {
		return row.set(checkFail, fmt.Sprintf("tried to reach %s other than the gateway: %s", plural(len(net.attempts), "host"), listed(net.attempts)),
			"switch off telemetry, update checks and downloads at start, bake dependencies into the image, and send model calls only to {{gateway.url}}")
	}
	return row.set(checkPass, fmt.Sprintf("only the gateway %s was contacted", in.gatewayHost), "")
}

// helperFix is the fix hint for a detail that blames the helper image
// (utils.HelperLacks): the cause is the project's images.helper, not the
// agent, so the agent-side hints would send its author the wrong way.
func helperFix(detail string) (string, bool) {
	if !strings.Contains(detail, "set images.helper") {
		return "", false
	}
	return "set images.helper in deploy/bench.yaml to the bench helper image (bench/coding-core-base:1, built from deploy/helper/Dockerfile), then run bifrost-bench doctor", true
}

// evidenceRow: which evidence level the agent's runs carry.
func evidenceRow(in *checkInput) checkRow {
	row := newRow(rowEvidence)
	if in.ev.AgentTrace != nil {
		d := in.ev.AgentTrace
		if d.Quality != schemas.TraceQualityComplete || d.Emitted == 0 {
			return row.set(checkWarn, fmt.Sprintf("%s trace: %s (%d events, %d malformed, %d oversized)", d.Source, d.Quality, d.Emitted, d.Malformed, d.Oversized),
				"emit bifrost.agent.event records or configure a parser matching the harness output")
		}
		if d.Emitted > 0 {
			return row.set(checkPass, fmt.Sprintf("%s trace: %s (%s)", d.Source, d.Quality, plural(d.Emitted, "event")), "")
		}
	}
	if in.agent.Parser != "" {
		native := 0
		for _, e := range in.ev.Events {
			if e.Evidence == schemas.EvidenceNative {
				native++
			}
		}
		if native == 0 {
			return row.set(checkWarn, fmt.Sprintf("parser %s read no events from stdout; runs fall back to gateway level", in.agent.Parser),
				"check that the parser matches the harness version and the harness prints its stream to stdout")
		}
		return row.set(checkPass, fmt.Sprintf("native: parser %s read %s, plus gateway rows", in.agent.Parser, plural(native, "event")), "")
	}
	if in.ev.Requests != nil && !in.ev.Requests.Complete && in.ev.Requests.Reason != "" {
		return row.set(checkWarn, "gateway only, and the gateway rows are incomplete: "+in.ev.Requests.Reason,
			"every model call must go through {{gateway.url}} with the run key")
	}
	return row.set(checkPass, "gateway only (no parser): success, steps, tokens, cost and time; add a parser for tool targets", "")
}

// claimRow: a final message was read from the end of stdout (or by the
// parser), so false completion can be measured.
func claimRow(in *checkInput) checkRow {
	row := newRow(rowClaim)
	c := in.run.Claim
	if c == nil || strings.TrimSpace(c.Text) == "" {
		return row.set(checkWarn, "no final message on stdout",
			"print the final message last on stdout; the bench reads its last 4 KB")
	}
	claim := "no success claim"
	if c.Made {
		claim = "success claimed (" + c.Matched + ")"
	}
	return row.set(checkPass, fmt.Sprintf("%q, %s, from %s", excerptText(lastLineOf(c.Text), 60), claim, c.Source), "")
}

// servicesRow: every service of the task started and passed its ready check.
func servicesRow(sc *checkScenario) checkRow {
	row := newRow(rowServices)
	want := utils.TaskServices(sc.suite, sc.task)
	if sc.services == nil {
		return row.set(checkFail, "no service record in the run (services.json and evidence are empty): unknown whether the services started",
			"run bifrost-bench doctor and check that the environment supports services; the run folder should hold services.json")
	}
	byID := map[string]schemas.ServiceResult{}
	for _, r := range sc.services {
		byID[r.ID] = r
	}
	var ready, bad []string
	for _, sv := range want {
		r, ok := byID[sv.ID]
		switch {
		case !ok:
			bad = append(bad, sv.ID+" (not started)")
		case !r.Ready:
			bad = append(bad, fmt.Sprintf("%s (%s)", sv.ID, firstNonEmpty(r.Error, "ready check failed")))
		default:
			label := sv.ID
			if r.Proxied {
				label += " via the tool proxy"
			}
			ready = append(ready, label)
		}
	}
	if len(bad) > 0 {
		return row.set(checkFail, fmt.Sprintf("%d of %d services not ready: %s", len(bad), len(want), listed(bad)),
			"build the service image (bifrost-bench suite admit builds it) and run its ready check by hand: docker run, then the ready argv")
	}
	return row.set(checkPass, fmt.Sprintf("%s ready: %s", plural(len(ready), "service"), listed(ready)), "")
}

// mcpConfigRow: the run's MCP servers reached the agent, in BENCH_MCP_SERVERS
// and, unless the agent takes them from the environment only, in its own
// config file.
func mcpConfigRow(in *checkInput) checkRow {
	row := newRow(rowMCP)
	raw := in.plan.Env[utils.EnvMCPServers]
	var servers map[string]struct {
		URL string `json:"url"`
	}
	if raw == "" || json.Unmarshal([]byte(raw), &servers) != nil || len(servers) == 0 {
		return row.set(checkFail, "the agent's plan carries no "+utils.EnvMCPServers+": the agent was not told about the MCP server",
			"the bench sets it for every mcp service the agent sees; check the service's role (mcp) and that it is not hidden")
	}
	ids := make([]string, 0, len(servers))
	for id := range servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var files []string
	for p, content := range in.plan.Files {
		all := true
		for _, id := range ids {
			all = all && strings.Contains(content, servers[id].URL)
		}
		if all {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	format := ""
	if in.agent.MCP != nil {
		format = in.agent.MCP.Format
	}
	switch {
	case len(files) > 0:
		return row.set(checkPass, fmt.Sprintf("%s (%s) written to %s and %s", strings.Join(ids, ", "), servers[ids[0]].URL, listed(files), utils.EnvMCPServers), "")
	case format == "env" || in.agent.Adapter == "scripted":
		return row.set(checkPass, fmt.Sprintf("%s in %s only (mcp.format env)", strings.Join(ids, ", "), utils.EnvMCPServers), "")
	default:
		return row.set(checkWarn, fmt.Sprintf("%s in %s, but no config file of the agent names %s", strings.Join(ids, ", "), utils.EnvMCPServers, servers[ids[0]].URL),
			"set mcp.format (codex_toml, claude_json, opencode_json, generic_json with mcp.config_path) or read "+utils.EnvMCPServers+" yourself")
	}
}

// toolCallRow: the tool proxy recorded the agent's traffic to a proxied
// service. The scripted model calls only the first tool an agent offers, so
// an agent whose first tool is built in lists the server's tools but calls
// none; that is still proof the proxy path works.
func toolCallRow(sc *checkScenario) checkRow {
	row := newRow(rowToolCall)
	if sc.tools == nil {
		return row.set(checkFail, "no tool ledger: "+firstNonEmpty(sc.toolsErr, "the run recorded none")+"; tool metrics are unknown, not zero",
			"check that the tool proxy started (services row) and that images.toolproxy is the current bench image")
	}
	var calls, lists, faulted int
	tools := map[string]bool{}
	servers := map[string]bool{}
	for _, c := range sc.tools {
		servers[c.Server] = true
		switch {
		case c.Tool == "tools/list":
			lists++
		default:
			calls++
			tools[c.Tool] = true
		}
		if c.Fault != "" {
			faulted++
		}
	}
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	sort.Strings(names)
	srv := make([]string, 0, len(servers))
	for n := range servers {
		srv = append(srv, n)
	}
	sort.Strings(srv)
	mcp := false
	for _, sv := range utils.TaskServices(sc.suite, sc.task) {
		mcp = mcp || sv.Role == schemas.ServiceRoleMCP
	}
	switch {
	case calls > 0:
		return row.set(checkPass, fmt.Sprintf("%s recorded on %s (%s), %d tool lists", plural(calls, "tool call"), strings.Join(srv, ", "), listed(names), lists), "")
	case lists > 0:
		return row.set(checkPass, fmt.Sprintf("the agent listed the tools of %s through the proxy (%d tools/list); no tools/call, as expected: the scripted model calls only the first tool offered", strings.Join(srv, ", "), lists), "")
	case mcp:
		return row.set(checkFail, "the proxy recorded nothing: the agent never contacted the MCP server",
			"make the agent load its MCP config at start (see the MCP config row) and connect before its first model call")
	default:
		return row.set(checkWarn, "the proxy recorded no call: with the scripted model an agent calls an HTTP tool only if the model asks it to",
			"nothing to fix for the scripted model; a real model must reach the service at http://toolproxy:9200/s/<service id>/")
	}
}

// sessionsRow: every session ran, in order, each exiting by itself; the
// memory volume (and the agent's memory.paths) were kept between them.
func sessionsRow(in *checkInput) checkRow {
	row := newRow(rowSessions)
	sc := in.scenario
	want := sc.task.Sessions
	if len(sc.sessions) == 0 {
		return row.set(checkFail, fmt.Sprintf("no session record (sessions.json): unknown whether the %d sessions ran", len(want)),
			"the run folder should hold sessions.json; check that the environment supports sessions")
	}
	var ids []string
	for i, r := range sc.sessions {
		ids = append(ids, r.ID)
		switch {
		case i >= len(want) || r.ID != want[i].ID || r.Index != i+1:
			return row.set(checkFail, fmt.Sprintf("session %d is %q (index %d), want %q", i+1, r.ID, r.Index, sessionID(want, i)),
				"sessions must run in the order task.yaml lists them")
		case i > 0 && r.Started.Before(sc.sessions[i-1].Ended):
			return row.set(checkFail, fmt.Sprintf("session %s started before session %s ended", r.ID, sc.sessions[i-1].ID), "sessions must not overlap")
		case r.TimedOut:
			return row.set(checkFail, fmt.Sprintf("session %s hit its wall budget", r.ID), "exit when the session's work is done")
		case r.ExitCode != 0:
			return row.set(checkFail, fmt.Sprintf("session %s exited with code %d", r.ID, r.ExitCode), "exit 0 at the end of every session")
		}
	}
	if len(sc.sessions) < len(want) {
		return row.set(checkFail, fmt.Sprintf("%d of %d sessions ran (%s)", len(sc.sessions), len(want), strings.Join(ids, ", ")),
			"a session that crashes or times out ends the run; see the exit row")
	}
	kept := utils.DefaultMemoryDir + " (" + utils.EnvMemoryDir + ")"
	if in.agent.Memory == nil || len(in.agent.Memory.Paths) == 0 {
		return row.set(checkWarn, fmt.Sprintf("%d sessions ran in order (%s); only %s was kept between them: the agent declares no memory.paths",
			len(sc.sessions), strings.Join(ids, ", "), kept),
			"if the agent keeps memory anywhere but "+utils.DefaultMemoryDir+", list that folder under memory.paths, or it is lost between sessions")
	}
	return row.set(checkPass, fmt.Sprintf("%d sessions ran in order (%s); kept between them: %s and %s",
		len(sc.sessions), strings.Join(ids, ", "), kept, strings.Join(in.agent.Memory.Paths, ", ")), "")
}

func sessionID(want []schemas.SessionSpec, i int) string {
	if i < len(want) {
		return want[i].ID
	}
	return "none"
}

// answerRow: the answer was exported, from the answer file, else the final
// message.
func answerRow(sc *checkScenario) checkRow {
	row := newRow(rowAnswer)
	file := utils.AnswerPath(sc.suite, sc.task)
	a := sc.answer
	switch {
	case a == nil || a.Source == "missing":
		return row.set(checkFail, "no answer: neither "+file+" nor a final message",
			"write the answer to $"+utils.EnvAnswerPath+" ("+file+" in the workspace) or print it as the final message")
	case a.Source == "final_message":
		return row.set(checkWarn, fmt.Sprintf("no %s; the final message was used (%q)", file, excerptText(a.Text, 60)),
			"write the answer to $"+utils.EnvAnswerPath+"; a final message carries whatever else the agent said too")
	default:
		return row.set(checkPass, fmt.Sprintf("%s written (%d bytes): %q", file, len(a.Text), excerptText(a.Text, 60)), "")
	}
}

func newRow(id string) checkRow {
	return checkRow{ID: id, Title: rowTitles[id]}
}

// set fills a row's status, detail and fix hint.
func (r checkRow) set(status, detail, fix string) checkRow {
	r.Status, r.Detail, r.Fix = status, detail, fix
	return r
}

// configUses reports whether a template variable appears in argv, env or
// files of the agent config.
func configUses(cfg *schemas.AgentConfig, v string) bool {
	for _, a := range cfg.Argv {
		if strings.Contains(a, v) {
			return true
		}
	}
	for _, m := range []map[string]string{cfg.Env, cfg.Files} {
		for _, s := range m {
			if strings.Contains(s, v) {
				return true
			}
		}
	}
	return false
}

// firstFailure is the first part failure recorded at stage, or a fallback.
func firstFailure(run *schemas.Run, stage schemas.Stage) string {
	for _, f := range run.Failures {
		if f.Stage == stage {
			return f.Part + ": " + f.Error
		}
	}
	if len(run.Failures) > 0 {
		f := run.Failures[0]
		return string(f.Stage) + " " + f.Part + ": " + f.Error
	}
	return "no failure recorded"
}

func isGatewayName(name, host string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	return host != "" && (name == host || strings.HasPrefix(name, host+"."))
}

func isLoopback(ip string) bool {
	return strings.HasPrefix(ip, "127.") || ip == "::1"
}

// hostOnly drops the port from a Host header value.
func hostOnly(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}

func requestList(reqs []environments.NetWatchRecord) string {
	var out []string
	seen := map[string]bool{}
	for _, r := range reqs {
		s := r.Method + " " + r.Path
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return listed(out)
}

// listed joins the first maxListed items and counts the rest.
func listed(items []string) string {
	if len(items) <= maxListed {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:maxListed], ", ") + fmt.Sprintf(" and %d more", len(items)-maxListed)
}

// lastLine is ": <last line of s>" for a detail, or "" when s is empty.
func lastLine(s string) string {
	if l := lastLineOf(s); l != "" {
		return ": " + excerptText(l, 120)
	}
	return ""
}

// lastLineOf is the last non-empty line of s: an agent prints its final
// message last.
func lastLineOf(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func agentDuration(run *schemas.Run) string {
	t := run.Timing
	if t.AgentStarted == nil || t.AgentExited == nil {
		return "an unknown time"
	}
	return t.AgentExited.Sub(*t.AgentStarted).Round(100 * time.Millisecond).String()
}

func excerptText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func firstArg(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

// plural is "1 host" or "3 hosts".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
