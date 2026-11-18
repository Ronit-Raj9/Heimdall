//go:build integration

package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// teamFixture is the team suite (suite/testdata/team): one task a team
// passes when both workers wrote their files and the lead collected their
// replies, and the two scripted multi-agent controls.
const teamFixture = "../suite/testdata/team"

// teamProxyImage is the tool proxy image with member routes. Build it from
// this tree before the test (docs/multi-agent.md): the stock dev image may
// predate them. The test skips when it is missing.
const teamProxyImage = "bench/bifrost-bench:hd-ma1"

// teamGateway is e2eGateway plus member keys: each member on its own slot.
type teamGateway struct {
	e2eGateway
	mu     sync.Mutex
	minted []schemas.MemberKey
}

// MintKey mints a key for the arm's own model, as the bifrost gateway does.
func (g *teamGateway) MintKey(ctx context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	k, err := g.e2eGateway.MintKey(ctx, rc)
	if err == nil && rc.Agent != nil {
		k.Model = rc.Agent.Model
	}
	return k, err
}

func (g *teamGateway) MintMemberKeys(_ context.Context, rc *schemas.RunContext, run *schemas.RunKey, members []schemas.TeamMember) ([]schemas.MemberKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []schemas.MemberKey
	for i, m := range members {
		k := schemas.MemberKey{AgentID: m.ID, ID: "vk-" + rc.Run.RunID + "-" + m.ID, Name: rc.Run.RunID + "--" + m.ID,
			Value: "hd-ma1-secret-" + m.ID, KeySlot: "slot-m" + string(rune('1'+i)), BudgetUSD: m.BudgetUSD}
		if m.Role == schemas.AgentRoleLead { // the lead runs on the run key and slot, as the bifrost gateway does
			k.ID, k.Name, k.KeySlot = run.ID, run.Name, run.KeySlot
		}
		out = append(out, k)
	}
	g.minted = append(g.minted, out...)
	return out, nil
}

// TestTeamEndToEnd runs a three-member team (team-control: a lead and two
// workers, each in its own container with its own key, talking through the
// mailbox) and the in-process control (subagents-control) through the full
// pipeline with real docker, and checks the per-member keys, the mailbox
// rows in the tool ledger, the sealed team.json, agents.json and
// messages.jsonl, the multi-agent metrics and that nothing is left behind.
func TestTeamEndToEnd(t *testing.T) {
	requireImages(t)
	if err := exec.Command("docker", "image", "inspect", teamProxyImage).Run(); err != nil {
		t.Skipf("tool proxy image %s is not present locally", teamProxyImage)
	}
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	copyTree(t, teamFixture, root)
	st, err := store.NewFSStore(filepath.Join(root, "runs"), nil)
	if err != nil {
		t.Fatal(err)
	}
	gw := &teamGateway{}
	docker := utils.Docker{}
	reg := &schemas.Registry{
		Loaders:      suite.Builtin(root),
		Environments: environments.Builtin(environments.DockerWorkspaceConfig{HelperImage: e2eBaseImage, ToolProxyImage: teamProxyImage}),
		Adapters:     adapters.Builtin(adapters.CommandConfig{BenchRoot: root}),
		Graders:      graders.Builtin(graders.HiddenTestsConfig{}),
		Evidence:     evidence.Builtin(evidence.Deps{Gateway: gw, Docker: docker, HelperImage: e2eBaseImage}),
		Metrics:      metrics.Builtin(),
		Gateway:      gw,
		Store:        st,
	}
	s, err := engine.LoadSuite(context.Background(), reg, "hd-ma1-team")
	if err != nil {
		t.Fatalf("load suite: %v", err)
	}
	exp := &schemas.Experiment{
		Name: "hd-ma1-team", Suite: "hd-ma1-team", Smoke: true, Seed: 1, Concurrency: 1,
		Arms: map[string]schemas.ArmSpec{"A": {Label: "team", Agent: "agents/team-control.yaml"},
			"B": {Label: "subagents", Agent: "agents/subagents-control.yaml"}},
		Conditions: []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: []string{"split"}}, Trials: 1}},
		Primary:    schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	m, err := engine.NewPlanner(reg, root, nil).Plan(ctx, exp)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	p := engine.NewPipeline(reg, root, nil)
	for i := range m.Slots {
		slot := &m.Slots[i]
		res, err := p.RunSlot(ctx, engine.SlotInput{Manifest: m, Suite: s, Slot: slot, Attempt: 1})
		if err != nil {
			t.Fatalf("slot %s: %v", slot.SlotID, err)
		}
		if err := st.Verify(ctx, m.ExpID, res.Run.RunID); err != nil {
			t.Errorf("%s: seal does not verify: %v", res.Run.RunID, err)
		}
		switch slot.Arm {
		case "A":
			if res.Run.Outcome != schemas.OutcomePass {
				t.Errorf("team run: outcome %s, failures %+v", res.Run.Outcome, res.Run.Failures)
			}
			checkTeamRun(t, res.Dir, res.Metrics)
		case "B":
			checkSubagentsRun(t, res.Dir, res.Metrics)
		}
		checkNothingLeft(t, res.Run.RunID)
	}
	ids, slots := map[string]bool{}, map[string]bool{}
	for _, k := range gw.minted {
		ids[k.ID], slots[k.KeySlot] = true, true
	}
	if len(gw.minted) != 3 || len(ids) != 3 || len(slots) != 3 {
		t.Errorf("member keys: %d minted, %d distinct ids, %d distinct slots; want 3 each", len(gw.minted), len(ids), len(slots))
	}
}

