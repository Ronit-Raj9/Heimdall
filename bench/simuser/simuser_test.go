package simuser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEffective(t *testing.T) {
	if Effective(&schemas.Suite{}, &schemas.Experiment{}) != nil {
		t.Fatal("no settings must give nil")
	}
	s := Effective(&schemas.Suite{Simulator: &schemas.SimulatorSpec{Provider: "anthropic", Model: "a", Seed: 5}}, nil)
	if s.Mode != schemas.SimulatorScripted || s.PromptVersion != PromptUserV1 || s.MaxTurns != DefaultMaxTurns || s.Seed != 5 {
		t.Fatalf("defaults: %+v", s)
	}
	o := Effective(&schemas.Suite{Simulator: &schemas.SimulatorSpec{Provider: "anthropic", Model: "a", MaxTurns: 8}},
		&schemas.Experiment{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Model: "b"}})
	if o.Mode != schemas.SimulatorLLM || o.Provider != "anthropic" || o.Model != "b" || o.MaxTurns != 8 {
		t.Fatalf("override: %+v", o)
	}
	if Label(o) != "simulated user (model: anthropic/b)" || Label(s) != "scripted user" || Label(nil) != "scripted user" {
		t.Fatalf("labels %q %q", Label(o), Label(s))
	}
}

func TestCheck(t *testing.T) {
	ok := &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "p", Model: "m", PromptVersion: PromptUserV1, MaxTurns: 10}
	if err := Check(ok); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*schemas.SimulatorSpec){
		"mode":        func(s *schemas.SimulatorSpec) { s.Mode = "human" },
		"turns":       func(s *schemas.SimulatorSpec) { s.MaxTurns = 0 },
		"prompt":      func(s *schemas.SimulatorSpec) { s.PromptVersion = "user-v9" },
		"bad prompt":  func(s *schemas.SimulatorSpec) { s.PromptVersion = "../x" },
		"no model":    func(s *schemas.SimulatorSpec) { s.Model = "" },
		"placeholder": func(s *schemas.SimulatorSpec) { s.Provider = Placeholder },
	} {
		c := *ok
		mut(&c)
		if Check(&c) == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	scripted := &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted, PromptVersion: PromptUserV1, MaxTurns: 3}
	if err := Check(scripted); err != nil {
		t.Fatalf("scripted needs no model: %v", err)
	}
}

func TestHashAndEnv(t *testing.T) {
	a := Effective(&schemas.Suite{Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "p", Model: "m"}}, nil)
	b := *a
	b.Model = "n"
	ha, err := Hash(a)
	if err != nil || len(ha) != 64 {
		t.Fatalf("hash %q %v", ha, err)
	}
	if hb, _ := Hash(&b); hb == ha {
		t.Fatal("model change must change the hash")
	}
	env := ServiceEnv(a)
	sha, _ := PromptSHA256(PromptUserV1)
	if env[EnvMode] != "llm" || env[EnvPromptSHA256] != sha || env[EnvMaxTurns] != "12" {
		t.Fatalf("env %v", env)
	}
	if _, ok := env["BENCH_SVC_GATEWAY_KEY"]; ok {
		t.Fatal("simulator env must never carry gateway access")
	}
}

func TestGatewayFor(t *testing.T) {
	llm := &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "p", Model: "m"}
	user := schemas.ServiceSpec{ID: "customer", Role: schemas.ServiceRoleUser}
	if GatewayFor(user, nil) != nil || GatewayFor(user, &schemas.SimulatorSpec{Mode: schemas.SimulatorScripted}) != nil {
		t.Fatal("a scripted user gets no key")
	}
	if g := GatewayFor(user, llm); g == nil || g.Provider != "p" || g.Model != "m" {
		t.Fatalf("llm user: %+v", g)
	}
	user.Gateway = &schemas.ServiceGateway{Model: "own", BudgetUSD: 1}
	if g := GatewayFor(user, llm); g.Provider != "p" || g.Model != "own" || g.BudgetUSD != 1 {
		t.Fatalf("own model: %+v", g)
	}
	tool := schemas.ServiceSpec{ID: "t", Role: schemas.ServiceRoleTool}
	if GatewayFor(tool, llm) != nil {
		t.Fatal("a tool without gateway gets no key")
	}
	tool.Gateway = &schemas.ServiceGateway{Provider: "x", Model: "y"}
	if g := GatewayFor(tool, nil); g == nil || g.Model != "y" {
		t.Fatalf("tool with gateway: %+v", g)
	}
}

func TestCheckUserFile(t *testing.T) {
	dir := t.TempDir()
	both := filepath.Join(dir, "both.json")
	only := filepath.Join(dir, "script.json")
	if err := os.WriteFile(both, []byte(`{"script":{"opening":"hi"},"persona":{"goal":"refund"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(only, []byte(`{"script":{"opening":"hi"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if CheckUserFile(both, schemas.SimulatorLLM) != nil || CheckUserFile(both, schemas.SimulatorScripted) != nil {
		t.Fatal("a file with both must pass both modes")
	}
	if err := CheckUserFile(only, schemas.SimulatorLLM); err == nil || !strings.Contains(err.Error(), "persona") {
		t.Fatalf("got %v", err)
	}
}

func TestFiles(t *testing.T) {
	f := Files()
	if len(f["simuser.py"]) == 0 || len(f["prompts/user-v1.txt"]) == 0 {
		t.Fatalf("files %v", len(f))
	}
}

// TestSimuserPython runs simuser_test.py: both modes against a local fake
// gateway, no network beyond loopback.
func TestSimuserPython(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-B", "-m", "unittest", "simuser_test")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("simuser_test.py: %v\n%s", err, out)
	}
}

func TestSimulatorBackedService(t *testing.T) {
	llm := &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "p", Model: "m"}
	tau2 := schemas.ServiceSpec{ID: "tau2", Role: schemas.ServiceRoleMCP, Gateway: &schemas.ServiceGateway{}}
	if !SimulatorBacked(tau2) || GatewayFor(tau2, nil) != nil {
		t.Fatal("gateway: {} on a server is simulator-backed and needs no key when scripted")
	}
	if g := GatewayFor(tau2, llm); g == nil || g.Model != "m" {
		t.Fatalf("llm: %+v", g)
	}
	if n := len(SimulatedServices(nil, &schemas.Task{Services: []schemas.ServiceSpec{tau2, {ID: "db"}}})); n != 1 {
		t.Fatalf("simulated services %d", n)
	}
}
