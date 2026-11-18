package environments

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
)

// fakeSvcGateway is a schemas.Gateway that mints predictable keys and
// answers FetchRequests with fixed rows.
type fakeSvcGateway struct {
	mu      sync.Mutex
	minted  []*schemas.RunContext
	revoked []string
	rows    []schemas.GatewayRequest
	direct  bool
}

func (g *fakeSvcGateway) GetName() string    { return "fake" }
func (g *fakeSvcGateway) GetVersion() string { return "0" }
func (g *fakeSvcGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return &schemas.GatewayManifest{}, nil
}
func (g *fakeSvcGateway) MintKey(_ context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.minted = append(g.minted, rc)
	k := &schemas.RunKey{ID: "vk-" + rc.Run.RunID, Name: rc.Run.RunID, Value: "sk-secret-" + rc.Run.RunID, KeySlot: "fp-slot-2",
		Provider: rc.Agent.Gateway.Provider, Model: rc.Agent.Model, SlotAuth: "placeholder"}
	if g.direct {
		k.SlotAuth = ""
	}
	return k, nil
}
func (g *fakeSvcGateway) RevokeKey(_ context.Context, k *schemas.RunKey) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.revoked = append(g.revoked, k.ID)
	return nil
}
func (g *fakeSvcGateway) RevokeStale(context.Context, map[string]bool) (int, error) { return 0, nil }
func (g *fakeSvcGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return &schemas.RequestLog{Complete: true, Requests: append([]schemas.GatewayRequest(nil), g.rows...)}, nil
}

// fakeSvcFault records plans.
type fakeSvcFault struct {
	mu      sync.Mutex
	plans   []schemas.FaultPlan
	cleared []string
}

func (f *fakeSvcFault) SetPlan(_ context.Context, slot string, p schemas.FaultPlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plans = append(f.plans, p)
	return nil
}
func (f *fakeSvcFault) ClearPlan(_ context.Context, slot string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, slot)
	return nil
}
func (f *fakeSvcFault) Ledger(context.Context, string) ([]schemas.LedgerEntry, error) {
	return nil, nil
}

func userRC(t *testing.T, runID string, sim *schemas.SimulatorSpec) *schemas.RunContext {
	rc := serviceRC(t,
		schemas.ServiceSpec{ID: "desk", Role: schemas.ServiceRoleMCP, Image: "desk:1", Port: 8000},
		schemas.ServiceSpec{ID: "customer", Role: schemas.ServiceRoleUser, Image: "sim:1", Port: 8000, MCP: &schemas.MCPEndpoint{Path: "/mcp"},
			Export: []schemas.StateExport{{ID: "transcript.json", Path: "/state/transcript.json"}}},
	)
	rc.Run.RunID = runID
	rc.Suite.Simulator = sim
	return rc
}

func llmSim() *schemas.SimulatorSpec {
	return &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: "claude-sim", Seed: 3}
}

func price(model string, tok schemas.Tokens) (float64, bool) {
	if model != "claude-sim" {
		return 0, false
	}
	return float64(tok.Fresh)*1e-6 + float64(tok.Out)*5e-6, true
}

