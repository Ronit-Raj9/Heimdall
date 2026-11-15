package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// clusterIndex is testIndex that also implements clusters.Source: its
// sealed runs come back as verified, with grade, gates and metrics read
// from the fake files, and r_t02_B fails too (a timeout).
type clusterIndex struct {
	*fakeIndex
	problems map[string]string // run id -> problem, as SealedRuns reports a seal mismatch
}

func newClusterIndex() clusterIndex {
	idx := testIndex()
	idx.files["r_t01_A"][FileGrade] = []byte(`{"passed":false,"failed_tests":["tests.test_limiter::test_window"],"suite_version":"1.0.0","grader_version":"1.0.0","diff_sha256":"x"}`)
	idx.files["r_t01_A"][clusters.FileStderr] = []byte("Traceback (most recent call last):\n  File \"/workspace/app.py\", line 12\nValueError: bad window 42\n")
	for i := range idx.runs {
		idx.runs[i].Run.Timing.Started = time.Date(2026, 11, 14, 23, 50, i, 0, time.UTC)
		if idx.runs[i].Run.RunID == "r_t02_B" {
			idx.runs[i].Run.Outcome = schemas.OutcomeTimeout
		}
	}
	return clusterIndex{fakeIndex: idx, problems: map[string]string{}}
}

// VerifiedRuns implements clusters.Source.
func (c clusterIndex) VerifiedRuns(_ context.Context, expID string) ([]schemas.SealedRun, error) {
	var out []schemas.SealedRun
	for _, rv := range c.Runs(expID) {
		if !rv.Sealed {
			continue
		}
		sr := schemas.SealedRun{Slot: rv.Slot, Run: rv.Run, SealHash: rv.SealHash, Metrics: map[string]schemas.Value{}}
		if p, ok := c.problems[rv.Run.RunID]; ok {
			sr.Problems = []string{p}
			out = append(out, sr)
			continue
		}
		files := c.files[rv.Run.RunID]
		if data, ok := files[FileGrade]; ok {
			sr.Grade = &schemas.Grade{}
			if err := json.Unmarshal(data, sr.Grade); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(files[FileGates], &sr.Gates); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, nil
}

// RunEvidence implements clusters.Source.
func (c clusterIndex) RunEvidence(runID, name string) ([]byte, error) {
	return c.RunFile(runID, name)
}

// getClustersPath serves one clusters request on a fresh router.
func getClustersPath(t *testing.T, index ReadIndex, path string) (int, map[string]any) {
	t.Helper()
	h := NewClustersHandler(testMode, index)
	h.now = func() time.Time { return time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC) }
	r := router.New()
	h.RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + path)
	r.Handler(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
	}
	if body["mode"] != string(testMode) {
		t.Errorf("mode = %v", body["mode"])
	}
	return ctx.Response.StatusCode(), body
}

func TestClustersRoute(t *testing.T) {
	tests := []struct {
		name   string
		index  ReadIndex
		query  string
		status int
		count  int // clusters listed
	}{
		{name: "defaults", index: newClusterIndex(), query: "", status: fasthttp.StatusOK, count: 2},
		{name: "filters", index: newClusterIndex(), query: "?suite=coding-core&exp=e_test&exp=e_other&since=7d&min=1&synthetic=include", status: fasthttp.StatusOK, count: 2},
		{name: "min hides small clusters", index: newClusterIndex(), query: "?min=2", status: fasthttp.StatusOK, count: 0},
		{name: "other suite", index: newClusterIndex(), query: "?suite=other@1.0.0", status: fasthttp.StatusOK, count: 0},
		{name: "bad synthetic", index: newClusterIndex(), query: "?synthetic=maybe", status: fasthttp.StatusBadRequest},
		{name: "bad min", index: newClusterIndex(), query: "?min=two", status: fasthttp.StatusBadRequest},
		{name: "bad since", index: newClusterIndex(), query: "?since=yesterday", status: fasthttp.StatusBadRequest},
		{name: "no index", index: nil, query: "", status: fasthttp.StatusServiceUnavailable},
		{name: "index without verified runs", index: testIndex(), query: "", status: fasthttp.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := getClustersPath(t, tt.index, "/clusters"+tt.query)
			if status != tt.status {
				t.Fatalf("status = %d, want %d: %v", status, tt.status, body)
			}
			if status != fasthttp.StatusOK {
				return
			}
			checkClusterList(t, body)
			if got := len(listAt(body, "clusters")); got != tt.count {
				t.Fatalf("clusters %d, want %d", got, tt.count)
			}
		})
	}
}

