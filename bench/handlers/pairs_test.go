package handlers

import (
	"testing"
)

func TestPairResponse(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_B"][FileEvents] = []byte("{\"event_id\":\"r_t01_B:1\",\"type\":\"tool_call\",\"target\":\"cat CLAUDE.md\"}\n" +
		"{\"event_id\":\"r_t01_B:2\",\"type\":\"tool_call\",\"target\":\"pytest\"}\n")
	d := fullDeps()
	d.index = idx
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/pairs/p_t01", nil, nil))
	runs, _ := body["runs"].([]any)
	steps, _ := body["alignment"].([]any)
	first, _ := body["first_divergence"].(map[string]any)
	if len(runs) != 2 || len(steps) != 2 || first == nil || first["index"] != float64(0) || first["note"] != DivergenceNote {
		t.Fatalf("pair = %v", body)
	}
	step0 := steps[0].(map[string]any)
	events := step0["events"].(map[string]any)
	if events["A"] != nil || events["B"] != "r_t01_B:1" || step0["diverged"] != true {
		t.Errorf("step 0 = %v", step0)
	}
	if run0 := runs[0].(map[string]any); run0["arm"] != "A" || run0["metrics"] == nil {
		t.Errorf("run 0 = %v", run0)
	}
}

func TestPairWithOneRunHasNoAlignment(t *testing.T) {
	idx := testIndex()
	idx.runs = idx.runs[:1]
	d := fullDeps()
	d.index = idx
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/pairs/p_t01", nil, nil))
	if steps, _ := body["alignment"].([]any); len(steps) != 0 || body["first_divergence"] != nil {
		t.Fatalf("pair = %v", body)
	}
}

// 6-A-03 and 6-A-05: a pair carries each arm's own provenance (independent of
// the server mode) and the suite its task links into.
func TestPairProvenanceAndSuite(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	m.SuiteID = "coding-core"
	b := m.Arms["B"]
	b.Config.Adapter = "scripted"
	m.Arms["B"] = b
	d := fullDeps()
	d.index = idx
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/pairs/p_t01", nil, nil))
	if body["suite"] != "coding-core" {
		t.Fatalf("suite = %v", body["suite"])
	}
	runs, _ := body["runs"].([]any)
	got := map[string]any{}
	for _, r := range runs {
		rm := r.(map[string]any)
		got[rm["arm"].(string)] = rm["synthetic"]
	}
	if got["A"] != false || got["B"] != true {
		t.Fatalf("synthetic by arm = %v", got)
	}
}

// QC2-27-05, QC2-27-06, QC2-27-09: provenance is per run. A torn ledger makes
// only that run unavailable and leaves the pair readable; a run with no
// recorded model call is unobserved, never model-played; the pair names the
// frozen suite version.
func TestPairProvenancePerRun(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	m.SuiteID, m.SuiteVersion = "coding-core", "1.0.0"
	ids := map[string]string{}
	for _, rv := range idx.Pair("p_t01") {
		ids[rv.Slot.Arm] = rv.Run.RunID
	}
	if idx.files == nil {
		idx.files = map[string]map[string][]byte{}
	}
	set := func(arm, data string) {
		if idx.files[ids[arm]] == nil {
			idx.files[ids[arm]] = map[string][]byte{}
		}
		idx.files[ids[arm]][proxyLedgerFile] = []byte(data)
	}
	set("A", `{"kind":"message","action":"pass","upstream":"real","status":200}`+"\n"+`{"kind":"mess`)
	d := fullDeps()
	d.index = idx
	get := func() (map[string]any, map[string]map[string]any) {
		resp := newTestClient(t, d).do(t, "GET", "/api/bench/pairs/p_t01", nil, nil)
		if resp.StatusCode() != 200 {
			t.Fatalf("status %d", resp.StatusCode())
		}
		body := decodeBody(t, resp)
		byArm := map[string]map[string]any{}
		for _, r := range body["runs"].([]any) {
			rm := r.(map[string]any)
			byArm[rm["arm"].(string)] = rm
		}
		return body, byArm
	}
	body, arms := get()
	if body["suite_version"] != "1.0.0" || body["suite"] != "coding-core" {
		t.Fatalf("suite ref %v@%v", body["suite"], body["suite_version"])
	}
	if arms["A"]["provenance"] != "unavailable" || arms["A"]["provenance_note"] == nil || arms["A"]["metrics"] == nil {
		t.Fatalf("torn ledger arm %v", arms["A"])
	}
	if arms["B"]["provenance"] != "unobserved" || arms["B"]["synthetic"] != false {
		t.Fatalf("no ledger arm %v, want unobserved", arms["B"])
	}
	if a, _ := body["alignment"].([]any); len(a) == 0 {
		t.Fatal("alignment lost with a torn ledger")
	}
	set("A", `{"kind":"message","action":"pass","upstream":"real","status":200}`)
	set("B", `{"kind":"message","action":"pass","upstream":"scripted","status":200}`)
	_, arms = get()
	if arms["A"]["provenance"] != "model" || arms["B"]["provenance"] != "scripted" || arms["B"]["synthetic"] != true {
		t.Fatalf("model/scripted %v / %v", arms["A"], arms["B"])
	}
}
