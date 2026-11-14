package handlers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// triageIndex is testIndex with pattern values in each run's metrics.json:
// r_t02_A was sealed before the pattern metrics existed, so it has none.
func triageIndex() *fakeIndex {
	idx := testIndex()
	idx.files["r_t01_A"][FileMetrics] = []byte(`{
		"pattern_loop": {"state":"observed","v":1,"evidence":"gateway","reason":"events: r_t01_A:gateway:3,r_t01_A:gateway:4; Bash \"ls\" repeated 3 times in a row"},
		"pattern_unverified_finish": {"state":"observed","v":0,"evidence":"gateway"},
		"pattern_ignored_error": {"state":"unknown","v":null,"evidence":"native","reason":"no agent_stream events"},
		"false_completion": {"state":"observed","v":1,"evidence":"native"}}`)
	idx.files["r_t01_B"][FileMetrics] = []byte(`{
		"pattern_loop": {"state":"observed","v":0,"evidence":"gateway"},
		"pattern_unverified_finish": {"state":"observed","v":1,"evidence":"gateway","reason":"events: r_t01_B:gateway:9; success claimed"},
		"pattern_ignored_error": {"state":"observed","v":1,"evidence":"native","reason":"events: r_t01_B:agent_stream:2; ignored"},
		"false_completion": {"state":"not_applicable","v":null}}`)
	idx.files["r_t02_B"][FileMetrics] = []byte(`{
		"pattern_loop": {"state":"observed","v":1,"evidence":"gateway"},
		"pattern_unverified_finish": {"state":"observed","v":1,"evidence":"gateway"},
		"pattern_ignored_error": {"state":"observed","v":0,"evidence":"native"},
		"false_completion": {"state":"not_applicable","v":null}}`)
	return idx
}

