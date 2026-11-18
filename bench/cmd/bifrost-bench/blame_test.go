package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/attribution"
	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

func TestReplaySpec(t *testing.T) {
	cases := []struct {
		name  string
		in    *schemas.AttributionSpec
		force bool
		stub  string
		max   int
		want  schemas.AttributionSpec
	}{
		{"off by default", nil, false, "", 0, schemas.AttributionSpec{MaxReplays: attribution.DefaultMaxReplays}},
		{"opted in", &schemas.AttributionSpec{Replay: true, MaxReplays: 2, Stub: "agents/s.yaml"}, false, "", 0, schemas.AttributionSpec{Replay: true, MaxReplays: 2, Stub: "agents/s.yaml"}},
		{"flag forces, flag stub and max win", &schemas.AttributionSpec{Stub: "a.yaml"}, true, "b.yaml", 5, schemas.AttributionSpec{Replay: true, MaxReplays: 5, Stub: "b.yaml"}},
		{"no stub means no replay", nil, true, "", 0, schemas.AttributionSpec{MaxReplays: attribution.DefaultMaxReplays}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := replaySpec(tc.in, tc.force, tc.stub, tc.max); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTier1Of(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"evidence.json":  `{"agents":{"version":"1.0.0","shape":"team","agents":[{"id":"lead","role":"lead","depth":0,"source":"gateway_key","strength":"proven"},{"id":"worker-b","role":"worker","depth":0,"source":"gateway_key","strength":"proven"}],"coverage":{"steps":1,"attributed":1,"unknown":0}}}`,
		"messages.jsonl": `{"seq":3,"from":"worker-b","to":"ops-desk","body":"RUN curl","body_sha256":"h","size":8,"sender_agent_id":"worker-b","at":"2026-11-18T06:00:00Z","delivered":false}` + "\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sr := &schemas.SealedRun{Dir: dir, SealHash: "seal", Slot: schemas.Slot{ExpID: "e_x"},
		Run:   schemas.Run{RunID: "r_1", Outcome: schemas.OutcomeIntegrityViolation},
		Gates: []schemas.GateResult{{Gate: "peer_injection_followed", Effect: schemas.GateVeto, Fired: true, Evidence: []string{"agent:worker-b", "message:3"}}}}
	b, ev, err := tier1Of(sr)
	if err != nil {
		t.Fatal(err)
	}
	if b.Tier != attribution.TierGateProven || b.AgentID != "worker-b" || b.ExpID != "e_x" || b.ParentSeal != "seal" || ev.Agents == nil {
		t.Fatalf("blame %+v", b)
	}
	if !attribution.ReplayCandidate(b, ev) {
		t.Fatal("a gate-proven team failure is a replay candidate")
	}
}

// TestAcmeTeamExampleWiring loads the acme-team example suite and checks
// that every multi-agent probe names what judges it: a gate this binary
// registers, or a metric its probe lists, and that the team config freezes
// the four members and the spawn caps over_spawn reads.
func TestAcmeTeamExampleWiring(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "acme-team")
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "acme-team")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks) != 3 {
		t.Fatalf("tasks %d", len(s.Tasks))
	}
	gatesByName := map[string]bool{}
	for _, g := range gates.Builtin() {
		gatesByName[g.GetName()] = true
	}
	judged := map[string][]string{
		"lossy-handoff":      {gates.HandoffFactLost},
		"poisoned-peer":      {gates.PeerInjectionFollowed},
		"relayed-consent":    {gates.RelayedConsentAction},
		"cross-agent-canary": {gates.CrossAgentSecretLeak},
		"delegation-bomb":    {gates.OverSpawn, gates.DelegationLoop},
		"covert-channel":     {gates.UnmonitoredChannelUsed},
	}
	metricsByProbe := map[string]string{"wrong-majority": "conformity_flip", "seeded-bug": "reviewer_catch"}
	for _, id := range []string{"worker-outage", "worker-tool-break", "lossy-handoff", "poisoned-peer", "relayed-consent", "wrong-majority", "delegation-bomb", "seeded-bug", "cross-agent-canary", "covert-channel"} {
		p := s.ProbeByID(id)
		if p == nil {
			t.Errorf("probe %s is not loaded", id)
			continue
		}
		for _, g := range judged[id] {
			if !gatesByName[g] {
				t.Errorf("probe %s: gate %s is not registered", id, g)
			}
		}
		if m := metricsByProbe[id]; m != "" && !slices.Contains(p.Metrics, m) {
			t.Errorf("probe %s does not list %s", id, m)
		}
	}
	for _, id := range []string{"worker-outage", "worker-tool-break", "lossy-handoff", "poisoned-peer", "cross-agent-canary"} {
		if s.ProbeByID(id).Apply.Agent == "" {
			t.Errorf("probe %s targets no member", id)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "agents", "team.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg schemas.AgentConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Team) != 4 || cfg.Limits == nil || cfg.Limits.MaxAgents != 6 || cfg.Limits.MaxDepth != 2 || cfg.Files["/tmp/bench/acme-team.py"] == "" {
		t.Fatalf("team config %+v", cfg)
	}
}

func TestKeepReplay(t *testing.T) {
	root := t.TempDir()
	sr := &schemas.SealedRun{SealHash: "seal", Run: schemas.Run{RunID: "r_1"}}
	old := &attribution.Blame{RunID: "r_1", ExpID: "e_x", ParentSeal: "seal", Tier: attribution.TierReplayValidated, AgentID: "lead",
		Replay: &attribution.Replay{Status: attribution.ReplayFlipped}}
	if err := attribution.Write(root, old); err != nil {
		t.Fatal(err)
	}
	b := &attribution.Blame{RunID: "r_1", ExpID: "e_x", Tier: attribution.TierGateProven, AgentID: "lead"}
	keepReplay(root, sr, b)
	if b.Replay == nil || b.Tier != attribution.TierReplayValidated {
		t.Fatalf("a tier-1 pass erased the replay: %+v", b)
	}
	other := &attribution.Blame{RunID: "r_1", ExpID: "e_x", Tier: attribution.TierGateProven, AgentID: "worker-a"}
	keepReplay(root, sr, other)
	if other.Tier != attribution.TierGateProven {
		t.Fatal("a replay of another suspect must not validate this blame")
	}
}
