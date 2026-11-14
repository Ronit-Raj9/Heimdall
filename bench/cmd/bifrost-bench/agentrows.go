package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// maxListed bounds how many hosts or paths a row detail lists.
const maxListed = 5

// sessionVar is the template variable that carries the session id.
const sessionVar = "{{run.session}}"

// checkRows evaluates every row after the image row from one sealed check
// run. It is pure: everything it reads is in in.
func checkRows(in *checkInput) []checkRow {
	net := summarizeNet(in.net, in.gatewayHost, in.gatewayPort)
	return []checkRow{
		userRow(in),
		modelCallRow(in, net),
		sessionRow(in, net),
		exitRow(in),
		diffRow(in),
		egressRow(in, net),
		evidenceRow(in),
		claimRow(in),
	}
}

// netSummary is the observer's records sorted into gateway traffic and
// everything else.
type netSummary struct {
	gatewayReqs []environments.NetWatchRecord // HTTP requests to the gateway
	attempts    []string                      // egress attempts: names, addresses
}

// summarizeNet splits the observer's records. The gateway is its host name
// and the addresses that name resolved to (or that HTTP requests naming it
// were sent to). Loopback traffic (an agent talking to its own server) is
// local. Any other DNS name, connection attempt, datagram or HTTP request
// is an egress attempt.
func summarizeNet(recs []environments.NetWatchRecord, host string, port int) netSummary {
	gwIPs := map[string]bool{}
	for _, r := range recs {
		switch {
		case r.T == environments.NetWatchDNSAnswer && isGatewayName(r.Name, host):
			for _, ip := range r.IPs {
				gwIPs[ip] = true
			}
		case r.T == environments.NetWatchHTTP && isGatewayName(hostOnly(r.Host), host):
			gwIPs[r.Dst] = true
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
			if !isGatewayName(r.Name, host) && r.Name != "localhost" && !strings.HasSuffix(r.Name, ".localhost") {
				attempt(r.Name)
			}
		case environments.NetWatchSYN:
			if !isLoopback(r.Dst) && !(gwIPs[r.Dst] && r.Port == port) {
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
		return row.set(checkPass, fmt.Sprintf("%d model calls through Bifrost with the run key, answered by the scripted model", ok), "")
	}
}

// sessionRow: the x-bf-session-id header, read on the wire by the observer.
// Bifrost does not log the header, so without the observer the row can only
// say whether the config passes {{run.session}}.
func sessionRow(in *checkInput, net netSummary) checkRow {
	row := newRow(rowSession)
	fix := "send the header x-bf-session-id: {{run.session}} (and x-bf-session-affinity: off) on every model call; rows still join by the run key without it"
	if in.netErr != "" {
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
	detail := fmt.Sprintf("exported, %d files changed", len(d.Files))
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
			return row.set(checkFail, "isolation not proven: "+g.Detail, "run bifrost-bench doctor; the agent must attach only to the internal evaluation network")
		}
	}
	if in.netErr != "" {
		return row.set(checkWarn, "attempts not observed: "+in.netErr, "the helper image needs python3 and sleep for the observer")
	}
	if len(net.attempts) > 0 {
		return row.set(checkFail, fmt.Sprintf("tried to reach %s other than the gateway: %s", plural(len(net.attempts), "host"), listed(net.attempts)),
			"switch off telemetry, update checks and downloads at start, bake dependencies into the image, and send model calls only to {{gateway.url}}")
	}
	return row.set(checkPass, fmt.Sprintf("only the gateway %s was contacted", in.gatewayHost), "")
}

// evidenceRow: which evidence level the agent's runs carry.
func evidenceRow(in *checkInput) checkRow {
	row := newRow(rowEvidence)
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
		return row.set(checkPass, fmt.Sprintf("native: parser %s read %d events, plus gateway rows", in.agent.Parser, native), "")
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
