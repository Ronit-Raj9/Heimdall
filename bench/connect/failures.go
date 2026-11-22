package connect

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The failure catalog (section 4.10): every way connecting an agent goes
// wrong, the message shown verbatim and the one-click fix. Detect and
// Verify report these, and ExplainRunError maps a real run's launcher error
// to the same entry, so a failed run says what a failed verification says.

// Failure codes.
const (
	FailDNS            = "dns"
	FailPrivateAddress = "private_address"
	FailIPAllowlist    = "ip_allowlist"
	FailTLS            = "tls"
	FailAuthMissing    = "auth_missing"
	FailAuthWrong      = "auth_wrong"
	FailSigV4          = "sigv4"
	FailPath           = "path"
	FailField          = "field"
	FailNoAnswer       = "no_answer"
	FailEcho           = "echo"
	FailTimeout        = "timeout"
	FailRateLimit      = "rate_limit"
	FailUpstream5xx    = "upstream_5xx"
	FailOneShot        = "one_shot"
	FailMemoryLeak     = "memory_leak"
	FailOwnModelKey    = "own_model_key"
	FailToolsIgnored   = "tools_ignored"
	FailA2AVersion     = "a2a_version"
	FailImageArch      = "image_arch"
	FailImageRoot      = "image_root"
	FailBindLoopback   = "bind_loopback"
	FailEgressBlocked  = "egress_blocked"
	FailNoOrgKey       = "no_org_key"
)

// Fix actions the UI and the CLI offer next to a failure.
const (
	ActionCopyCommand     = "copy_command"
	ActionOpenWorkers     = "open_workers"
	ActionOpenAuth        = "open_auth"
	ActionInlineSecret    = "inline_secret"
	ActionEditSecret      = "edit_secret"
	ActionSwitchAuth      = "switch_auth"
	ActionSwitchConnector = "switch_connector"
	ActionAcceptMapping   = "accept_mapping"
	ActionPickField       = "pick_field"
	ActionPollSettings    = "poll_settings"
	ActionSetConcurrency  = "set_concurrency"
	ActionToggleCost      = "toggle_cost"
	ActionAddEgress       = "add_egress"
	ActionEditCommand     = "edit_command"
	ActionSwitchBenchmark = "switch_benchmark"
)

// Fix is the one-click fix of a failure.
type Fix struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	// Value is what the action needs: the command to copy, the secret
	// name, the field, the connector.
	Value string `json:"value,omitempty"`
}

