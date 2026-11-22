package evidence

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Remote coverage (docs/remote-execution.md): what a remote_http agent's
// run measured, channel by channel. The launcher prints one remote_report
// line on stdout with what the deployment said about itself; that report is
// agent-reported and fills only Evidence.Remote.Reported, which the
// reported_tokens and reported_cost_usd metrics read. It never touches the
// gateway log, tokens, cost_usd or an outcome.

const (
	remoteMaxLine  = 1 << 20
	remoteMaxStr   = 256
	remoteMaxNotes = 20
)

var remoteSecretRe = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._~+/=-]{8,}|(?:api[_-]?key|token|secret|password)["']?\s*[:=]\s*["']?[^\s"',}]{4,})`)

// CollectRemote sets ev.Remote for a run of a remote agent; any other run is
// left alone. It reads the launcher's stdout for remote_report lines (all
// sessions summed) and the run folder for the published tool gate's record.
// A stream that cannot be read leaves the self-report channel off: the
// report is optional and its absence is not an error.
func CollectRemote(rc *schemas.RunContext, ev *schemas.Evidence) {
	if rc != nil && ev != nil && rc.Agent != nil && rc.Agent.Remote == nil && rc.Agent.Pull != nil {
		// Y11: a pull agent's tool calls run in the bench's launcher, through
		// the run's tool proxy, so they are always on the tool ledger.
		p := rc.Agent.Pull
		view := &schemas.RemoteAgent{Tools: schemas.RemoteToolsExpose, Conversation: p.Conversation, Repo: p.Repo}
		if p.Tools == schemas.OpenAIToolsOff {
			view.Tools = schemas.RemoteToolsOff
		}
		ev.Remote = RemoteSummary(view, view.Tools == schemas.RemoteToolsExpose, nil)
		return
	}
	if rc == nil || ev == nil || rc.Agent == nil || rc.Agent.Remote == nil {
		return
	}
	var rep *schemas.RemoteReport
	if rc.Exec != nil && rc.Exec.StdoutPath != "" {
		rep = readRemoteReports(rc.Exec.StdoutPath)
	}
	published := false
	if rc.Dir != "" {
		_, err := os.Stat(filepath.Join(rc.Dir, schemas.RemoteToolsFile))
		published = err == nil
	}
	ev.Remote = RemoteSummary(rc.Agent.Remote, published, rep)
}

// RemoteSummary states which channels were active and what is therefore
// trusted, agent-reported or unknown. It is pure.
func RemoteSummary(r *schemas.RemoteAgent, toolsPublished bool, rep *schemas.RemoteReport) *schemas.RemoteEvidence {
	out := &schemas.RemoteEvidence{Reported: rep}
	out.Trusted = []string{"outcome (verifier)", "wall_time_s", "the request and response the launcher exchanged"}
	out.Channels.ModelTraffic = schemas.RemoteChannelOff
	if r.ModelTraffic == "gateway" {
		out.Channels.ModelTraffic = schemas.RemoteChannelOn
		out.Trusted = append(out.Trusted, "tokens", "cost_usd", "model turns (gateway, on the run key only)")
	} else {
		out.Unknown = append(out.Unknown, "tokens", "cost_usd", "steps (model calls go outside this gateway)")
	}
	switch {
	case r.Tools != schemas.RemoteToolsExpose:
		out.Channels.Tools = schemas.RemoteChannelOff
		out.Unknown = append(out.Unknown, "tool calls (no tools published)")
	case toolsPublished:
		out.Channels.Tools = schemas.RemoteChannelOn
		out.Trusted = append(out.Trusted, "tool calls through the published tool proxy (tool ledger)", "service state")
	default:
		out.Channels.Tools = schemas.RemoteChannelMissing
		out.Unknown = append(out.Unknown, "tool calls (remote.tools expose, but nothing was published)")
	}
	if r.Conversation.BenchDriven() { // Y11
		out.Trusted = append(out.Trusted, "the conversation (the bench drove every turn; the simulated user wrote the transcript)")
	}
	if r.Repo == schemas.RepoTarball { // Y11: remote_patch
		out.Unknown = append(out.Unknown, "how a coding task's patch was made (remote_patch: the agent worked in its own sandbox; only the patch and its grade are the bench's)")
	}
	out.Channels.SelfReport = schemas.RemoteChannelOff
	if rep != nil {
		out.Channels.SelfReport = schemas.RemoteChannelOn
		if rep.InputTokens != nil || rep.OutputTokens != nil || rep.TotalTokens != nil {
			out.AgentReported = append(out.AgentReported, "reported_tokens")
		}
		if rep.CostUSD != nil {
			out.AgentReported = append(out.AgentReported, "reported_cost_usd")
		}
		if rep.Events > 0 {
			out.AgentReported = append(out.AgentReported, "events (agent-reported timeline rows)")
		}
		if rep.Version != "" || rep.Build != "" {
			out.AgentReported = append(out.AgentReported, "version and build (self-declared)")
		}
	}
	out.Unknown = append(out.Unknown, "the agent's own local tool calls, files and internal steps", "whether the deployment is the version its owner claims")
	return out
}

// remoteLine is the launcher's remote_report line.
type remoteLine struct {
	Type      string         `json:"type"`
	Usage     map[string]any `json:"usage"`
	Version   string         `json:"version"`
	Build     string         `json:"build"`
	Events    int            `json:"events"`
	Dropped   int            `json:"events_dropped"`
	Artifacts []string       `json:"artifacts"`
	Async     bool           `json:"async"`
	Notes     []string       `json:"notes"`
}

// readRemoteReports sums every remote_report line of the stream, or nil when
// there is none or the stream cannot be read.
func readRemoteReports(path string) *schemas.RemoteReport {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), remoteMaxLine)
	var rep *schemas.RemoteReport
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"`+schemas.RemoteReportType+`"`) {
			continue
		}
		var l remoteLine
		if json.Unmarshal(line, &l) != nil || l.Type != schemas.RemoteReportType {
			continue
		}
		if rep == nil {
			rep = &schemas.RemoteReport{}
		}
		mergeRemoteLine(rep, &l)
	}
	return rep
}

