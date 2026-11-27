package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// UI fix wave, lane G3: paging, light lists, arm stats, the sealed metrics
// cache and the leaderboard's per-request reads.

func pagingCtx(query string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI("http://x/list" + query)
	return ctx
}

func TestReadListPaging(t *testing.T) {
	for _, tc := range []struct {
		query         string
		on            bool
		limit, offset int
		q             string
		status        int
	}{
		{query: "", on: false, limit: listPageDefaultLimit},
		{query: "?other=1", on: false, limit: listPageDefaultLimit},
		{query: "?limit=5", on: true, limit: 5},
		{query: "?limit=0", on: true, limit: 1},
		{query: "?limit=9000", on: true, limit: listPageMaxLimit},
		{query: "?offset=7", on: true, limit: listPageDefaultLimit, offset: 7},
		{query: "?q=%20HeLLo%20", on: true, limit: listPageDefaultLimit, q: "hello"},
		{query: "?q=", on: true, limit: listPageDefaultLimit},
		{query: "?limit=x", status: 400},
		{query: "?offset=-1", status: 400},
		{query: "?q=" + string(make([]byte, 0)) + fmt.Sprintf("%0201d", 0), status: 400},
	} {
		ctx := pagingCtx(tc.query)
		p, ok := readListPaging(ctx, testMode)
		if tc.status != 0 {
			if ok || ctx.Response.StatusCode() != tc.status {
				t.Fatalf("%s: ok %v status %d, want %d", tc.query, ok, ctx.Response.StatusCode(), tc.status)
			}
			continue
		}
		if !ok || p.on != tc.on || p.limit != tc.limit || p.offset != tc.offset || p.q != tc.q {
			t.Fatalf("%s: got %+v ok %v, want on %v limit %d offset %d q %q", tc.query, p, ok, tc.on, tc.limit, tc.offset, tc.q)
		}
	}
}

func TestPageItems(t *testing.T) {
	items := []string{"Alpha", "beta", "GAMMA", "alphabet"}
	id := func(s string) []string { return []string{s} }
	rows, page := pageItems(listPaging{on: true, q: "alpha", limit: 1}, items, id)
	if !slices.Equal(rows, []string{"Alpha"}) || page != (ListPage{Total: 2, Limit: 1, Offset: 0}) {
		t.Fatalf("first page %v %+v", rows, page)
	}
	rows, page = pageItems(listPaging{on: true, q: "alpha", limit: 1, offset: 1}, items, id)
	if !slices.Equal(rows, []string{"alphabet"}) || page.Total != 2 {
		t.Fatalf("second page %v %+v", rows, page)
	}
	rows, page = pageItems(listPaging{on: true, limit: 10, offset: 99}, items, id)
	if len(rows) != 0 || page.Total != 4 {
		t.Fatalf("past the end %v %+v", rows, page)
	}
}

// g3Index is testIndex with a second, empty experiment and costs on three
// of its four runs.
func g3Index() *fakeIndex {
	idx := testIndex()
	m := testManifest()
	m.ExpID, m.Experiment.Name, m.Slots = "e_second", "Second Look", nil
	idx.manifests["e_second"] = m
	cost := func(v float64) []byte {
		return []byte(fmt.Sprintf(`{"cost_usd":{"state":"observed","v":%g,"evidence":"gateway"}}`, v))
	}
	idx.files["r_t01_A"][FileMetrics] = cost(0.1)
	idx.files["r_t02_A"][FileMetrics] = cost(0.2)
	idx.files["r_t01_B"][FileMetrics] = cost(0.5)
	return idx
}