func TestServiceGatewayKeyLifecycle(t *testing.T) {
	docker, logPath := fakeDocker(t)
	gw := &fakeSvcGateway{rows: []schemas.GatewayRequest{
		{ID: "a", Status: "success", Kind: "message", Tokens: &schemas.Tokens{Fresh: 1000, Out: 100}},
		{ID: "b", Status: "success", Kind: "message", Tokens: &schemas.Tokens{Fresh: 2000, Out: 100}},
	}}
	fault := &fakeSvcFault{}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "bench-evalnet",
		ServiceGateway: ServiceGatewayConfig{Gateway: gw, AgentURL: "http://bifrost:8080/", Fault: fault, Price: price, PriceRef: "snap"}})
	rc := userRC(t, "r_gw1", llmSim())
	t.Cleanup(func() { e.revokeServiceKeys(context.Background(), "r_gw1") })
	if _, _, err := e.StartServices(t.Context(), rc); err != nil {
		t.Fatalf("start services: %v", err)
	}
	if len(gw.minted) != 1 {
		t.Fatalf("minted %d keys, want 1 (only the user service)", len(gw.minted))
	}
	m := gw.minted[0]
	if !strings.HasPrefix(m.Run.RunID, "r_gw1s") || m.Agent.Model != "claude-sim" || m.Agent.Gateway.Provider != "anthropic" {
		t.Fatalf("mint context %+v %+v", m.Run, m.Agent)
	}
	all := calls(t, logPath)
	create := callWith(all, "create --name bench-r_gw1-svc-customer")
	if create == "" || !strings.Contains(create, "--env-file") {
		t.Fatalf("user service not created with an env file: %q", create)
	}
	for _, c := range all {
		if strings.Contains(c, "sk-secret") {
			t.Fatalf("key on a docker command line: %s", c)
		}
	}
	if !strings.Contains(create, "-e "+simuser.EnvMode+"=llm") || !strings.Contains(create, simuser.EnvPromptSHA256+"=") {
		t.Errorf("simulator env missing: %s", create)
	}
	if strings.Contains(callWith(all, "create --name bench-r_gw1-svc-desk"), "--env-file") {
		t.Error("desk service got gateway access")
	}
	if callWith(all, "network connect bench-evalnet bench-r_gw1-svc-customer") == "" {
		t.Errorf("user service not on the evaluation network:\n%s", strings.Join(all, "\n"))
	}
	if callWith(all, "network connect bench-evalnet bench-r_gw1-svc-desk") != "" {
		t.Error("desk service joined the evaluation network")
	}
	if callWith(all, "volume create", "bifrost-bench.run="+m.Run.RunID, "bench-svckey-"+m.Run.RunID) == "" {
		t.Errorf("no keeper volume labelled with the key's name:\n%s", strings.Join(all, "\n"))
	}
	if len(fault.plans) != 1 || fault.plans[0].Mode != "none" || fault.plans[0].RunID != m.Run.RunID {
		t.Fatalf("plans %+v", fault.plans)
	}
	if got := liveServiceKeys("r_gw1"); len(got) != 1 || got[0] != "customer" {
		t.Fatalf("live keys %v", got)
	}

	files, err := e.ExportState(t.Context(), rc)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var gwFile *schemas.StateFile
	for i := range files {
		if files[i].ID == ServiceGatewayExport {
			gwFile = &files[i]
		}
	}
	if gwFile == nil || gwFile.Service != "customer" || gwFile.Gateway == nil {
		t.Fatalf("no gateway state file in %+v", files)
	}
	u := gwFile.Gateway
	if u.Requests != 2 || u.Tokens.Fresh != 3000 || u.USD.State != schemas.ValueObserved || u.FaultMode != "" || u.PriceRef != "snap" {
		t.Fatalf("usage %+v", u)
	}
	if v := *u.USD.V; v < 0.003999 || v > 0.004001 {
		t.Fatalf("usd %v, want 0.004", v)
	}
	raw, err := os.ReadFile(filepath.Join(rc.Dir, gwFile.Path))
	if err != nil {
		t.Fatal(err)
	}
	var doc serviceGatewayDoc
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Requests) != 2 {
		t.Fatalf("gateway file: %v %s", err, raw)
	}
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatal("key written to the run folder")
	}
	if len(gw.revoked) != 1 || len(fault.cleared) != 1 || len(liveServiceKeys("r_gw1")) != 0 {
		t.Fatalf("revoked %v cleared %v live %v", gw.revoked, fault.cleared, liveServiceKeys("r_gw1"))
	}
	if callWith(calls(t, logPath), "volume rm -f bench-svckey-"+m.Run.RunID) == "" {
		t.Error("keeper volume not removed at revoke")
	}
	if err := e.Destroy(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	if len(gw.revoked) != 1 {
		t.Fatalf("revoked twice: %v", gw.revoked)
	}
}

func TestServiceGatewaySmokeIsScripted(t *testing.T) {
	docker, _ := fakeDocker(t)
	gw := &fakeSvcGateway{rows: []schemas.GatewayRequest{{ID: "a", Status: "success", Kind: "message", Tokens: &schemas.Tokens{Fresh: 50, Out: 32}}}}
	fault := &fakeSvcFault{}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "bench-evalnet",
		ServiceGateway: ServiceGatewayConfig{Gateway: gw, AgentURL: "http://bifrost:8080", Fault: fault, Price: price}})
	rc := userRC(t, "r_gw2", llmSim())
	rc.Experiment = &schemas.Experiment{Smoke: true}
	if _, _, err := e.StartServices(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	if fault.plans[0].Mode != "scripted" {
		t.Fatalf("smoke plan %+v", fault.plans)
	}
	files, err := e.ExportState(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	u := files[len(files)-1].Gateway
	if u == nil || u.FaultMode != "scripted" || u.USD.State != schemas.ValueObserved || *u.USD.V != 0 {
		t.Fatalf("smoke usage %+v", u)
	}

	t.Run("direct slot refused", func(t *testing.T) {
		docker, _ := fakeDocker(t)
		gw := &fakeSvcGateway{direct: true}
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "bench-evalnet",
			ServiceGateway: ServiceGatewayConfig{Gateway: gw, AgentURL: "http://bifrost:8080", Fault: &fakeSvcFault{}}})
		rc := userRC(t, "r_gw3", llmSim())
		rc.Experiment = &schemas.Experiment{Smoke: true}
		_, _, err := e.StartServices(t.Context(), rc)
		if err == nil || !strings.Contains(err.Error(), "direct") {
			t.Fatalf("want a direct slot refusal, got %v", err)
		}
		if err := e.Destroy(t.Context(), rc); err != nil {
			t.Fatal(err)
		}
		if len(gw.revoked) != 1 {
			t.Fatalf("refused key not revoked: %v", gw.revoked)
		}
	})
}