// mergeRemoteLine adds one session's report: counts sum, strings keep the
// last non-empty value. Every number is checked and every string is capped,
// stripped of control characters and redacted again here: the launcher's
// output is still the agent's word.
func mergeRemoteLine(rep *schemas.RemoteReport, l *remoteLine) {
	addInt := func(dst **int64, key string) {
		v, ok := l.Usage[key].(float64)
		if !ok || v < 0 || v > 1e12 || math.IsNaN(v) {
			return
		}
		n := int64(v)
		if *dst != nil {
			n += **dst
		}
		*dst = &n
	}
	addInt(&rep.InputTokens, "input_tokens")
	addInt(&rep.OutputTokens, "output_tokens")
	addInt(&rep.TotalTokens, "total_tokens")
	if v, ok := l.Usage["cost_usd"].(float64); ok && v >= 0 && v < 1e6 && !math.IsNaN(v) {
		if rep.CostUSD != nil {
			v += *rep.CostUSD
		}
		rep.CostUSD = &v
	}
	if m, ok := l.Usage["model"].(string); ok && inert(m) != "" {
		rep.Model = inert(m)
	}
	if s := inert(l.Version); s != "" {
		rep.Version = s
	}
	if s := inert(l.Build); s != "" {
		rep.Build = s
	}
	rep.Events += max(l.Events, 0)
	rep.Dropped += max(l.Dropped, 0)
	rep.Async = rep.Async || l.Async
	for _, a := range l.Artifacts {
		if s := inert(a); s != "" && len(rep.Artifacts) < 100 {
			rep.Artifacts = append(rep.Artifacts, s)
		}
	}
	for _, n := range l.Notes {
		if s := inert(n); s != "" && len(rep.Notes) < remoteMaxNotes {
			rep.Notes = append(rep.Notes, s)
		}
	}
}

// inert makes an agent-reported string safe to show: printable characters
// only, secrets redacted, capped.
func inert(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	s = remoteSecretRe.ReplaceAllString(s, "[redacted]")
	if len(s) > remoteMaxStr {
		s = s[:remoteMaxStr]
	}
	return strings.TrimSpace(s)
}
