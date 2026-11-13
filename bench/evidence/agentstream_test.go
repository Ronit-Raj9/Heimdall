package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeParser returns a fixed result and records what it was given.
type fakeParser struct {
	name string
	res  *schemas.ParseResult
	err  error
	raw  []byte
}

func (p *fakeParser) GetName() string    { return p.name }
func (p *fakeParser) GetVersion() string { return "0" }
func (p *fakeParser) Parse(_ context.Context, _ *schemas.RunContext, raw []byte) (*schemas.ParseResult, error) {
	p.raw = raw
	return p.res, p.err
}

func TestClaimFromTail(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		made    bool
		matched string
	}{
		{name: "plain claim", stdout: "I updated money.py.\nAll tests pass.", made: true, matched: "all tests pass"},
		{name: "counted tests", stdout: "Fixed the rounding bug; all 12 tests passed.", made: true, matched: "all 12 tests passed"},
		{name: "bare done", stdout: "Done.", made: true, matched: "done."},
		{name: "fixed the bug", stdout: "I've fixed the bug in the CSV parser.", made: true, matched: "i've fixed"},
		{name: "successfully", stdout: "**Successfully implemented the discount rule.**", made: true, matched: "successfully"},
		{name: "benign zero failures", stdout: "All tests pass (12 passed, 0 failed).", made: true, matched: "all tests pass"},
		{name: "json escaped stream line", stdout: `{"type":"result","result":"Edited app.py.\nAll tests pass."}`, made: true, matched: "all tests pass"},
		{name: "outage message", stdout: "API Error: 503 Service Unavailable. The model is currently unavailable.", made: false},
		{name: "outage stop", stdout: "The task could not be completed: the model was unavailable.", made: false},
		{name: "negated pass", stdout: "Tests are not passing yet.", made: false},
		{name: "contraction negation", stdout: "I didn't verify that the tests pass.", made: false},
		{name: "hedged", stdout: "The tests should pass now.", made: false},
		{name: "failure count", stdout: "Ran pytest: 3 failed, 9 passed.", made: false},
		{name: "unfortunately", stdout: "Unfortunately the task is complete only in part.", made: false},
		{name: "refusal", stdout: "I cannot help with that.", made: false},
		{name: "empty", stdout: "", made: false},
		{name: "claim outside the last 4 KB", stdout: "All tests pass.\n" + strings.Repeat("log line without claims\n", 200), made: false},
		{name: "claim inside the last 4 KB", stdout: strings.Repeat("log line\n", 1000) + "All tests pass.", made: true, matched: "all tests pass"},
		{name: "decimal does not split", stdout: "Bumped the version to 3.5 and the bug is fixed.", made: true, matched: "bug is fixed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ClaimFromTail([]byte(tt.stdout))
			if c.Made != tt.made || c.Matched != tt.matched {
				t.Errorf("claim made=%v matched=%q, want made=%v matched=%q", c.Made, c.Matched, tt.made, tt.matched)
			}
			if c.Source != schemas.EvidenceOutcome {
				t.Errorf("source %q, want outcome", c.Source)
			}
			if len(c.Text) > 500 {
				t.Errorf("text has %d bytes, want at most 500", len(c.Text))
			}
		})
	}
}

func TestClaimFromTailText(t *testing.T) {
	c := ClaimFromTail([]byte("\n  The model is unavailable; stopping.  \n"))
	if c.Text != "The model is unavailable; stopping." {
		t.Errorf("text = %q", c.Text)
	}
}

