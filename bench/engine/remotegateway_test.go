package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// newDeferredWorld is a coordinator with no gateway at all, as on a hosted
// server, whose experiment was planned with the gateway preflight deferred to
// runners, served over real HTTP.
func newDeferredWorld(t *testing.T) *remoteWorld {
	t.Helper()
	cs := newRemoteSide(t)
	cs.b.reg.Gateway = nil
	m, err := NewPlanner(cs.b.reg, cs.b.root, nil, WithGatewayDeferredToRunner()).Plan(context.Background(), testExperiment())
	if err != nil {
		t.Fatalf("plan without a gateway: %v", err)
	}
	c, err := NewCoordinator(cs.b.reg, RemoteOptions{Token: remoteTestToken, StagingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return &remoteWorld{coord: cs, c: c, url: serveWorkers(t, c), m: m, run: RunOptions{Remote: c}}
}

func runnerSide(t *testing.T, h *schemas.GatewayHygiene) *remoteSide {
	t.Helper()
	side := newRemoteSide(t)
	side.b.reg.Gateway.(*fakeGateway).hygiene = h
	return side
}

func hygieneCheck(t *testing.T, m *schemas.Manifest, runs []schemas.SealedRun) *schemas.InstrumentCheck {
	t.Helper()
	return instrument(t, m, runs, verdict.InstrumentGatewayHygiene)
}

func instrument(t *testing.T, m *schemas.Manifest, runs []schemas.SealedRun, name string) *schemas.InstrumentCheck {
	t.Helper()
	for _, p := range verdict.Builtin() {
		if p.GetName() != "paired_signflip" {
			continue
		}
		an, err := p.Decide(context.Background(), m, runs)
		if err != nil {
			t.Fatal(err)
		}
		for i := range an.Verdict.Instruments {
			if an.Verdict.Instruments[i].Name == name {
				return &an.Verdict.Instruments[i]
			}
		}
		return nil
	}
	t.Fatal("paired_signflip not registered")
	return nil
}

// TestRemoteDeferredGateway: a manifest planned with no gateway records the
// preflight as the runners'; the runner preflights its own gateway before
// every slot, uploads the record with each run, and the gateway_hygiene
// instrument is evaluated from those records.
func TestRemoteDeferredGateway(t *testing.T) {
	rw := newDeferredWorld(t)
	if !rw.m.Gateway.Deferred() {
		t.Fatalf("manifest gateway %+v, want deferred_to_runner", rw.m.Gateway)
	}
	raw, _ := json.Marshal(rw.m.Gateway)
	if !strings.Contains(string(raw), `"deferred_to_runner":true`) {
		t.Fatalf("manifest gateway JSON %s", raw)
	}
	// The deferred record is deterministic: a second plan hashes the same
	// gateway section, so the runner's comparison holds.
	again, err := NewPlanner(rw.coord.b.reg, rw.coord.b.root, nil, WithGatewayDeferredToRunner()).Plan(context.Background(), testExperiment())
	if err != nil || !jsonEqual(again.Gateway, rw.m.Gateway) {
		t.Fatalf("second deferred plan gateway %+v, %v", again.Gateway, err)
	}
	done := rw.attach(t)
	side := runnerSide(t, &schemas.GatewayHygiene{MCPAutoToolInjectOff: true})
	w := rw.worker(t, "w1", side)
	ctx := context.Background()
	for range rw.m.Slots {
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease while slots are pending")
		}
		before := side.b.w.count("preflight")
		res := w.Work(ctx, l)
		if res == nil || !res.Accepted {
			t.Fatalf("run not accepted: %+v", res)
		}
		if side.b.w.count("preflight") <= before {
			t.Fatal("the runner ran a slot without its own gateway preflight")
		}
	}
	if sum := done(); sum.Sealed != len(rw.m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
	runs, err := rw.coord.st.SealedRuns(ctx, rw.m.ExpID, "")
	if err != nil || len(runs) != len(rw.m.Slots) {
		t.Fatalf("sealed runs %d, %v", len(runs), err)
	}
	for _, sr := range runs {
		if len(sr.Problems) > 0 {
			t.Fatalf("run %s problems %v", sr.Run.RunID, sr.Problems)
		}
		if sr.RunnerGateway == nil || sr.RunnerGateway.Name != "fake" || sr.RunnerGateway.Hygiene == nil {
			t.Fatalf("run %s runner gateway %+v", sr.Run.RunID, sr.RunnerGateway)
		}
	}
	c := hygieneCheck(t, rw.m, runs)
	if c == nil || c.Status != schemas.InstrumentPass {
		t.Fatalf("gateway_hygiene %+v", c)
	}
	// The manifest names no gateway part; every run names the runner's.
	for k := range rw.m.Parts {
		if strings.HasPrefix(k, "gateway/") {
			t.Fatalf("deferred manifest freezes gateway part %s", k)
		}
	}
	g0 := side.b.reg.Gateway
	wantPart := "gateway/" + g0.GetName() + "@" + g0.GetVersion()
	for _, sr := range runs {
		if sr.RunnerGateway.PartVersion != wantPart {
			t.Fatalf("run %s part %q, want %q", sr.Run.RunID, sr.RunnerGateway.PartVersion, wantPart)
		}
	}
	if c := instrument(t, rw.m, runs, verdict.InstrumentGatewayVersions); c == nil || c.Status != schemas.InstrumentPass {
		t.Fatalf("gateway_versions %+v", c)
	}
	mixed := append([]schemas.SealedRun(nil), runs...)
	other := *mixed[0].RunnerGateway
	other.PartVersion = "gateway/fake@9.9.9"
	mixed[0].RunnerGateway = &other
	if c := instrument(t, rw.m, mixed, verdict.InstrumentGatewayVersions); c == nil || c.Status != schemas.InstrumentWarn ||
		!strings.Contains(c.Detail, "gateway/fake@9.9.9") || !strings.Contains(c.Detail, wantPart) {
		t.Fatalf("mixed gateway versions: %+v", c)
	}
	// A run whose record is missing leaves the instrument unknown, with
	// the count; a violation fails it.
	missing := append([]schemas.SealedRun(nil), runs...)
	missing[0].RunnerGateway = nil
	if c := hygieneCheck(t, rw.m, missing); c == nil || c.Status != schemas.InstrumentUnknown || !strings.Contains(c.Detail, "1 of") {
		t.Fatalf("missing record: %+v", c)
	}
	bad := append([]schemas.SealedRun(nil), runs...)
	g := *bad[0].RunnerGateway
	g.Hygiene = &schemas.GatewayHygiene{Violations: []string{"semantic cache on"}}
	bad[0].RunnerGateway = &g
	if c := hygieneCheck(t, rw.m, bad); c == nil || c.Status != schemas.InstrumentFail {
		t.Fatalf("violation: %+v", c)
	}
	// A manifest that does not defer has no such instrument: its own
	// preflight refused the plan on any violation.
	plain := *rw.m
	plain.Gateway = &schemas.GatewayManifest{Name: "fake"}
	if c := hygieneCheck(t, &plain, runs); c != nil {
		t.Fatalf("non-deferred manifest got %+v", c)
	}
	// The coordinator sealed the record in remote/worker.json.
	dirs, _ := filepath.Glob(filepath.Join(rw.coord.st.Root(), rw.m.ExpID, "slots", "*", "runs", runs[0].Run.RunID, "remote", "worker.json"))
	if len(dirs) != 1 {
		t.Fatalf("worker.json of %s: %v", runs[0].Run.RunID, dirs)
	}
	b, _ := os.ReadFile(dirs[0])
	if !strings.Contains(string(b), `"gateway"`) {
		t.Fatalf("worker.json without the runner's gateway record: %s", b)
	}
}

// TestRemoteDeferredGatewayRefusals: a runner without a gateway, or whose
// gateway fails the hygiene check, refuses the lease and runs nothing.
func TestRemoteDeferredGatewayRefusals(t *testing.T) {
	cases := []struct {
		name string
		side func(t *testing.T) *remoteSide
		want string
	}{
		{"hygiene violation", func(t *testing.T) *remoteSide {
			return runnerSide(t, &schemas.GatewayHygiene{Violations: []string{"semantic cache plugin is enabled"}})
		}, "hygiene"},
		{"no gateway", func(t *testing.T) *remoteSide {
			s := newRemoteSide(t)
			s.b.reg.Gateway = nil
			return s
		}, "gateway"},
		{"other part differs", func(t *testing.T) *remoteSide {
			// Only gateway/* parts are skipped for a deferred manifest.
			s := runnerSide(t, &schemas.GatewayHygiene{})
			s.b.reg.Gates = append(s.b.reg.Gates, fakeGate{part: part{"extra_gate"}, w: s.b.w})
			return s
		}, "part versions differ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rw := newDeferredWorld(t)
			done := rw.attach(t)
			side := tc.side(t)
			w := rw.worker(t, "w1", side)
			l := lease(t, w)
			if l == nil {
				t.Fatal("no lease")
			}
			if res := w.Work(context.Background(), l); res != nil {
				t.Fatalf("worker ran a refused lease: %+v", res)
			}
			if side.b.w.count("exec") != 0 {
				t.Fatal("agent ran on a refused lease")
			}
			journal, err := os.ReadFile(filepath.Join(rw.c.opts.StagingDir, rw.m.ExpID+".jsonl"))
			if err != nil || !strings.Contains(string(journal), tc.want) {
				t.Fatalf("journal %s %v", journal, err)
			}
			if sum := done(); sum.Sealed != 0 {
				t.Fatalf("summary %+v", sum)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