func TestExperimentsListPaging(t *testing.T) {
	c := newTestClient(t, testDeps{index: g3Index(), analyzer: &fakeAnalyzer{}, launcher: &fakeLauncher{}, catalog: fakeCatalog{}})
	get := func(q string) (int, map[string]any) {
		resp := c.do(t, "GET", "/api/bench/experiments"+q, nil, nil)
		if resp.StatusCode() != 200 {
			return resp.StatusCode(), nil
		}
		return 200, decodeBody(t, resp)
	}
	// The old call: every experiment, no paging fields, no arm stats.
	_, plain := get("")
	if len(listAt(plain, "experiments")) != 2 {
		t.Fatalf("plain list %v", plain)
	}
	for _, k := range []string{"total", "limit", "offset"} {
		if _, ok := plain[k]; ok {
			t.Fatalf("plain list has %s", k)
		}
	}
	for _, row := range listAt(plain, "experiments") {
		if _, ok := row["arm_stats"]; ok {
			t.Fatalf("plain row has arm_stats: %v", row)
		}
	}
	// A page carries the same row as the plain list.
	_, one := get("?q=demo")
	rows := listAt(one, "experiments")
	if one["total"] != 1.0 || len(rows) != 1 || rows[0]["exp_id"] != testExp {
		t.Fatalf("q=demo %v", one)
	}
	var plainRow map[string]any
	for _, r := range listAt(plain, "experiments") {
		if r["exp_id"] == testExp {
			plainRow = r
		}
	}
	a, _ := json.Marshal(plainRow)
	b, _ := json.Marshal(rows[0])
	if string(a) != string(b) {
		t.Fatalf("paged row differs:\n%s\n%s", a, b)
	}
	if _, body := get("?q=SECOND"); body["total"] != 1.0 || listAt(body, "experiments")[0]["exp_id"] != "e_second" {
		t.Fatalf("q matches the name ignoring case: %v", body)
	}
	if _, body := get("?limit=1"); body["total"] != 2.0 || body["limit"] != 1.0 || len(listAt(body, "experiments")) != 1 {
		t.Fatalf("limit=1 %v", body)
	}
	if _, body := get("?offset=5"); body["total"] != 2.0 || len(listAt(body, "experiments")) != 0 {
		t.Fatalf("offset past the end %v", body)
	}
	if code, _ := get("?limit=x"); code != 400 {
		t.Fatalf("bad limit status %d", code)
	}
	if code, _ := get("?arm_stats=maybe"); code != 400 {
		t.Fatalf("bad arm_stats status %d", code)
	}
}

func TestExperimentsListArmStats(t *testing.T) {
	c := newTestClient(t, testDeps{index: g3Index(), analyzer: &fakeAnalyzer{}, launcher: &fakeLauncher{}, catalog: fakeCatalog{}})
	body := decodeBody(t, c.do(t, "GET", "/api/bench/experiments?arm_stats=1&q="+testExp, nil, nil))
	rows := listAt(body, "experiments")
	if len(rows) != 1 {
		t.Fatalf("rows %v", body)
	}
	stats, _ := rows[0]["arm_stats"].([]any)
	if len(stats) != 2 {
		t.Fatalf("arm_stats %v", rows[0]["arm_stats"])
	}
	a, b := stats[0].(map[string]any), stats[1].(map[string]any)
	// A: t01 fail, t02 pass, both priced 0.1 + 0.2.
	if a["arm"] != "A" || a["runs"] != 2.0 || a["passed"] != 1.0 || a["pass_rate"] != 0.5 || a["cost_runs"] != 2.0 {
		t.Fatalf("arm A %v", a)
	}
	if c, _ := a["cost_usd"].(float64); c < 0.2999 || c > 0.3001 {
		t.Fatalf("arm A cost %v", a["cost_usd"])
	}
	// B: both pass; one run has no cost, so the setup's cost is not known.
	if b["arm"] != "B" || b["runs"] != 2.0 || b["passed"] != 2.0 || b["pass_rate"] != 1.0 || b["cost_usd"] != nil || b["cost_runs"] != 1.0 {
		t.Fatalf("arm B %v", b)
	}
	// arm_stats alone pages nothing away.
	all := decodeBody(t, c.do(t, "GET", "/api/bench/experiments?arm_stats=1", nil, nil))
	if len(listAt(all, "experiments")) != 2 || all["total"] != 2.0 {
		t.Fatalf("arm_stats alone %v", all)
	}
	empty := armStats(g3Index(), g3Index().manifests["e_second"], nil)
	if len(empty) != 2 || empty[0].PassRate != nil || empty[0].CostUSD != nil {
		t.Fatalf("no runs: %+v", empty)
	}
}