// Failure is one catalog entry, its message filled in.
type Failure struct {
	Code    string `json:"code"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Fix     *Fix   `json:"fix,omitempty"`
	// Infra marks a failure of the network or the platform in between, not
	// of the agent: a run that fails this way is retried, not scored.
	Infra bool `json:"infra,omitempty"`
	// Options are the fields to pick from (no_answer).
	Options []string `json:"options,omitempty"`
}

// CatalogEntry is a catalog row as GET /connect/failures lists it: the
// message template with its {placeholders}.
type CatalogEntry struct {
	Code     string `json:"code"`
	Title    string `json:"title"`
	Trigger  string `json:"trigger"`
	Template string `json:"message"`
	Action   string `json:"action,omitempty"`
	Label    string `json:"label,omitempty"`
	Infra    bool   `json:"infra,omitempty"`
}

var catalog = []CatalogEntry{
	{Code: FailDNS, Title: "Name does not resolve", Trigger: "the name does not resolve from the run target",
		Template: "{host} does not resolve from where your runs go ({target}). If it is an internal name, run `heimdall serve {url}` inside your network or connect your own worker.",
		Action:   ActionCopyCommand, Label: "Copy the serve command", Infra: true},
	{Code: FailPrivateAddress, Title: "Private address", Trigger: "the host is a private, loopback or internal address on the shared worker",
		Template: "{host} is not a public internet address. The shared worker only calls public addresses. Run `heimdall serve {url} --map auto` on a machine that can reach it; it connects out only.",
		Action:   ActionCopyCommand, Label: "Copy the serve command"},
	{Code: FailIPAllowlist, Title: "Connection refused", Trigger: "connection reset or 403 with no auth challenge",
		Template: "Your endpoint refused the connection from {source}. If it allows only listed addresses, add the shared worker's addresses shown on Workers.",
		Action:   ActionOpenWorkers, Label: "Open Workers", Infra: true},
	{Code: FailTLS, Title: "Certificate not valid", Trigger: "certificate invalid or name mismatch",
		Template: "The TLS certificate of {host} is not valid for that name ({detail}). Heimdall does not skip certificate checks; fix the certificate or use mutual TLS settings with your CA.",
		Action:   ActionOpenAuth, Label: "Open auth settings"},
	{Code: FailAuthMissing, Title: "Token needed", Trigger: "401 with WWW-Authenticate: Bearer",
		Template: "Your endpoint wants a bearer token. Paste it here; it is stored as the secret {secret} and never shown again.",
		Action:   ActionInlineSecret, Label: "Add the token"},
	{Code: FailAuthWrong, Title: "Token rejected", Trigger: "401 or 403 after a token was sent",
		Template: "Your endpoint rejected the token in {secret} (HTTP {status}: '{snippet}'). Replace the secret value.",
		Action:   ActionEditSecret, Label: "Replace the secret"},
	{Code: FailSigV4, Title: "AWS signing needed", Trigger: "an AWS error header or x-amzn-errortype",
		Template: "This is an AWS endpoint that wants SigV4 signing. Give an access key with bedrock-agentcore:InvokeAgentRuntime; Heimdall signs each request.",
		Action:   ActionSwitchAuth, Label: "Use AWS signing"},
	{Code: FailPath, Title: "Wrong path", Trigger: "404 or 405 on the mapped path, another candidate answered",
		Template: "{method} {path} answered {status}, but {other} answered: this looks like {kind}. Use that connector?",
		Action:   ActionSwitchConnector, Label: "Switch connector"},
	{Code: FailField, Title: "Field named", Trigger: "422 naming a field",
		Template: "Your endpoint wants the field `{field}` ({type}). Heimdall will send the task there. Confirm?",
		Action:   ActionAcceptMapping, Label: "Use this field"},
	{Code: FailNoAnswer, Title: "Answer not found", Trigger: "2xx, nonce not found",
		Template: "Your endpoint answered, but the reply did not contain the check code {code}. Pick the field that holds the agent's reply:",
		Action:   ActionPickField, Label: "Pick the field"},
	{Code: FailEcho, Title: "Prompt echoed", Trigger: "the full prompt found in the answer",
		Template: "Your endpoint returned the prompt itself. Point Heimdall at the agent's route, not an echo or proxy route."},
	{Code: FailTimeout, Title: "No reply in time", Trigger: "no first byte within the timeout",
		Template: "No reply within {timeout} s. If the agent works longer, Heimdall can poll: does the first answer return a job id? Or turn on streaming.",
		Action:   ActionPollSettings, Label: "Poll or stream", Infra: true},
	{Code: FailRateLimit, Title: "Rate limited", Trigger: "429",
		Template: "Your endpoint answered 429 (rate limited) after {n} requests. Heimdall will send at most 2 at a time; change it under Limits.",
		Action:   ActionSetConcurrency, Label: "Set concurrency to 2", Infra: true},
	{Code: FailUpstream5xx, Title: "Load balancer error", Trigger: "502, 503, 504 before any body, after retries",
		Template: "Your load balancer answered {status} three times. This is counted as an infrastructure failure, not a failed task, and retried.",
		Infra:    true},
	{Code: FailOneShot, Title: "No memory between turns", Trigger: "the state row recalled nothing either way",
		Template: "Your agent does not remember earlier turns, with or without history. Conversation and memory benchmarks are left out of its report card."},
	{Code: FailMemoryLeak, Title: "Memory leaks across sessions", Trigger: "the isolation row recalled the code",
		Template: "Your agent remembered a code from another session. Repeated tests would not be independent. Add a reset call, or let Heimdall send a fresh user id in the field `{field}`.",
		Action:   ActionPickField, Label: "Pick the user id field"},
	{Code: FailOwnModelKey, Title: "Model calls not measured", Trigger: "gateway row: no call reached Bifrost",
		Template: "Your agent called its model provider directly, so Heimdall cannot measure its cost or inject model faults. Read OPENAI_BASE_URL and OPENAI_API_KEY from the request, or accept agent-reported cost.",
		Action:   ActionToggleCost, Label: "Accept agent-reported cost"},
	{Code: FailToolsIgnored, Title: "Tools not called", Trigger: "tool task: no call reached the tool gate",
		Template: "Your agent answered without calling the task's tools. It will score on answers only; support benchmarks need tool calls."},
	{Code: FailA2AVersion, Title: "Older A2A version", Trigger: "card protocolVersion below 1.0",
		Template: "Your A2A card is version {version}. Heimdall speaks 1.0 and falls back to message/send for older servers."},
	{Code: FailImageArch, Title: "Wrong image platform", Trigger: "the image has only arm64",
		Template: "This image is built for arm64 only; the shared worker runs amd64. Rebuild with --platform linux/amd64."},
	{Code: FailImageRoot, Title: "Image runs as root", Trigger: "config User empty or 0 on the shared worker",
		Template: "This image runs as root. The shared worker runs agents as an unprivileged user; add USER to the Dockerfile."},
	{Code: FailBindLoopback, Title: "Listens on loopback only", Trigger: "sidecar health fails, port open on 127.0.0.1 only",
		Template: "Your server listens on 127.0.0.1 inside its container, so Heimdall cannot reach it. Start it with --host 0.0.0.0.",
		Action:   ActionEditCommand, Label: "Edit the start command"},
	{Code: FailEgressBlocked, Title: "Blocked outbound call", Trigger: "blocked connection to a host during the real task",
		Template: "Your app tried to reach {host}, which runs do not allow. Allow it (recorded on first run, replayed after) or remove the call.",
		Action:   ActionAddEgress, Label: "Allow this host"},
	{Code: FailNoOrgKey, Title: "Provider key needed", Trigger: "the benchmark's judge or simulated user needs a provider the org has no key for",
		Template: "This benchmark uses a judge model. Add a key for OpenAI or Anthropic, or pick a benchmark graded without a model.",
		Action:   ActionSwitchBenchmark, Label: "Pick another benchmark"},
}

// Catalog lists every entry, in catalog order.
func Catalog() []CatalogEntry {
	return append([]CatalogEntry(nil), catalog...)
}

var placeholderRe = regexp.MustCompile(`\{[a-z_]+\}`)

// Lookup fills the code's message with vars. Placeholders without a value
// keep a plain stand-in, so a message never shows braces.
func Lookup(code string, vars map[string]string) Failure {
	for _, e := range catalog {
		if e.Code != code {
			continue
		}
		msg := placeholderRe.ReplaceAllStringFunc(e.Template, func(ph string) string {
			k := ph[1 : len(ph)-1]
			if v := vars[k]; v != "" {
				return v
			}
			return standIn[k]
		})
		f := Failure{Code: e.Code, Title: e.Title, Message: msg, Infra: e.Infra}
		if e.Action != "" {
			f.Fix = &Fix{Action: e.Action, Label: e.Label, Value: vars["fix"]}
		}
		return f
	}
	return Failure{Code: code, Title: "Unknown problem", Message: vars["detail"]}
}

var standIn = map[string]string{
	"host": "the endpoint", "target": "the shared worker", "url": "<your URL>",
	"source": "the shared worker", "detail": "name mismatch", "secret": "AGENT_TOKEN", "status": "401",
	"snippet": "no detail", "method": "POST", "path": "the mapped path", "other": "another route", "kind": "another kind of server",
	"field": "user_id", "type": "string", "code": "the check code", "timeout": "60", "n": "3", "version": "0.3",
}

// Run-time patterns of the launchers (adapters/remotehttp.py,
// adapters/openaiagent.py, adapters/a2a.py and the engine) and the catalog
// entry each maps to. The first match wins.
var runPatterns = []struct {
	re   *regexp.Regexp
	code string
}{
	{regexp.MustCompile(`(?i)(name or service not known|no such host|nodename nor servname|getaddrinfo|could not resolve|temporary failure in name resolution)`), FailDNS},
	{regexp.MustCompile(`(?i)(private address|private or local address|private network|loopback|not a public address|refuses private)`), FailPrivateAddress},
	{regexp.MustCompile(`(?i)(certificate|x509|ssl|tls handshake)`), FailTLS},
	{regexp.MustCompile(`(?i)(x-amzn-errortype|missing authentication token|signature.*(does not match|expired)|sigv4)`), FailSigV4},
	{regexp.MustCompile(`(?i)\b429\b|rate.?limit|too many requests`), FailRateLimit},
	{regexp.MustCompile(`(?i)\b(401|403)\b|unauthori[sz]ed|forbidden|invalid api key`), FailAuthWrong},
	{regexp.MustCompile(`(?i)\b(HTTP |status |answered |code )(502|503|504)\b|bad gateway|service unavailable|gateway time-?out`), FailUpstream5xx},
	{regexp.MustCompile(`(?i)(timed out|timeout|deadline exceeded|read timeout|did not answer within)`), FailTimeout},
	{regexp.MustCompile(`(?i)(connection reset|connection refused|econnreset|econnrefused)`), FailIPAllowlist},
	{regexp.MustCompile(`(?i)(\b(HTTP |status |answered )(404|405)\b|not found|method not allowed)`), FailPath},
	{regexp.MustCompile(`(?i)(\b(HTTP |status |answered )422\b|unprocessable|field required)`), FailField},
	{regexp.MustCompile(`(?i)(has no "answer" field|no answer|answer path matched nothing|empty answer|no output text|no message content)`), FailNoAnswer},
	{regexp.MustCompile(`(?i)(egress|blocked connection|not allowed to reach|denied by the egress)`), FailEgressBlocked},
	{regexp.MustCompile(`(?i)(protocolversion|a2a.*version)`), FailA2AVersion},
}

var hostInText = regexp.MustCompile(`(?i)\b([a-z0-9-]+(\.[a-z0-9-]+)+)(:[0-9]+)?\b`)
var statusInText = regexp.MustCompile(`\b([1-5][0-9][0-9])\b`)

// ExplainRunError maps a run's launcher or verification error text to its
// catalog entry. ok is false when no entry fits; the caller then shows the
// text as it is.
func ExplainRunError(text string) (Failure, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Failure{}, false
	}
	for _, p := range runPatterns {
		if !p.re.MatchString(text) {
			continue
		}
		vars := map[string]string{"detail": capText(text, 160)}
		if m := hostInText.FindString(text); m != "" && !strings.HasPrefix(m, "0.") {
			vars["host"] = m
		}
		if m := statusInText.FindString(text); m != "" {
			vars["status"] = m
		}
		vars["snippet"] = capText(text, 120)
		return Lookup(p.code, vars), true
	}
	return Failure{}, false
}

// statusFailure is the catalog entry of an HTTP status from a probe.
func statusFailure(status int, hdr map[string]string, sentToken bool, vars map[string]string) (Failure, bool) {
	if vars == nil {
		vars = map[string]string{}
	}
	vars["status"] = strconv.Itoa(status)
	switch {
	case hdr["x-amzn-errortype"] != "" || strings.Contains(strings.ToLower(hdr["www-authenticate"]), "aws4"):
		return Lookup(FailSigV4, vars), true
	case status == 401 && !sentToken:
		return Lookup(FailAuthMissing, vars), true
	case (status == 401 || status == 403) && sentToken:
		return Lookup(FailAuthWrong, vars), true
	case status == 403:
		if hdr["www-authenticate"] == "" {
			return Lookup(FailIPAllowlist, vars), true
		}
		return Lookup(FailAuthMissing, vars), true
	case status == 429:
		return Lookup(FailRateLimit, vars), true
	case status == 502 || status == 503 || status == 504:
		return Lookup(FailUpstream5xx, vars), true
	}
	return Failure{}, false
}

// Codes lists every catalog code, sorted (tests and docs).
func Codes() []string {
	out := make([]string, 0, len(catalog))
	for _, e := range catalog {
		out = append(out, e.Code)
	}
	sort.Strings(out)
	return out
}

func capText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