func TestClusterDetailRoute(t *testing.T) {
	index := newClusterIndex()
	_, list := getClustersPath(t, index, "/clusters")
	cs := listAt(list, "clusters")
	if len(cs) == 0 {
		t.Fatal("no clusters")
	}
	sig, _ := cs[0]["signature"].(string)
	t.Run("found, min does not hide it", func(t *testing.T) {
		status, body := getClustersPath(t, index, "/clusters/"+sig+"?min=5")
		if status != fasthttp.StatusOK {
			t.Fatalf("status %d: %v", status, body)
		}
		checkKeys(t, "ClusterDetail", body, loadKeys(t, "ClusterDetail"), true)
		cluster, _ := body["cluster"].(map[string]any)
		checkCluster(t, "ClusterDetail.cluster", cluster)
		for i, r := range listAt(body, "runs") {
			checkKeys(t, fmt.Sprintf("ClusterRun #%d", i), r, loadKeys(t, "ClusterRun"), false)
		}
	})
	t.Run("unknown signature", func(t *testing.T) {
		if status, _ := getClustersPath(t, index, "/clusters/c_0000000000000000"); status != fasthttp.StatusNotFound {
			t.Fatalf("status %d, want 404", status)
		}
	})
	t.Run("invalid signature", func(t *testing.T) {
		if status, _ := getClustersPath(t, index, "/clusters/a%20b"); status != fasthttp.StatusNotFound {
			t.Fatalf("status %d, want 404", status)
		}
	})
	t.Run("seal mismatch is excluded", func(t *testing.T) {
		bad := newClusterIndex()
		bad.problems["r_t01_A"] = "seal does not verify: file grade.json changed"
		status, body := getClustersPath(t, bad, "/clusters")
		if status != fasthttp.StatusOK {
			t.Fatalf("status %d", status)
		}
		if got := len(listAt(body, "clusters")); got != 1 {
			t.Fatalf("clusters %d, want 1 (the timeout)", got)
		}
		ex := listAt(body, "excluded")
		if len(ex) != 1 || ex[0]["reason"] != clusters.ReasonSealFailed {
			t.Fatalf("excluded %v", ex)
		}
	})
}

// checkClusterList checks a ClusterList body and its nested objects
// against the contract key lists.
func checkClusterList(t *testing.T, body map[string]any) {
	t.Helper()
	checkKeys(t, "ClusterList", body, loadKeys(t, "ClusterList"), true)
	filters, _ := body["filters"].(map[string]any)
	checkKeys(t, "ClusterFilters", filters, loadKeys(t, "ClusterFilters"), false)
	for i, x := range listAt(body, "excluded") {
		checkKeys(t, fmt.Sprintf("ClusterExclusion #%d", i), x, loadKeys(t, "ClusterExclusion"), false)
	}
	for i, c := range listAt(body, "clusters") {
		checkCluster(t, fmt.Sprintf("ClusterSummary #%d", i), c)
	}
}

// checkCluster checks one ClusterSummary and its layers, related clusters
// and config change.
func checkCluster(t *testing.T, where string, c map[string]any) {
	t.Helper()
	checkKeys(t, where, c, loadKeys(t, "ClusterSummary"), false)
	for i, l := range listAt(c, "layers") {
		checkKeys(t, fmt.Sprintf("%s.layers #%d", where, i), l, loadKeys(t, "ClusterLayer"), false)
	}
	for i, r := range listAt(c, "related") {
		checkKeys(t, fmt.Sprintf("%s.related #%d", where, i), r, loadKeys(t, "ClusterRelated"), false)
	}
	if cc, ok := c["after_config_change"].(map[string]any); ok {
		checkKeys(t, where+".after_config_change", cc, loadKeys(t, "ClusterConfigChange"), false)
	}
}

// listAt returns the objects of an array field, none when it is absent or
// empty.
func listAt(obj map[string]any, key string) []map[string]any {
	items, _ := obj[key].([]any)
	var out []map[string]any
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