func TestSealedMetricsCache(t *testing.T) {
	idx := testIndex()
	runs := idx.Runs(testExp)[:1]
	id := runs[0].Run.RunID
	first := runMetrics(idx, runs)[id]
	if !first.found {
		t.Fatalf("first read %+v", first)
	}
	// The sealed file is read once: a change under the same seal hash is not
	// seen (a sealed folder cannot change).
	idx.files[id][FileMetrics] = []byte(`{"steps":{"state":"observed","v":99,"evidence":"native"}}`)
	if v := runMetrics(idx, runs)[id].values["steps"].V; v == nil || *v != 3 {
		t.Fatalf("cached steps %v", v)
	}
	// A caller changing its map never changes the cache.
	got := runMetrics(idx, runs)[id]
	delete(got.values, "steps")
	if _, ok := runMetrics(idx, runs)[id].values["steps"]; !ok {
		t.Fatal("cache changed through a caller's map")
	}
	// Another seal hash is another file: read again.
	resealed := slices.Clone(runs)
	resealed[0].SealHash = "sha256:other"
	if v := runMetrics(idx, resealed)[id].values["steps"].V; v == nil || *v != 99 {
		t.Fatalf("resealed steps %v", v)
	}
	// No seal hash: never cached.
	unhashed := slices.Clone(runs)
	unhashed[0].SealHash = ""
	idx.files[id][FileMetrics] = []byte(`{"steps":{"state":"observed","v":7,"evidence":"native"}}`)
	if v := runMetrics(idx, unhashed)[id].values["steps"].V; v == nil || *v != 7 {
		t.Fatalf("unhashed steps %v", v)
	}
	idx.files[id][FileMetrics] = []byte(`{"steps":{"state":"observed","v":8,"evidence":"native"}}`)
	if v := runMetrics(idx, unhashed)[id].values["steps"].V; v == nil || *v != 8 {
		t.Fatalf("unhashed steps not read again: %v", v)
	}
	// Another index (another organization) with the same run id and seal
	// hash never sees this index's entry.
	other := testIndex()
	other.files[id][FileMetrics] = []byte(`{"steps":{"state":"observed","v":42,"evidence":"native"}}`)
	if v := runMetrics(other, other.Runs(testExp)[:1])[id].values["steps"].V; v == nil || *v != 42 {
		t.Fatalf("other index steps %v", v)
	}
	// An index type that cannot be a map key is read every time, never panics.
	ci := newClusterIndex()
	if r := runMetrics(ci, ci.Runs(testExp)[:1])[id]; !r.found {
		t.Fatalf("uncomparable index %+v", r)
	}
}

// saveEvaluations stores evaluations straight into the project's store.
func saveEvaluations(t *testing.T, dir string, evs ...*schemas.Evaluation) {
	t.Helper()
	for _, ev := range evs {
		if err := evalstore.Open(dir).Save(ev); err != nil {
			t.Fatal(err)
		}
	}
}

