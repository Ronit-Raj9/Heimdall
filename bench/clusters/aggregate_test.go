package clusters

import (
	"testing"
	"time"
)

func TestScopeOf(t *testing.T) {
	tests := []struct {
		name string
		c    Cluster
		want string
	}{
		{name: "one run", c: Cluster{Count: 1, ConfigHashes: []string{"h1"}, Tasks: []string{"s/t1"}}, want: ScopeSingleRun},
		{name: "one config one task", c: Cluster{Count: 3, ConfigHashes: []string{"h1"}, Tasks: []string{"s/t1"}}, want: ScopeAgentAndTask},
		{name: "agent specific", c: Cluster{Count: 3, ConfigHashes: []string{"h1"}, Tasks: []string{"s/t1", "s/t2"}}, want: ScopeAgent},
		{name: "task specific", c: Cluster{Count: 3, ConfigHashes: []string{"h1", "h2"}, Tasks: []string{"s/t1"}}, want: ScopeTask},
		{name: "systemic", c: Cluster{Count: 3, ConfigHashes: []string{"h1", "h2"}, Tasks: []string{"s/t1", "s/t2"}}, want: ScopeSystemic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scopeOf(tt.c); got != tt.want {
				t.Fatalf("scope %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRelatedReason(t *testing.T) {
	failed := Layer{Kind: LayerFailedTests, key: "a"}
	errA := Layer{Kind: LayerError, key: "x"}
	errB := Layer{Kind: LayerError, key: "y"}
	gate := Layer{Kind: LayerGates, key: "g"}
	out := Layer{Kind: LayerOutcome, key: "fail"}
	tests := []struct {
		name string
		a, b []Layer
		want string
	}{
		{name: "error", a: []Layer{out, failed, errA}, b: []Layer{out, failed, errB}, want: "same failed set, different error fingerprint"},
		{name: "error present on one side", a: []Layer{out, failed, errA}, b: []Layer{out, failed}, want: "same failed set, different error fingerprint"},
		{name: "gates and error", a: []Layer{out, gate, failed, errA}, b: []Layer{out, failed, errB}, want: "same failed set, different gates, error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relatedReason(tt.a, tt.b); got != tt.want {
				t.Fatalf("reason %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigChange(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 1, h, 0, 0, 0, time.UTC) }
	run := func(id, hash, task string, h int) RunRef {
		return RunRef{RunID: id, Agent: "codex", ConfigHash: hash, Suite: "s", Task: task, At: at(h)}
	}
	cluster := []scored{{ref: run("r3", "h2", "t1", 5), agent: "codex", hash: "h2"}}
	tests := []struct {
		name string
		seen []RunRef
		runs []scored
		want *ConfigChange
	}{
		{name: "older config ran the task", seen: []RunRef{run("r1", "h1", "t1", 1), run("r3", "h2", "t1", 5)}, runs: cluster,
			want: &ConfigChange{Agent: "codex", FromHash: "h1", ToHash: "h2", At: at(5)}},
		{name: "older config never ran the task", seen: []RunRef{run("r1", "h1", "t9", 1), run("r3", "h2", "t1", 5)}, runs: cluster},
		{name: "no older config", seen: []RunRef{run("r3", "h2", "t1", 5), run("r4", "h1", "t1", 7)}, runs: cluster},
		{name: "newest older config wins", seen: []RunRef{run("r0", "h0", "t1", 0), run("r1", "h1", "t1", 1), run("r3", "h2", "t1", 5)}, runs: cluster,
			want: &ConfigChange{Agent: "codex", FromHash: "h1", ToHash: "h2", At: at(5)}},
		{name: "cluster spans configs", seen: []RunRef{run("r1", "h1", "t1", 1), run("r3", "h2", "t1", 5)},
			runs: append(cluster, scored{ref: run("r1", "h1", "t1", 1), agent: "codex", hash: "h1"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configChange(tt.runs, &scope{seen: tt.seen})
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Fatalf("config change %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestUniq(t *testing.T) {
	got := uniq([]string{"b", "", "a", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("uniq %v", got)
	}
}

// TestNewClusterOutcomeOnlyTitle (J13.groups): an outcome-only cluster says
// where its runs happened when they share a benchmark and a last tool.
func TestNewClusterOutcomeOnlyTitle(t *testing.T) {
	layers := Layers(Features{Outcome: "fail"})
	run := func(id, suite, tool string) scored {
		return scored{ref: RunRef{RunID: id, Suite: suite, Task: "t1", LastTool: tool, ConfigHash: "h1"}, layers: layers, f: Features{Outcome: "fail"}}
	}
	c := newCluster("c_1", []scored{run("r1", "swe-lite", "Bash"), run("r2", "swe-lite", "Bash"), run("r3", "swe-lite", "Edit")}, 3)
	if c.Title != "Failed with no error recorded on swe-lite in Bash" {
		t.Fatalf("title %q", c.Title)
	}
	c = newCluster("c_2", []scored{run("r1", "swe-lite", ""), run("r2", "other", "")}, 2)
	if c.Title != "Failed with no error recorded" {
		t.Fatalf("title over two benchmarks %q", c.Title)
	}
}
