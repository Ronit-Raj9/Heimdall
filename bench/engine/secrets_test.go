package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// orgSecrets is a SecretSource holding several orgs' secrets; it serves
// only its own org's, as the keystore-backed source does.
type orgSecrets struct {
	org   string
	byOrg map[string]map[string]string
	asked [][]string
}

func (s *orgSecrets) ResolveSecrets(_ context.Context, names []string) (map[string]string, error) {
	s.asked = append(s.asked, names)
	out := map[string]string{}
	for _, n := range names {
		if v, ok := s.byOrg[s.org][n]; ok {
			out[n] = v
		}
	}
	return out, nil
}

func secretFixture(t *testing.T) (*schemas.Manifest, *schemas.Suite) {
	t.Helper()
	task := &schemas.Task{ID: "t1", Dir: t.TempDir(), External: []schemas.ExternalAPI{{
		ID: "search", BaseURL: "https://api.search.test", AuthEnv: "SEARCH_KEY",
	}}}
	suite := &schemas.Suite{Root: t.TempDir(), Tasks: []*schemas.Task{task}}
	m := &schemas.Manifest{
		Experiment: schemas.Experiment{ID: "e1", External: schemas.ExternalLive},
		Arms: map[string]schemas.ArmManifest{
			"a": {Config: schemas.AgentConfig{Env: map[string]string{"TOKEN": "Bearer {{secret.AGENT_TOKEN}}"}}},
			"b": {Config: schemas.AgentConfig{}},
		},
		Slots: []schemas.Slot{{SlotID: "s1", Task: "t1", Arm: "a"}, {SlotID: "s2", Task: "t1", Arm: "b"}},
	}
	return m, suite
}

func TestCheckSecretsPlanError(t *testing.T) {
	m, suite := secretFixture(t)
	src := &orgSecrets{org: "org_a", byOrg: map[string]map[string]string{
		"org_a": {"SEARCH_KEY": "a-search"},
		"org_b": {"AGENT_TOKEN": "b-token"}, // another org's secret never satisfies org_a's plan
	}}
	p := &Planner{secrets: src}
	err := p.checkSecrets(t.Context(), m, suite)
	if err == nil || !strings.Contains(err.Error(), "org secret AGENT_TOKEN is not set") || !strings.Contains(err.Error(), "Settings > Secrets") {
		t.Fatalf("err %v", err)
	}
	if len(src.asked) != 1 || strings.Join(src.asked[0], ",") != "AGENT_TOKEN,SEARCH_KEY" {
		t.Fatalf("asked %v", src.asked)
	}
	src.byOrg["org_a"]["AGENT_TOKEN"] = "a-token"
	if err := p.checkSecrets(t.Context(), m, suite); err != nil {
		t.Fatal(err)
	}
	m.Experiment.External = schemas.ExternalReplay // a replay API reads no credential
	m.Arms = map[string]schemas.ArmManifest{"a": {}, "b": {}}
	src.asked = nil
	if err := (&Planner{secrets: src}).checkSecrets(t.Context(), m, suite); err != nil || len(src.asked) != 0 {
		t.Fatalf("err %v asked %v", err, src.asked)
	}
}

func secretRunState(m *schemas.Manifest, suite *schemas.Suite, arm string, in map[string]string) *runState {
	a := m.Arms[arm]
	return &runState{
		in: SlotInput{Manifest: m, Suite: suite, Secrets: in},
		rc: &schemas.RunContext{Manifest: m, Suite: suite, Task: suite.TaskByID("t1"), Agent: &a.Config},
	}
}

func TestResolveSecretsAndExpand(t *testing.T) {
	m, suite := secretFixture(t)
	src := &orgSecrets{org: "org_a", byOrg: map[string]map[string]string{
		"org_a": {"SEARCH_KEY": "a-search", "AGENT_TOKEN": "a-token", "UNRELATED": "nope"},
	}}
	p := &Pipeline{reg: &schemas.Registry{Secrets: src}}
	st := secretRunState(m, suite, "a", nil)
	if err := p.resolveSecrets(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	if len(st.rc.Secrets) != 2 || st.rc.Secrets["SEARCH_KEY"] != "a-search" || st.rc.Secrets["AGENT_TOKEN"] != "a-token" {
		t.Fatalf("secrets %v", st.rc.Secrets)
	}
	plan := &schemas.RunPlan{Env: map[string]string{"TOKEN": "Bearer {{secret.AGENT_TOKEN}}", "PLAIN": "x"}}
	if err := expandPlanSecrets(st.rc, plan); err != nil {
		t.Fatal(err)
	}
	if plan.Env["TOKEN"] != "Bearer a-token" || plan.Env["PLAIN"] != "x" {
		t.Fatalf("env %v", plan.Env)
	}
	if red := redactPlan(plan, st.rc.Secrets["AGENT_TOKEN"]); strings.Contains(red.Env["TOKEN"], "a-token") {
		t.Fatalf("plan.json keeps the secret: %v", red.Env)
	}

	// A remote worker's lease secrets are the only source: the runner's
	// registry source (its own keystore and environment) is never read.
	src.asked = nil
	st = secretRunState(m, suite, "a", map[string]string{"SEARCH_KEY": "lease-search"})
	err := p.resolveSecrets(t.Context(), st)
	if err == nil || !strings.Contains(err.Error(), "org secret AGENT_TOKEN is not set: add it in Settings > Secrets") {
		t.Fatalf("err %v", err)
	}
	if len(src.asked) != 0 {
		t.Fatalf("registry source read on a leased run: %v", src.asked)
	}

	// No source at all: the run keeps the bench's environment (rc.Secrets nil).
	st = secretRunState(m, suite, "b", nil)
	if err := (&Pipeline{reg: &schemas.Registry{}}).resolveSecrets(t.Context(), st); err != nil || st.rc.Secrets != nil {
		t.Fatalf("err %v secrets %v", err, st.rc.Secrets)
	}
}