func g3Evaluation(id, org, name string, created time.Time) *schemas.Evaluation {
	return &schemas.Evaluation{
		ID: id, Org: org, Status: schemas.EvaluationDone, CreatedAt: created,
		Spec: schemas.EvaluationSpec{Name: name, Mode: "score", Benchmarks: []string{"longmemeval"}, Faults: []string{"clean"},
			Arms: []schemas.EvaluationArm{{Label: "A", Agent: "scripted", Model: "m"}},
			Gate: &schemas.EvaluationGate{MinScore: map[string]float64{"coding": 0.5}}},
		Children: []schemas.EvaluationChild{{Benchmark: "longmemeval", Status: string(schemas.ExperimentDone),
			SkippedTasks: []schemas.EvaluationSkippedTask{{Task: "t01", Reason: "needs gpu"}}, FaultOf: map[string]string{"x": "y"}}},
		Result: &schemas.EvaluationResult{Arms: []schemas.EvaluationArmResult{{Label: "A", Score: 0.5, Runs: 4}}, Analyses: map[string]string{"e": "a_1"}},
	}
}

func TestEvaluationsListOptions(t *testing.T) {
	r, dir := evalServer(t, Services{Suites: evalSuites(), OrgID: "org1"})
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	saveEvaluations(t, dir,
		g3Evaluation("ev_20261120_090000_aaaa", "org1", "Nightly coding", at),
		g3Evaluation("ev_20261120_090100_bbbb", "org1", "Memory check", at.Add(time.Minute)),
		g3Evaluation("ev_20261120_090200_cccc", "org2", "Nightly coding other org", at.Add(2*time.Minute)),
	)
	list := func(q string) (int, map[string]any) {
		ctx := evalCall(r, "GET", APIPrefix+"/evaluations"+q, "")
		var body map[string]any
		_ = json.Unmarshal(ctx.Response.Body(), &body)
		return ctx.Response.StatusCode(), body
	}
	code, plain := list("")
	rows := listAt(plain, "evaluations")
	if code != 200 || len(rows) != 2 {
		t.Fatalf("plain %d %v", code, plain)
	}
	if _, ok := plain["total"]; ok {
		t.Fatal("plain list has total")
	}
	// The old call keeps the heavy parts.
	ch := rows[0]["children"].([]any)[0].(map[string]any)
	if _, ok := ch["skipped_tasks"]; !ok {
		t.Fatalf("plain child lost skipped_tasks: %v", ch)
	}
	status := rows[0]["status"].(string)

	// Light: the row's fields, not the heavy ones.
	_, light := list("?fields=light")
	lrows := listAt(light, "evaluations")
	if len(lrows) != 2 {
		t.Fatalf("light %v", light)
	}
	if _, ok := light["total"]; ok {
		t.Fatal("light without paging has total")
	}
	lr := lrows[0]
	spec := lr["spec"].(map[string]any)
	lch := lr["children"].([]any)[0].(map[string]any)
	res := lr["result"].(map[string]any)
	if spec["name"] == nil || spec["gate"] != nil || lch["skipped_tasks"] != nil || lch["fault_of"] != nil || lch["benchmark"] != "longmemeval" || res["analyses"] != nil || res["arms"] == nil {
		t.Fatalf("light row %v", lr)
	}
	// Light never changes the stored evaluation.
	if again := listAt(func() map[string]any { _, b := list(""); return b }(), "evaluations")[0]["children"].([]any)[0].(map[string]any); again["skipped_tasks"] == nil {
		t.Fatal("light changed the store")
	}

	// q on id and name, paging, and the other org never counted.
	_, nightly := list("?q=NIGHTLY")
	if nightly["total"] != 1.0 || len(listAt(nightly, "evaluations")) != 1 || listAt(nightly, "evaluations")[0]["id"] != "ev_20261120_090000_aaaa" {
		t.Fatalf("q=NIGHTLY %v", nightly)
	}
	_, byID := list("?q=bbbb")
	if byID["total"] != 1.0 {
		t.Fatalf("q by id %v", byID)
	}
	_, other := list("?q=other+org")
	if other["total"] != 0.0 || len(listAt(other, "evaluations")) != 0 {
		t.Fatalf("other org leaked: %v", other)
	}
	_, page := list("?limit=1&offset=1")
	if page["total"] != 2.0 || len(listAt(page, "evaluations")) != 1 {
		t.Fatalf("page %v", page)
	}

	// status keeps only the asked statuses.
	_, same := list("?status=" + status)
	if len(listAt(same, "evaluations")) != 2 {
		t.Fatalf("status=%s %v", status, same)
	}
	otherStatus := schemas.EvaluationQueued
	if status == otherStatus {
		otherStatus = schemas.EvaluationFailed
	}
	if _, none := list("?status=" + otherStatus); len(listAt(none, "evaluations")) != 0 {
		t.Fatalf("status=%s %v", otherStatus, none)
	}
	if code, _ := list("?status=finished"); code != 400 {
		t.Fatalf("bad status %d", code)
	}
	if code, _ := list("?fields=tiny"); code != 400 {
		t.Fatalf("bad fields %d", code)
	}
}