// checkTeamRun checks the sealed files of the team run.
func checkTeamRun(t *testing.T, dir string, got map[string]schemas.Value) {
	t.Helper()
	var team schemas.TeamRecord
	readJSONFile(t, filepath.Join(dir, schemas.TeamFile), &team)
	if len(team.Results) != 3 || len(team.Keys) != 3 {
		t.Fatalf("team.json: %d results, %d keys; want 3 each", len(team.Results), len(team.Keys))
	}
	for _, r := range team.Results {
		if r.Exec == nil || r.Exec.ExitCode != 0 {
			t.Errorf("member %s: %+v %s", r.AgentID, r.Exec, r.Error)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, schemas.TeamFile)); err == nil && strings.Contains(string(b), "hd-ma1-secret-") {
		t.Errorf("team.json holds a member key value")
	}
	ledger, err := os.ReadFile(filepath.Join(dir, "tools.jsonl"))
	if err != nil {
		t.Fatalf("tools.jsonl: %v", err)
	}
	sends, stamped := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(ledger)), "\n") {
		var c schemas.ToolCall
		if json.Unmarshal([]byte(line), &c) != nil || c.Server != schemas.MailboxServer || c.Tool != schemas.MailboxSend {
			continue
		}
		sends++
		if c.AgentID != "" {
			stamped++
		}
	}
	if sends < 4 || stamped != sends {
		t.Errorf("tools.jsonl: %d mailbox sends, %d with a member id; want >= 4, all stamped", sends, stamped)
	}
	for _, f := range []string{"state/mailbox/messages.jsonl", schemas.MessagesFile, schemas.AgentsFile, "agent.lead.stream.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("team run: %s missing: %v", f, err)
		}
	}
	var tree schemas.AgentTree
	readJSONFile(t, filepath.Join(dir, schemas.AgentsFile), &tree)
	messages := 0
	for _, e := range tree.Edges {
		if e.Kind == schemas.AgentEdgeMessage && e.Strength == schemas.AgentStrengthProven {
			messages++
		}
	}
	t.Logf("team run: agents %d, edges %d, coverage %+v, notes %v", len(tree.Agents), len(tree.Edges), tree.Coverage, tree.Notes)
	if tree.Shape != schemas.AgentShapeTeam || len(tree.Agents) != 3 || messages < 4 {
		t.Errorf("agents.json: shape %s, %d agents, %d proven message edges", tree.Shape, len(tree.Agents), messages)
	}
	if v := got["agents_spawned"]; v.V == nil || *v.V != 2 {
		t.Errorf("agents_spawned = %+v, want 2", v)
	}
	if v := got["messages"]; v.V == nil || *v.V < 4 {
		t.Errorf("messages = %+v, want >= 4", v)
	}
}

// checkSubagentsRun checks the in-process control's agent tree: a planner
// under the main agent, two workers that ran at the same time and a
// reviewer, all from its native self-report.
func checkSubagentsRun(t *testing.T, dir string, got map[string]schemas.Value) {
	t.Helper()
	var tree schemas.AgentTree
	readJSONFile(t, filepath.Join(dir, schemas.AgentsFile), &tree)
	t.Logf("subagents run: agents %d, edges %d, coverage %+v", len(tree.Agents), len(tree.Edges), tree.Coverage)
	names := map[string]schemas.AgentNode{}
	for _, a := range tree.Agents {
		names[a.ID] = a
	}
	for _, id := range []string{schemas.AgentRoot, "planner", "worker-a", "worker-b", "reviewer"} {
		if _, ok := names[id]; !ok {
			t.Errorf("agents.json: no agent %s in %+v", id, tree.Agents)
		}
	}
	if names["worker-a"].ParentID != "planner" || names["worker-a"].Depth != 2 || names["worker-a"].Source != schemas.AgentSourceSelfReport {
		t.Errorf("worker-a: %+v", names["worker-a"])
	}
	if v := got["serial_collapse"]; v.V == nil || *v.V != 0 {
		t.Errorf("serial_collapse = %+v, want 0 (the workers overlap)", v)
	}
	if v := got["handoffs"]; v.V == nil || *v.V != 1 {
		t.Errorf("handoffs = %+v, want 1", v)
	}
}

func readJSONFile(t *testing.T, p string, v any) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
}

// copyTree copies a fixture folder into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
