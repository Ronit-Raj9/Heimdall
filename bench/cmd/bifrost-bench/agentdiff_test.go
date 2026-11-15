package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/agentdiff"
)

// agentDiffRecorded is the read-only recorded bundle.
const agentDiffRecorded = "../../testdata/recorded"

func TestRunAgentDiffRecorded(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		var out bytes.Buffer
		code := runAgentDiff([]string{"-runs", agentDiffRecorded, "-a", "e_pilot_mixed_nc93:A", "-b", "e_pilot_mixed_nc93:B", "-condition", "empty-test-run"}, &out)
		if code != exitOK {
			t.Fatalf("exit %d", code)
		}
		for _, want := range []string{
			"agent diff  coding-core@1.0.0  condition empty-test-run",
			"1 task flipped to pass and 0 flipped to fail (net +1)",
			"Cannot tell whether B is better or worse",
			"~ budgets.cpus: 2 -> 1",
			`- env.BIFROST_API_KEY: "[redacted]" -> (unset)`,
			"--- a/home/agent/.codex/config.toml",
			"flipped_to_pass   t05",
			"pair p_",
			"cost_usd",
		} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("output lacks %q:\n%s", want, out.String())
			}
		}
	})
	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		code := runAgentDiff([]string{"-runs", agentDiffRecorded, "-a", "e_pilot_mixed_nc93:A", "-b", "e_pilot_mixed_nc93:B", "-json"}, &out)
		if code != exitOK {
			t.Fatalf("exit %d", code)
		}
		var r agentdiff.Report
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Condition != "clean" || r.Outcome.CommonTasks != 1 || r.Outcome.Counts.UnchangedPass != 1 {
			t.Fatalf("report %+v", r.Outcome)
		}
	})
	t.Run("no common task is a usage error", func(t *testing.T) {
		var out bytes.Buffer
		code := runAgentDiff([]string{"-runs", agentDiffRecorded, "-a", "e_smoke_sdoj:A", "-b", "e_smoke_sdoj:B"}, &out)
		if code != exitUsage || out.Len() != 0 {
			t.Fatalf("exit %d, output %q", code, out.String())
		}
	})
	t.Run("bad flags", func(t *testing.T) {
		if code := runAgentDiff([]string{"-runs", agentDiffRecorded, "-a", "x", "-b", "y", "-condition", "a b"}, &bytes.Buffer{}); code != exitUsage {
			t.Fatalf("exit %d", code)
		}
	})
}

func TestDiffNumber(t *testing.T) {
	cases := []struct {
		v      float64
		signed bool
		want   string
	}{
		{109612, false, "109612"},
		{-65700.4, true, "-65700"},
		{0.0212534, false, "0.02125"},
		{0.0004536, true, "+0.0004536"},
		{8, true, "+8"},
	}
	for _, c := range cases {
		if got := diffNumber(c.v, c.signed); got != c.want {
			t.Errorf("diffNumber(%v, %v) = %q, want %q", c.v, c.signed, got, c.want)
		}
	}
}