func TestAgentsListPaging(t *testing.T) {
	c := newSuitesClient(t, testSuites(t), fakeAgents{}, testIndex())
	plain := decodeBody(t, c.do(t, "GET", APIPrefix+"/agents", nil, nil))
	n := len(listAt(plain, "agents"))
	if n < 2 {
		t.Fatalf("plain agents %v", plain)
	}
	if _, ok := plain["total"]; ok {
		t.Fatal("plain agents has total")
	}
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/agents?q=CUSTOM", nil, nil))
	rows := listAt(body, "agents")
	if body["total"] != 1.0 || len(rows) != 1 || rows[0]["id"] != "custom" {
		t.Fatalf("q=CUSTOM %v", body)
	}
	body = decodeBody(t, c.do(t, "GET", APIPrefix+"/agents?limit=1", nil, nil))
	if body["total"] != float64(n) || len(listAt(body, "agents")) != 1 {
		t.Fatalf("limit=1 %v", body)
	}
	if resp := c.do(t, "GET", APIPrefix+"/agents?offset=no", nil, nil); resp.StatusCode() != 400 {
		t.Fatalf("bad offset %d", resp.StatusCode())
	}
}

func TestSuitesListPagingAndLightFacts(t *testing.T) {
	suites := testSuites(t)
	no := false
	canary := suites.suites["canary"]
	canary.Tasks[0].Provenance = &schemas.TaskProvenance{CreatedBy: minedBy, InstructionReviewed: &no}
	canary.Tasks[1].Provenance = &schemas.TaskProvenance{CreatedBy: minedBy}
	c := newSuitesClient(t, suites, fakeAgents{}, testIndex())

	full := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites", nil, nil))
	if _, ok := full["total"]; ok {
		t.Fatal("full list has total")
	}
	if _, ok := suiteByID(t, full, "canary")["mined_tasks"]; ok {
		t.Fatal("full list carries light facts")
	}
	light := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites?fields=light", nil, nil))
	if _, ok := light["total"]; ok {
		t.Fatal("light list without paging has total")
	}
	cs := suiteByID(t, light, "canary")
	if tasks, _ := cs["tasks"].([]any); len(tasks) != 0 {
		t.Fatalf("light entry has tasks: %v", cs["tasks"])
	}
	if cs["mined_tasks"] != 2.0 || cs["redacted_tasks"] != 0.0 || cs["mined_admitted_tasks"] == nil {
		t.Fatalf("light facts %v", cs)
	}
	un, _ := cs["unreviewed_tasks"].([]any)
	if len(un) != 1 || un[0].(map[string]any)["id"] != "c01" {
		t.Fatalf("unreviewed %v", cs["unreviewed_tasks"])
	}
	if _, ok := suiteByID(t, light, "broken")["mined_tasks"]; ok {
		t.Fatal("a broken entry has light facts")
	}
	// Paging works on both shapes.
	for _, q := range []string{"?q=CANARY", "?fields=light&q=canary"} {
		body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites"+q, nil, nil))
		if body["total"] != 1.0 || len(listAt(body, "suites")) != 1 {
			t.Fatalf("%s %v", q, body)
		}
	}
	body := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites?limit=2", nil, nil))
	if body["total"] != 3.0 || len(listAt(body, "suites")) != 2 {
		t.Fatalf("limit=2 %v", body)
	}
}