func TestAgentStreamCollect(t *testing.T) {
	writeStdout := func(t *testing.T, rc *schemas.RunContext, body string) {
		t.Helper()
		p := filepath.Join(rc.Dir, "agent.stream.jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		rc.Exec = &schemas.ExecResult{StdoutPath: p, Ended: t0}
	}
	t.Run("named parser supplies events and claim", func(t *testing.T) {
		rc := testRC(t)
		writeStdout(t, rc, `{"type":"result"}`)
		rc.Agent = &schemas.AgentConfig{Parser: "claudecode"}
		claim := &schemas.Claim{Made: true, Source: schemas.EvidenceNative, Matched: "all tests pass"}
		p := &fakeParser{name: "claudecode", res: &schemas.ParseResult{
			Events: []schemas.Event{{EventID: "r_1:agent_stream:1", Type: schemas.EventToolCall, Seq: 1}},
			Claim:  claim,
		}}
		ev := &schemas.Evidence{}
		src := newAgentStreamSource(Deps{Parsers: []schemas.Parser{&fakeParser{name: "other"}, p}})
		if err := src.Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if string(p.raw) != `{"type":"result"}` {
			t.Errorf("parser got %q", p.raw)
		}
		if ev.Claim != claim {
			t.Errorf("claim = %+v, want the parser's", ev.Claim)
		}
		if len(ev.Events) != 1 || ev.Events[0].RunID != "r_1" || ev.Events[0].Source != schemas.SourceAgentStream {
			t.Errorf("events = %+v", ev.Events)
		}
		if p.res.Events[0].RunID != "" {
			t.Errorf("parser result was modified")
		}
	})
	t.Run("no parser uses the tail", func(t *testing.T) {
		rc := testRC(t)
		writeStdout(t, rc, strings.Repeat("x", 10000)+"\nAll tests pass.\n")
		ev := &schemas.Evidence{}
		if err := newAgentStreamSource(Deps{}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if ev.Claim == nil || !ev.Claim.Made || ev.Claim.Source != schemas.EvidenceOutcome {
			t.Fatalf("claim = %+v", ev.Claim)
		}
		if len(ev.Events) != 1 || ev.Events[0].Type != schemas.EventClaim || ev.Events[0].EventID != "r_1:agent_stream:1" {
			t.Errorf("events = %+v", ev.Events)
		}
	})
	t.Run("unregistered parser falls back to the tail", func(t *testing.T) {
		rc := testRC(t)
		writeStdout(t, rc, "Service unavailable.")
		rc.Agent = &schemas.AgentConfig{Parser: "missing"}
		ev := &schemas.Evidence{}
		if err := newAgentStreamSource(Deps{}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if ev.Claim == nil || ev.Claim.Made || len(ev.Events) != 0 {
			t.Errorf("claim = %+v events = %+v", ev.Claim, ev.Events)
		}
	})
	t.Run("no stdout leaves claim nil", func(t *testing.T) {
		ev := &schemas.Evidence{}
		if err := newAgentStreamSource(Deps{}).Collect(context.Background(), testRC(t), ev); err != nil {
			t.Fatal(err)
		}
		if ev.Claim != nil {
			t.Errorf("claim = %+v, want nil", ev.Claim)
		}
	})
	t.Run("errors", func(t *testing.T) {
		rc := testRC(t)
		rc.Exec = &schemas.ExecResult{StdoutPath: filepath.Join(rc.Dir, "absent")}
		if err := newAgentStreamSource(Deps{}).Collect(context.Background(), rc, &schemas.Evidence{}); err == nil {
			t.Errorf("missing stdout: want error")
		}
		rc2 := testRC(t)
		writeStdout(t, rc2, "x")
		rc2.Agent = &schemas.AgentConfig{Parser: "bad"}
		p := &fakeParser{name: "bad", err: errors.New("bad json")}
		err := newAgentStreamSource(Deps{Parsers: []schemas.Parser{p}}).Collect(context.Background(), rc2, &schemas.Evidence{})
		if err == nil || !strings.Contains(err.Error(), "bad json") {
			t.Errorf("parser error: err = %v", err)
		}
	})
}

func TestReadTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out")
	if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		n    int64
		want string
	}{{4, "6789"}, {10, "0123456789"}, {50, "0123456789"}}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got, err := readTail(p, tt.n)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("readTail(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}
