package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestValidateTeam(t *testing.T) {
	two := []schemas.TeamMember{{ID: "lead"}, {ID: "worker-a"}}
	for _, tc := range []struct {
		name string
		team []schemas.TeamMember
		task *schemas.Task
		want string
	}{
		{"no team", nil, nil, ""},
		{"ok", two, &schemas.Task{}, ""},
		{"one member", two[:1], nil, "at least two"},
		{"bad id", []schemas.TeamMember{{ID: "Lead"}, {ID: "w"}}, nil, "must match"},
		{"repeated id", []schemas.TeamMember{{ID: "w"}, {ID: "w"}}, nil, "used twice"},
		{"negative budget", []schemas.TeamMember{{ID: "a", BudgetUSD: -1}, {ID: "b"}}, nil, "negative budget"},
		{"sessions", two, &schemas.Task{Sessions: []schemas.SessionSpec{{ID: "s1"}}}, "multi-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTeam(tc.team, tc.task)
			if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMemberConfig(t *testing.T) {
	arm := &schemas.AgentConfig{ID: "arm", Model: "m1", Argv: []string{"run"}, Env: map[string]string{"A": "1", "B": "2"},
		Team: []schemas.TeamMember{{ID: "x"}, {ID: "y"}}, Limits: &schemas.AgentLimits{MaxAgents: 3}}
	got := memberConfig(arm, schemas.TeamMember{ID: "x", Model: "m2", Env: map[string]string{"B": "3"}})
	if got.ID != "arm" || got.Model != "m2" || got.Env["A"] != "1" || got.Env["B"] != "3" || got.Team != nil || got.Limits != nil || got.Argv[0] != "run" {
		t.Errorf("member config = %+v", got)
	}
	if arm.Env["B"] != "2" || len(arm.Team) != 2 {
		t.Error("the arm config was changed")
	}
	own := &schemas.AgentConfig{ID: "codex", Model: "gpt"}
	if got := memberConfig(arm, schemas.TeamMember{ID: "y", Config: own, Argv: []string{"x"}}); got.ID != "codex" || got.Model != "gpt" || got.Argv[0] != "x" {
		t.Errorf("resolved member config = %+v", got)
	}
}

func TestTeamEnvAndTokens(t *testing.T) {
	team := []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "w"}}
	env := teamEnv(team[1], team, "abc")
	if env["HEIMDALL_AGENT_ID"] != "w" || env["HEIMDALL_AGENT_ROLE"] != "worker" || env["HEIMDALL_TEAM"] != "lead,w" ||
		env["HEIMDALL_MAILBOX_URL"] != "http://toolproxy:9200/m/abc/s/mailbox/" {
		t.Errorf("team env = %v", env)
	}
	a, err := newRouteToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newRouteToken()
	if len(a) != 32 || a == b {
		t.Errorf("tokens %q %q", a, b)
	}
}

func TestCombineResults(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	res := []schemas.MemberResult{
		{AgentID: "lead", Exec: &schemas.ExecResult{Started: t0.Add(time.Second), Ended: t0.Add(5 * time.Second), StdoutPath: "/run/agent.lead.stream.jsonl", StderrPath: "/run/agent.lead.stderr.txt"}},
		{AgentID: "w", Error: "create failed"},
		{AgentID: "v", Exec: &schemas.ExecResult{Started: t0, Ended: t0.Add(9 * time.Second), ExitCode: 3, TimedOut: true}},
	}
	x := combineResults(res)
	if !x.Started.Equal(t0) || !x.Ended.Equal(t0.Add(9*time.Second)) || x.ExitCode != 3 || !x.TimedOut ||
		x.StdoutPath != "/run/agent.stream.jsonl" || x.StderrPath != "/run/agent.stderr.txt" {
		t.Errorf("combined = %+v", x)
	}
	if res[0].Exec.StdoutPath != "/run/agent.lead.stream.jsonl" {
		t.Error("a member's result was changed")
	}
	if combineResults([]schemas.MemberResult{{AgentID: "a"}}) != nil {
		t.Error("no member ran: nil")
	}
}

// slotGateway reports a fixed number of key slots.
type slotGateway struct {
	schemas.Gateway
	n int
}

func (g slotGateway) KeySlots(string) int { return g.n }

func TestResolveTeam(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	member := "id: coder\nadapter: scripted\nimage: img:1\nscripted: {kind: noop}\nmodel: m-coder\nnotes: {project_file: AGENTS.md}\nbudgets: {wall_s: 60, cpus: 1, mem_gb: 1}\ngateway: {provider: anthropic, max_retries: 0}\n"
	if err := os.WriteFile(filepath.Join(root, "agents", "coder.yaml"), []byte(member), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := &schemas.Registry{Adapters: []schemas.AgentAdapter{fakeAdapter{part: part{name: "scripted"}}}, Gateway: slotGateway{n: 2}}
	p := NewPlanner(reg, root, nil)
	cfg := &schemas.AgentConfig{ID: "arm", Adapter: "scripted", Team: []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "coder", Agent: "agents/coder.yaml"}}}
	if err := p.resolveTeam(context.Background(), cfg, &schemas.Suite{}); err != nil {
		t.Fatal(err)
	}
	if c := cfg.Team[1].Config; c == nil || c.Model != "m-coder" || cfg.Team[0].Config != nil {
		t.Fatalf("resolved team = %+v", cfg.Team)
	}
	big := &schemas.AgentConfig{ID: "arm", Team: []schemas.TeamMember{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	if err := p.resolveTeam(context.Background(), big, &schemas.Suite{}); err == nil || !strings.Contains(err.Error(), "3 members need 3 gateway key slots, the gateway has 2") {
		t.Errorf("over the slots: %v", err)
	}
	missing := &schemas.AgentConfig{ID: "arm", Team: []schemas.TeamMember{{ID: "a"}, {ID: "b", Agent: "agents/nope.yaml"}}}
	if err := p.resolveTeam(context.Background(), missing, &schemas.Suite{}); err == nil || !strings.Contains(err.Error(), "team member b") {
		t.Errorf("missing member config: %v", err)
	}
}

// slotFault records which slots got a plan and which were cleared.
type slotFault struct{ set, cleared []string }

func (f *slotFault) SetPlan(_ context.Context, slot string, _ schemas.FaultPlan) error {
	f.set = append(f.set, slot)
	return nil
}
func (f *slotFault) ClearPlan(_ context.Context, slot string) error {
	f.cleared = append(f.cleared, slot)
	return nil
}
func (f *slotFault) Ledger(context.Context, string) ([]schemas.LedgerEntry, error) { return nil, nil }

// memberGateway mints member keys the way the bifrost gateway does: the
// lead on the run key and slot.
type memberGateway struct{ schemas.Gateway }

func (memberGateway) MintMemberKeys(_ context.Context, _ *schemas.RunContext, run *schemas.RunKey, members []schemas.TeamMember) ([]schemas.MemberKey, error) {
	var out []schemas.MemberKey
	for i, m := range members {
		k := schemas.MemberKey{AgentID: m.ID, ID: "vk-" + m.ID, KeySlot: "slot-" + m.ID}
		if i == 0 {
			k.ID, k.KeySlot = run.ID, run.KeySlot
		}
		out = append(out, k)
	}
	return out, nil
}

// TestMintTeamSlots: every non-lead member slot gets the run's plan and is
// cleared at cleanup; the run slot is left to bindFaultPlan and cleanup.
func TestMintTeamSlots(t *testing.T) {
	f := &slotFault{}
	p := &Pipeline{reg: &schemas.Registry{Gateway: memberGateway{}, Fault: f}, faultMode: faultModeNone}
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Key: &schemas.RunKey{ID: "vk-run", KeySlot: "slot-run"},
		Agent: &schemas.AgentConfig{Team: []schemas.TeamMember{{ID: "lead", Role: "lead"}, {ID: "a"}, {ID: "b"}}}}
	st := &runState{rc: rc}
	if err := p.mintTeam(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if len(rc.Key.Members) != 3 || strings.Join(f.set, ",") != "slot-a,slot-b" {
		t.Errorf("members %d, plans bound to %v", len(rc.Key.Members), f.set)
	}
	if err := p.clearTeamPlans(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.cleared, ",") != "slot-a,slot-b" {
		t.Errorf("cleared %v", f.cleared)
	}
	single := &runState{rc: &schemas.RunContext{Run: &schemas.Run{RunID: "r_2"}, Key: &schemas.RunKey{}, Agent: &schemas.AgentConfig{}}}
	if err := p.mintTeam(context.Background(), single); err != nil || single.rc.Key.Members != nil {
		t.Errorf("single-agent run: %v %+v", err, single.rc.Key)
	}
}