func TestBuildTriage(t *testing.T) {
	r := func(num, den, unknown int) schemas.Ratio { return schemas.Ratio{Num: num, Den: den, Unknown: unknown} }
	unsealed := triageIndex()
	for i := range unsealed.runs {
		if unsealed.runs[i].Run.RunID == "r_t02_B" {
			unsealed.runs[i].Sealed = false
		}
	}
	type want struct {
		perArm map[string]schemas.Ratio
		runs   map[string][]string
	}
	tests := []struct {
		name  string
		index *fakeIndex
		want  map[string]want
	}{
		{
			name:  "ratios and runs per arm",
			index: triageIndex(),
			want: map[string]want{
				"pattern_loop": {perArm: map[string]schemas.Ratio{"A": r(1, 1, 1), "B": r(1, 2, 0)},
					runs: map[string][]string{"A": {"r_t01_A"}, "B": {"r_t02_B"}}},
				"pattern_unverified_finish": {perArm: map[string]schemas.Ratio{"A": r(0, 1, 1), "B": r(2, 2, 0)},
					runs: map[string][]string{"A": {}, "B": {"r_t01_B", "r_t02_B"}}},
				"pattern_ignored_error": {perArm: map[string]schemas.Ratio{"A": r(0, 0, 2), "B": r(1, 2, 0)},
					runs: map[string][]string{"A": {}, "B": {"r_t01_B"}}},
				"false_completion": {perArm: map[string]schemas.Ratio{"A": r(1, 1, 1), "B": r(0, 0, 0)},
					runs: map[string][]string{"A": {"r_t01_A"}, "B": {}}},
			},
		},
		{
			name:  "a run in flight counts nowhere",
			index: unsealed,
			want: map[string]want{
				"pattern_loop": {perArm: map[string]schemas.Ratio{"A": r(1, 1, 1), "B": r(0, 1, 0)},
					runs: map[string][]string{"A": {"r_t01_A"}, "B": {}}},
				"pattern_unverified_finish": {perArm: map[string]schemas.Ratio{"A": r(0, 1, 1), "B": r(1, 1, 0)},
					runs: map[string][]string{"A": {}, "B": {"r_t01_B"}}},
				"pattern_ignored_error": {perArm: map[string]schemas.Ratio{"A": r(0, 0, 2), "B": r(1, 1, 0)},
					runs: map[string][]string{"A": {}, "B": {"r_t01_B"}}},
				"false_completion": {perArm: map[string]schemas.Ratio{"A": r(1, 1, 1), "B": r(0, 0, 0)},
					runs: map[string][]string{"A": {"r_t01_A"}, "B": {}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildTriage(tt.index, testManifest(), tt.index.Runs(testExp), nil)
			if err != nil {
				t.Fatalf("BuildTriage: %v", err)
			}
			if got.ExpID != testExp || len(got.Groups) != len(triagePatterns) {
				t.Fatalf("triage = %+v", got)
			}
			for i, g := range got.Groups {
				if g.Pattern != triagePatterns[i] || g.Label == "" || g.Description == "" {
					t.Errorf("group %d = %q label %q", i, g.Pattern, g.Label)
				}
				w := tt.want[g.Pattern]
				if !reflect.DeepEqual(g.PerArm, w.perArm) {
					t.Errorf("%s per_arm = %v, want %v", g.Pattern, g.PerArm, w.perArm)
				}
				if !reflect.DeepEqual(g.Runs, w.runs) {
					t.Errorf("%s runs = %v, want %v", g.Pattern, g.Runs, w.runs)
				}
			}
		})
	}
}

func TestTriageLabels(t *testing.T) {
	custom := &schemas.Catalog{Metrics: []schemas.MetricDescriptor{{Name: "pattern_loop", Label: "Loop (catalog)", Description: "From the catalog."}}}
	tests := []struct {
		name      string
		catalog   *schemas.Catalog
		label     string
		described string
	}{
		{name: "fallback without a catalog", catalog: nil, label: "Loop", described: triageFallback["pattern_loop"].description},
		{name: "catalog descriptor wins", catalog: custom, label: "Loop (catalog)", described: "From the catalog."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, desc := triageLabel("pattern_loop", tt.catalog)
			if label != tt.label || desc != tt.described {
				t.Errorf("label %q description %q", label, desc)
			}
		})
	}
}

func TestTriageRoute(t *testing.T) {
	broken := triageIndex()
	broken.files["r_t01_A"][FileMetrics] = []byte(`{not json`)
	tests := []struct {
		name   string
		index  ReadIndex
		path   string
		status int
	}{
		{name: "ok", index: triageIndex(), path: "/experiments/e_test/triage", status: fasthttp.StatusOK},
		{name: "unknown experiment", index: triageIndex(), path: "/experiments/e_nope/triage", status: fasthttp.StatusNotFound},
		{name: "invalid id", index: triageIndex(), path: "/experiments/a%20b/triage", status: fasthttp.StatusNotFound},
		{name: "no index", index: nil, path: "/experiments/e_test/triage", status: fasthttp.StatusServiceUnavailable},
		{name: "unreadable metrics", index: broken, path: "/experiments/e_test/triage", status: fasthttp.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := router.New()
			NewTriageHandler(testMode, tt.index, fakeCatalog{}).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
			var ctx fasthttp.RequestCtx
			ctx.Request.Header.SetMethod(fasthttp.MethodGet)
			ctx.Request.SetRequestURI(APIPrefix + tt.path)
			r.Handler(&ctx)
			if got := ctx.Response.StatusCode(); got != tt.status {
				t.Fatalf("status = %d, want %d: %s", got, tt.status, ctx.Response.Body())
			}
			var body map[string]any
			if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
				t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
			}
			if body["mode"] != string(testMode) {
				t.Errorf("mode = %v", body["mode"])
			}
			if tt.status != fasthttp.StatusOK {
				return
			}
			checkKeys(t, "Triage", body, loadKeys(t, "Triage"), true)
			for i, g := range objectsAt(t, body, "groups[]") {
				checkKeys(t, fmt.Sprintf("Triage.groups #%d", i), g, loadKeys(t, "TriageGroup"), false)
				runs, _ := g["runs"].(map[string]any)
				for arm, ids := range runs {
					if _, ok := ids.([]any); !ok {
						t.Errorf("group %d runs[%s] = %v, want a list", i, arm, ids)
					}
				}
			}
		})
	}
}
