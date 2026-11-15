package evidence

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCollectRemote(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "agent.stream.jsonl")
	lines := []string{
		`{"type": "request", "session_index": 1}`,
		`@@heimdall-event {"type": "tool_call", "name": "x"}`,
		`{"type": "remote_report", "usage": {"input_tokens": 100, "output_tokens": 20, "cost_usd": 0.01, "model": "m"}, "version": "v1", "events": 1}`,
		`{"type": "remote_report", "usage": {"input_tokens": 50, "output_tokens": -3, "cost_usd": "x"}, "build": "b\u0000 sk-abcdefghijklmnop", "events": 2, "notes": ["n1"]}`,
		`{"type": "result", "answer_bytes": 4}`,
	}
	if err := os.WriteFile(stream, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{Dir: dir, Exec: &schemas.ExecResult{StdoutPath: stream},
		Agent: &schemas.AgentConfig{Remote: &schemas.RemoteAgent{URL: "https://a", Tools: schemas.RemoteToolsExpose}}}
	ev := &schemas.Evidence{}
	CollectRemote(rc, ev)
	r := ev.Remote
	if r == nil || r.Reported == nil {
		t.Fatalf("remote %+v", r)
	}
	rep := r.Reported
	if *rep.InputTokens != 150 || *rep.OutputTokens != 20 || *rep.CostUSD != 0.01 || rep.Model != "m" || rep.Version != "v1" || rep.Events != 3 {
		t.Fatalf("report %+v", rep)
	}
	if strings.Contains(rep.Build, "sk-") || strings.ContainsRune(rep.Build, 0) {
		t.Fatalf("build not made inert: %q", rep.Build)
	}
	if r.Channels != (schemas.RemoteChannels{ModelTraffic: "off", Tools: "missing", SelfReport: "on"}) {
		t.Fatalf("channels %+v", r.Channels)
	}
	if !slices.Contains(r.Unknown, "tokens") || slices.Contains(r.Trusted, "tokens") || !slices.Contains(r.AgentReported, "reported_tokens") {
		t.Fatalf("summary %+v", r)
	}
	if ev.Requests != nil {
		t.Fatal("self-report touched the gateway log")
	}

	// Published tools and gateway traffic: tool calls and tokens are trusted.
	if err := os.WriteFile(filepath.Join(dir, schemas.RemoteToolsFile), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.Agent.Remote.ModelTraffic = "gateway"
	ev = &schemas.Evidence{}
	CollectRemote(rc, ev)
	if ev.Remote.Channels.Tools != "on" || ev.Remote.Channels.ModelTraffic != "on" || !slices.Contains(ev.Remote.Trusted, "tokens") || slices.Contains(ev.Remote.Unknown, "tokens") {
		t.Fatalf("summary %+v", ev.Remote)
	}

	// No report: the self-report channel is off; a local agent gets nothing.
	if err := os.WriteFile(stream, []byte(`{"type":"result"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ev = &schemas.Evidence{}
	CollectRemote(rc, ev)
	if ev.Remote.Reported != nil || ev.Remote.Channels.SelfReport != "off" {
		t.Fatalf("no report: %+v", ev.Remote)
	}
	ev = &schemas.Evidence{}
	CollectRemote(&schemas.RunContext{Agent: &schemas.AgentConfig{}}, ev)
	if ev.Remote != nil {
		t.Fatal("a local agent got remote evidence")
	}
}
