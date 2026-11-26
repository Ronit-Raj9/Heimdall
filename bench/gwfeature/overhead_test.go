package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestOverheadDraftIsScriptedSingleArm(t *testing.T) {
	f, _ := Lookup(schemas.GatewayFeatureOverhead)
	get := func(_ context.Context, path string, out any) error {
		switch path {
		case "/api/version":
			return json.Unmarshal([]byte(`"v2.5.1"`), out)
		case "/api/plugins/loaded":
			return json.Unmarshal([]byte(`{"plugins":["logging","governance"]}`), out)
		}
		return errors.New("no route")
	}
	exp, notes, err := f.Draft(context.Background(), DraftEnv{Root: t.TempDir(), GatewayGet: get},
		DraftRequest{Feature: f.ID, Suite: "s@1", Base: "agents/a.yaml", Model: "claude-sonnet-4-5", Trials: 3, BudgetUSD: 1,
			Tasks: []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10", "t11", "t12"}})
	if err != nil {
		t.Fatal(err)
	}
	if !exp.ScriptedUpstream || len(exp.Arms) != 1 || exp.Arms["A"].Set["model"] != "claude-sonnet-4-5" {
		t.Fatalf("scripted %v arms %+v", exp.ScriptedUpstream, exp.Arms)
	}
	c := exp.Conditions[0]
	if c.Trials != 1 || len(c.Tasks.IDs) != 10 || c.Tasks.IDs[9] != "t10" {
		t.Fatalf("condition %+v", c)
	}
	g := exp.GatewayTest
	if g == nil || g.Control != "A" || g.Variants == nil || len(g.Variants) != 0 || g.Options["gateway_version"] != "v2.5.1" || g.Options["plugins"] != "governance,logging" {
		t.Fatalf("test %+v", g)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "costs nothing") {
		t.Fatalf("notes %v", notes)
	}
	exp, _, err = f.Draft(context.Background(), DraftEnv{Root: t.TempDir()}, DraftRequest{Feature: f.ID, Suite: "s@1", Base: "agents/a.yaml", Model: "m", Trials: 1, BudgetUSD: 1})
	if err != nil || exp.GatewayTest.Options["gateway_version"] != "unknown" {
		t.Fatalf("without a gateway: %v %+v", err, exp)
	}
}

// p4OverheadRuns builds one run with n message rows, each 2 ms of overhead:
// governance 800 us, logging 500 us, core 700 us.
func p4OverheadRuns(n int, withBuckets bool) []Run {
	r := Run{RunID: "r1", Task: "t1", Arm: "A"}
	for i := 0; i < n; i++ {
		ms := 2.0
		row := Row{ID: fmt.Sprint(i), Kind: "message", Status: "success", OverheadMS: &ms, RawKnown: true}
		if withBuckets {
			row.Overhead = []Bucket{{Name: "plugin.governance", US: 800}, {Name: "plugin.logging", US: 500}, {Name: "core", US: 700}}
		}
		r.Rows = append(r.Rows, row)
	}
	return []Run{r}
}

func p4OverheadDetails(t *testing.T, e Effect) overheadDetails {
	t.Helper()
	var d overheadDetails
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOverheadBucketsMedianP95Share(t *testing.T) {
	runs := p4OverheadRuns(60, true)
	for i := 0; i < 10; i++ { // ten slow rows
		ms := 10.0
		runs[0].Rows[i].OverheadMS = &ms
		runs[0].Rows[i].Overhead[0].US = 8800
	}
	e := ComputeOverhead(runs, map[string]string{"gateway_version": "v2.5.1", "plugins": "governance,logging"})
	if e.State != StateMeasured || e.Source.Gateway != "v2.5.1" {
		t.Fatalf("state %s reason %q source %+v", e.State, e.Reason, e.Source)
	}
	d := p4OverheadDetails(t, e)
	if d.Rows != 60 || d.MedianMS == nil || *d.MedianMS != 2 || d.P95MS == nil || *d.P95MS != 10 {
		t.Fatalf("total %+v", d)
	}
	if len(d.Buckets) != 3 || d.Buckets[0].Name != "plugin.governance" {
		t.Fatalf("buckets %+v", d.Buckets)
	}
	g := d.Buckets[0]
	if g.Rows != 60 || g.MedianUS != 800 || g.P95US != 8800 || fmt.Sprintf("%.4f", g.Share) != "0.6400" {
		t.Fatalf("governance %+v", g)
	}
	if !strings.HasPrefix(e.Headline, "Bifrost adds 2.0 ms per call (median; 10.0 ms at the 95th percentile). Largest parts: governance 0.8 ms, core 0.7 ms.") {
		t.Fatalf("headline %s", e.Headline)
	}
	if e.Setting == nil || e.Setting.Kind != "none" || e.Setting.Title != "No setting to copy" {
		t.Fatalf("setting %+v", e.Setting)
	}
}

func TestOverheadNotEnoughRows(t *testing.T) {
	e := ComputeOverhead(p4OverheadRuns(49, true), nil)
	if e.State != StateNotEnoughData || !strings.Contains(e.Reason, "49") {
		t.Fatalf("state %s reason %q", e.State, e.Reason)
	}
	e = ComputeOverhead(p4OverheadRuns(80, false), nil)
	if e.State != StateNotEnoughData {
		t.Fatalf("rows without buckets: %s", e.State)
	}
}

func TestOverheadPluginWithoutTime(t *testing.T) {
	e := ComputeOverhead(p4OverheadRuns(50, true), map[string]string{"plugins": "governance,logging,semantic_cache"})
	d := p4OverheadDetails(t, e)
	if strings.Join(d.NoTime, ",") != "semantic_cache" {
		t.Fatalf("no time %v", d.NoTime)
	}
}
