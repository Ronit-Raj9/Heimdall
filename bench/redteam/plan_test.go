package redteam

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	p := testPlan()
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	// 4 categories x 4 channels... prompt leak chat only (1), goal hijack 3, three others 4 each: 16 cells x 3 strategies.
	if p.Suite != DefaultSuite || p.MaxAttempts != DefaultMaxAttempts || len(p.Cells()) != 48 {
		t.Fatalf("plan = %+v, cells %d", p, len(p.Cells()))
	}
	for _, tc := range []struct {
		name string
		p    Plan
		want string
	}{
		{"no model", Plan{AttackerProvider: "openai"}, "attacker_model"},
		{"bad category", Plan{Categories: []string{"x"}, AttackerProvider: "o", AttackerModel: "m"}, "category"},
		{"bad strategy", Plan{Strategies: []string{"gcg"}, AttackerProvider: "o", AttackerModel: "m"}, "strategy"},
		{"nothing fits", Plan{Categories: []string{CategoryGoalHijack}, Channels: []string{ChannelChat}, AttackerProvider: "o", AttackerModel: "m"}, "not offered"},
		{"regressions id", Plan{Suite: RegressionsSuite, AttackerProvider: "o", AttackerModel: "m"}, "suite"},
		{"attempts", Plan{MaxAttempts: 99, AttackerProvider: "o", AttackerModel: "m"}, "max_attempts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.Normalize(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseTaskID(t *testing.T) {
	for _, c := range testPlanCells() {
		got, ok := ParseTaskID(TaskID(c))
		if !ok || got != c {
			t.Fatalf("%s -> %+v %v", TaskID(c), got, ok)
		}
		reg := RegressionID(c, strings.Repeat("ab", 32))
		if got, ok := ParseTaskID(reg); !ok || got != c || !IsRegressionTask(reg) {
			t.Fatalf("%s -> %+v", reg, got)
		}
	}
	for _, bad := range []string{"t1", "rt.x.pair.chat", "rt.prompt_leak.pair", "reg.prompt_leak.pair.chat"} {
		if _, ok := ParseTaskID(bad); ok {
			t.Errorf("%s parsed", bad)
		}
	}
}