func TestSuiteTaskList(t *testing.T) {
	c := newHiddenSuitesClient(t, testSuites(t), func(id string) bool { return id == "drifted" })
	resp := c.do(t, "GET", APIPrefix+"/suites/canary/task-list", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	body := decodeBody(t, resp)
	if body["suite"] != "canary" || body["total"] != 3.0 || len(listAt(body, "tasks")) != 3 || body["limit"] != float64(listPageDefaultLimit) {
		t.Fatalf("all tasks %v", body)
	}
	// The same rows GET /suites/{id} embeds.
	one := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/canary", nil, nil))
	a, _ := json.Marshal(one["tasks"])
	b, _ := json.Marshal(body["tasks"])
	if string(a) != string(b) {
		t.Fatalf("task rows differ:\n%s\n%s", a, b)
	}
	page := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/canary/task-list?limit=1&offset=1", nil, nil))
	if rows := listAt(page, "tasks"); page["total"] != 3.0 || len(rows) != 1 || rows[0]["id"] != "c02" {
		t.Fatalf("page %v", page)
	}
	q := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/canary/task-list?q=C03", nil, nil))
	if rows := listAt(q, "tasks"); q["total"] != 1.0 || len(rows) != 1 || rows[0]["id"] != "c03" {
		t.Fatalf("q %v", q)
	}
	none := decodeBody(t, c.do(t, "GET", APIPrefix+"/suites/canary/task-list?q=zzz", nil, nil))
	if tasks, ok := none["tasks"].([]any); !ok || len(tasks) != 0 || none["total"] != 0.0 {
		t.Fatalf("no match must be an empty list: %v", none)
	}
	for path, want := range map[string]int{
		"/suites/drifted/task-list":         404, // hidden by the list
		"/suites/nope/task-list":            404,
		"/suites/canary/task-list?limit=-3": 400,
	} {
		if resp := c.do(t, "GET", APIPrefix+path, nil, nil); resp.StatusCode() != want {
			t.Fatalf("%s status %d, want %d", path, resp.StatusCode(), want)
		}
	}
}

func TestClustersPaging(t *testing.T) {
	_, plain := getClustersPath(t, newClusterIndex(), "/clusters")
	if _, ok := plain["total"]; ok {
		t.Fatal("plain clusters has total")
	}
	all := listAt(plain, "clusters")
	if len(all) != 2 {
		t.Fatalf("plain %v", plain)
	}
	status, page := getClustersPath(t, newClusterIndex(), "/clusters?limit=1")
	if status != 200 || page["total"] != 2.0 || len(listAt(page, "clusters")) != 1 || page["runs"] != plain["runs"] {
		t.Fatalf("limit=1 %v", page)
	}
	title, _ := all[1]["title"].(string)
	_, q := getClustersPath(t, newClusterIndex(), "/clusters?q="+fasthttpEscape(title))
	if rows := listAt(q, "clusters"); len(rows) < 1 || rows[0]["signature"] == nil {
		t.Fatalf("q=%s %v", title, q)
	}
	if status, _ := getClustersPath(t, newClusterIndex(), "/clusters?limit=a"); status != 400 {
		t.Fatalf("bad limit %d", status)
	}
}

func fasthttpEscape(s string) string {
	var a fasthttp.Args
	a.Set("x", s)
	return string(a.QueryString()[2:])
}

type g3Catalog struct{}

func (g3Catalog) Catalog() *schemas.Catalog {
	return &schemas.Catalog{Suites: []schemas.CatalogSuite{
		{ID: "hd-harbor-bfcl", Version: "1.0.0", Tasks: []string{"a"}},
		{ID: "longmemeval", Version: "1.0.0"},
		{ID: "hd-harbor-tb2", Version: "2.0.0"},
	}}
}

func TestCatalogPaging(t *testing.T) {
	c := newTestClient(t, testDeps{index: testIndex(), analyzer: &fakeAnalyzer{}, launcher: &fakeLauncher{}, catalog: g3Catalog{}})
	plain := decodeBody(t, c.do(t, "GET", "/api/bench/catalog", nil, nil))
	if _, ok := plain["total"]; ok || len(listAt(plain, "suites")) != 3 {
		t.Fatalf("plain %v", plain)
	}
	body := decodeBody(t, c.do(t, "GET", "/api/bench/catalog?q=HARBOR&limit=1", nil, nil))
	if body["total"] != 2.0 || len(listAt(body, "suites")) != 1 || listAt(body, "suites")[0]["id"] != "hd-harbor-bfcl" || body["probes"] == nil {
		t.Fatalf("q=HARBOR %v", body)
	}
}

// countingSource counts the run files and seal checks asked of it.
type countingSource struct {
	leaderboardSource
	files map[string]int
	seals int
}

func (c *countingSource) RunFile(runID, name string) ([]byte, error) {
	c.files[runID+"/"+name]++
	return c.leaderboardSource.RunFile(runID, name)
}

func (c *countingSource) VerifySeal(ctx context.Context, runID string) error {
	c.seals++
	return c.leaderboardSource.VerifySeal(ctx, runID)
}

func TestLeaderboardRequestSource(t *testing.T) {
	inner := &countingSource{leaderboardSource: leaderboardSource{index: testIndex()}, files: map[string]int{}}
	src := memoLeaderboardSource(inner)
	if _, ok := src.(*requestLeaderboardSource); !ok {
		t.Fatalf("full source not wrapped: %T", src)
	}
	plain, err := leaderboard.Build(context.Background(), leaderboardSource{index: testIndex()}, leaderboard.Query{Condition: schemas.ConditionClean})
	if err != nil {
		t.Fatal(err)
	}
	var got *schemas.Leaderboard
	for range 3 {
		if got, err = leaderboard.Build(context.Background(), src, leaderboard.Query{Condition: schemas.ConditionClean}); err != nil {
			t.Fatal(err)
		}
	}
	for k, n := range inner.files {
		if n != 1 {
			t.Fatalf("%s read %d times in one request", k, n)
		}
	}
	for range 3 {
		if _, err := src.RunFile("r_t01_A", FileMetrics); err != nil {
			t.Fatal(err)
		}
		if err := src.(leaderboard.SealSource).VerifySeal(context.Background(), "r_t01_A"); err != nil {
			t.Fatal(err)
		}
		_, _ = src.RunFile("r_t01_A", "missing.json")
	}
	if inner.files["r_t01_A/"+FileMetrics] != 1 || inner.files["r_t01_A/missing.json"] != 1 || inner.seals > 1+len(testIndex().runs) {
		t.Fatalf("reads %v seals %d", inner.files, inner.seals)
	}
	// A file over memoFileMax is read every time.
	inner.index.(*fakeIndex).files["r_t01_A"]["big.json"] = make([]byte, memoFileMax+1)
	for range 2 {
		_, _ = src.RunFile("r_t01_A", "big.json")
	}
	if inner.files["r_t01_A/big.json"] != 2 {
		t.Fatalf("big file reads %d", inner.files["r_t01_A/big.json"])
	}
	a, _ := json.Marshal(plain.Entries)
	b, _ := json.Marshal(got.Entries)
	if string(a) != string(b) {
		t.Fatalf("board differs:\n%s\n%s", a, b)
	}
	// A source without every optional interface is used as it is.
	type bare struct{ leaderboard.Source }
	if s := memoLeaderboardSource(bare{inner}); func() bool { _, ok := s.(*requestLeaderboardSource); return ok }() {
		t.Fatal("bare source wrapped")
	}
}
