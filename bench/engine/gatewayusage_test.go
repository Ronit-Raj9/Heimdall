package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
)

type h01Store struct {
	schemas.Store
	files map[string][]byte
}

func (s *h01Store) GetName() string { return "h01" }
func (s *h01Store) WriteRunFile(_ context.Context, run *schemas.Run, name string, b []byte) error {
	s.files[run.RunID+"/"+name] = b
	return nil
}

type h01Gateway struct {
	schemas.Gateway
	org string
}

func (g h01Gateway) RunOrg(*schemas.RunContext) (string, bool) { return g.org, g.org != "" }

func TestSealGatewayUsageByRunOrg(t *testing.T) {
	ctx := context.Background()
	var asked []string
	SetGatewayUsageSource(func(runID, org string) ([]byte, bool) {
		asked = append(asked, runID+"|"+org)
		if runID == "run-7" && org == "org-1" {
			return []byte(`[{"request_id":"a"}]`), true
		}
		return nil, false
	})
	defer SetGatewayUsageSource(nil)

	st := &h01Store{files: map[string][]byte{}}
	p := &Pipeline{reg: &schemas.Registry{Store: st, Gateway: h01Gateway{org: "org-1"}}}
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "run-7", ExpID: "e_x"}}
	if err := sealGatewayUsage(ctx, st, rc.Run, p.runOrg(rc)); err != nil {
		t.Fatal(err)
	}
	if string(st.files["run-7/"+FileGatewayUsage]) != `[{"request_id":"a"}]` {
		t.Fatalf("sealed file: %v", st.files)
	}
	// Another org's run, or no rows: no file.
	if err := sealGatewayUsage(ctx, st, &schemas.Run{RunID: "run-8"}, "org-2"); err != nil || len(st.files) != 1 {
		t.Fatalf("no rows should write nothing: %v %v", err, st.files)
	}
	if asked[0] != "run-7|org-1" {
		t.Fatalf("source asked %v", asked)
	}
	// No source wired: no file, no error.
	SetGatewayUsageSource(nil)
	if err := sealGatewayUsage(ctx, st, &schemas.Run{RunID: "run-7"}, "org-1"); err != nil || len(st.files) != 1 {
		t.Fatalf("unwired: %v %v", err, st.files)
	}
	// A gateway with no org binding: local install, empty org.
	if org := (&Pipeline{reg: &schemas.Registry{}}).runOrg(rc); org != "" {
		t.Fatalf("no gateway org: %q", org)
	}
}

func TestPluginHeadersCarryRunOrg(t *testing.T) {
	ctx := context.Background()
	pf := bifrost.NewPluginFaults(&c04Proxy{plans: map[string]schemas.FaultPlan{}})
	p := &Pipeline{reg: &schemas.Registry{Fault: pf, Gateway: h01Gateway{org: "org-1"}}}
	if err := pf.SetPlan(ctx, "slot-1", schemas.FaultPlan{Mode: faultproxy.ModePersistent, FromCall: 1, Status: 503, RunID: "run-7"}); err != nil {
		t.Fatal(err)
	}
	st := c04State(adapters.ParserClaudeCode)
	if err := p.routePluginFaults(ctx, st); err != nil {
		t.Fatal(err)
	}
	if st.rc.FaultHeaders[bifrost.HeaderOrg] != "org-1" {
		t.Fatalf("org header: %v", st.rc.FaultHeaders)
	}
}