func TestServiceGatewayNotNeededOrNotWired(t *testing.T) {
	t.Run("scripted user mints nothing", func(t *testing.T) {
		docker, logPath := fakeDocker(t)
		gw := &fakeSvcGateway{}
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "bench-evalnet",
			ServiceGateway: ServiceGatewayConfig{Gateway: gw, AgentURL: "http://bifrost:8080"}})
		rc := userRC(t, "r_gw4", &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted})
		if _, _, err := e.StartServices(t.Context(), rc); err != nil {
			t.Fatal(err)
		}
		if len(gw.minted) != 0 || callWith(calls(t, logPath), "network connect bench-evalnet") != "" {
			t.Fatalf("scripted user got gateway access: minted %d", len(gw.minted))
		}
		if c := callWith(calls(t, logPath), "create --name bench-r_gw4-svc-customer"); !strings.Contains(c, simuser.EnvMode+"=scripted") {
			t.Fatalf("scripted env missing: %s", c)
		}
	})
	t.Run("llm user without a wired gateway is refused", func(t *testing.T) {
		docker, _ := fakeDocker(t)
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "bench-evalnet"})
		_, _, err := e.StartServices(t.Context(), userRC(t, "r_gw5", llmSim()))
		var se *ServiceError
		if err == nil || !strings.Contains(err.Error(), "not wired to the gateway") || !asServiceError(err, &se) || se.Service != "customer" {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("explicit gateway on a tool service", func(t *testing.T) {
		docker, _ := fakeDocker(t)
		gw := &fakeSvcGateway{}
		e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "bench-evalnet",
			ServiceGateway: ServiceGatewayConfig{Gateway: gw, AgentURL: "http://bifrost:8080"}})
		rc := serviceRC(t, schemas.ServiceSpec{ID: "summarizer", Role: schemas.ServiceRoleTool, Image: "s:1", Port: 9000,
			Gateway: &schemas.ServiceGateway{Provider: "openai", Model: "gpt-x", BudgetUSD: 0.5}})
		rc.Run.RunID = "r_gw6"
		t.Cleanup(func() { e.revokeServiceKeys(context.Background(), "r_gw6") })
		if _, _, err := e.StartServices(t.Context(), rc); err != nil {
			t.Fatal(err)
		}
		if len(gw.minted) != 1 || gw.minted[0].Agent.Model != "gpt-x" {
			t.Fatalf("minted %+v", gw.minted)
		}
	})
}

func asServiceError(err error, target **ServiceError) bool {
	se, ok := err.(*ServiceError)
	if ok {
		*target = se
	}
	return ok
}

func TestPriceServiceRows(t *testing.T) {
	rows := []schemas.GatewayRequest{
		{Status: "success", Kind: "message", Tokens: &schemas.Tokens{Fresh: 1_000_000}},
		{Status: "error", Kind: "message"},
		{Status: "success", Kind: "models"},
	}
	u := priceServiceRows(schemas.ServiceGatewayUsage{Model: "claude-sim", Complete: true, BudgetUSD: 0.5}, rows, price)
	if u.Requests != 2 || u.Errors != 1 || u.USD.State != schemas.ValueObserved || *u.USD.V != 1 || !u.OverBudget {
		t.Fatalf("usage %+v", u)
	}
	if u := priceServiceRows(schemas.ServiceGatewayUsage{Model: "other", Complete: true}, rows, price); u.USD.State != schemas.ValueUnknown {
		t.Fatalf("unpriced model: %+v", u)
	}
	if u := priceServiceRows(schemas.ServiceGatewayUsage{Model: "claude-sim", Reason: "timed out"}, rows, price); u.USD.State != schemas.ValueUnknown || !strings.Contains(u.USD.Reason, "incomplete") {
		t.Fatalf("incomplete rows: %+v", u)
	}
	if u := priceServiceRows(schemas.ServiceGatewayUsage{Model: "claude-sim", Complete: true}, rows, nil); u.USD.State != schemas.ValueUnknown {
		t.Fatalf("no snapshot: %+v", u)
	}
}
