package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestMiniSWEAgentRunTraceComplete runs the shipped mini-swe-agent config
// through the agent_stream source with the built-in parsers. A whole
// trajectory block gives a complete native trace, so the engine's contract
// (engine/stages.go collect) adds no complete_agent_trace and the proof can
// be complete. A block cut by the output cap gives a partial trace.
func TestMiniSWEAgentRunTraceComplete(t *testing.T) {
	cfgRaw, err := os.ReadFile(filepath.Join("..", "agents", "mini-swe-agent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var agent schemas.AgentConfig
	if err := yaml.Unmarshal(cfgRaw, &agent); err != nil {
		t.Fatal(err)
	}
	sample, err := os.ReadFile(filepath.Join("..", "adapters", "testdata", "mini-swe-agent-2.4.6-sample.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	cut := string(sample)[:strings.Index(string(sample), adapters.MiniSWETrajectoryEnd)-400]
	for _, tc := range []struct {
		name      string
		stdout    string
		truncated bool
		want      schemas.TraceQuality
	}{
		{"whole block", string(sample), false, schemas.TraceQualityComplete},
		{"block cut by the output cap", cut, true, schemas.TraceQualityPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := testRC(t)
			a := agent
			rc.Agent = &a
			p := filepath.Join(rc.Dir, "agent.stream.jsonl")
			if err := os.WriteFile(p, []byte(tc.stdout), 0o644); err != nil {
				t.Fatal(err)
			}
			rc.Exec = &schemas.ExecResult{StdoutPath: p, Truncated: tc.truncated}
			ev := &schemas.Evidence{}
			if err := newAgentStreamSource(Deps{Parsers: adapters.BuiltinParsers()}).Collect(context.Background(), rc, ev); err != nil {
				t.Fatal(err)
			}
			tr := ev.AgentTrace
			if tr == nil || tr.Quality != tc.want || tr.Source != schemas.TraceSourceNative || tr.Parser != adapters.ParserMiniSWE {
				t.Fatalf("trace = %+v", tr)
			}
			if rc.Run.Agent.TraceQuality != tc.want || len(ev.Events) == 0 {
				t.Fatalf("run trace %s, %d events", rc.Run.Agent.TraceQuality, len(ev.Events))
			}
			if tc.want == schemas.TraceQualityComplete && (ev.Claim == nil || !ev.Claim.Made) {
				t.Errorf("claim = %+v", ev.Claim)
			}
		})
	}
}
