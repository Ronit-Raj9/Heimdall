package handlers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// This file pins every JSON route to the key sets of the TypeScript types the
// UI reads (ui/lib/types/bench.ts), checked in under testdata/contract as one
// <Type>.keys file per type. A handler that drifts from the contract, or a
// type that changes without its key list, fails here. The UI's fixture test
// reads the same files.

// contractDir holds the key lists.
const contractDir = "testdata/contract"

// keyList is one type's keys: required, plus optional (omitempty) ones.
type keyList struct {
	required, optional []string
}

// loadKeys reads testdata/contract/<typ>.keys: one key per line, a trailing
// "?" for an optional key, "#" for a comment.
func loadKeys(t *testing.T, typ string) keyList {
	t.Helper()
	f, err := os.Open(filepath.Join(contractDir, typ+".keys"))
	if err != nil {
		t.Fatalf("open key list %s: %v", typ, err)
	}
	defer f.Close()
	var kl keyList
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasSuffix(line, "?"):
			kl.optional = append(kl.optional, strings.TrimSuffix(line, "?"))
		default:
			kl.required = append(kl.required, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read key list %s: %v", typ, err)
	}
	return kl
}

// checkKeys compares one JSON object's keys with a key list. Top-level
// responses also carry "mode".
func checkKeys(t *testing.T, where string, obj map[string]any, kl keyList, topLevel bool) {
	t.Helper()
	required := kl.required
	if topLevel && !slices.Contains(required, "mode") {
		required = append(slices.Clone(required), "mode")
	}
	for _, k := range required {
		if _, ok := obj[k]; !ok {
			t.Errorf("%s: missing key %q", where, k)
		}
	}
	for k := range obj {
		if !slices.Contains(required, k) && !slices.Contains(kl.optional, k) {
			t.Errorf("%s: key %q is not in the contract", where, k)
		}
	}
}

// objectsAt walks a path of object keys, where "[]" after a key means every
// element of that array, and returns the objects at its end.
func objectsAt(t *testing.T, root map[string]any, path string) []map[string]any {
	t.Helper()
	cur := []map[string]any{root}
	for _, seg := range strings.Split(path, ".") {
		key, each := strings.CutSuffix(seg, "[]")
		var next []map[string]any
		for _, obj := range cur {
			v := obj[key]
			if !each {
				if o, ok := v.(map[string]any); ok {
					next = append(next, o)
				}
				continue
			}
			arr, _ := v.([]any)
			for _, el := range arr {
				if o, ok := el.(map[string]any); ok {
					next = append(next, o)
				}
			}
		}
		cur = next
	}
	if len(cur) == 0 {
		t.Fatalf("no object at %q", path)
	}
	return cur
}

// contractIndex is a small run folder with every cell state: sealed counted
// runs, a run still in flight (s_t02_A) and a slot with no run (s_t02_B).
func contractIndex() *fakeIndex {
	idx := testIndex()
	var runs []RunView
	for _, rv := range idx.runs {
		switch rv.Slot.SlotID {
		case "s_t02_B":
			continue
		case "s_t02_A":
			rv.Sealed, rv.SealHash = false, ""
		}
		runs = append(runs, rv)
	}
	idx.runs = runs
	idx.files["r_t01_A"][FileGrade] = []byte(`{"passed":false,"exit_code":1}`)
	return idx
}

func TestResponsesMatchContract(t *testing.T) {
	d := fullDeps()
	d.index = contractIndex()
	c := newTestClient(t, d)
	plan := []byte(`{"name":"demo","conditions":[{"id":"clean","tasks":{"all":true},"trials":1}]}`)
	tests := []struct {
		name, method, path string
		body               []byte
		status             int
		typ                string
		nested             map[string]string // path -> type
	}{
		{"health", "GET", "/health", nil, 200, "Health", nil},
		{"doctor", "GET", "/doctor", nil, 200, "Doctor", nil},
		{"catalog", "GET", "/catalog", nil, 200, "Catalog", nil},
		{"plan", "POST", "/experiments/plan", plan, 200, "ExperimentPlan", nil},
		{"plan refused", "POST", "/experiments/plan", []byte(`{"name":"bad"}`), 200, "ExperimentPlan", nil},
		{"create", "POST", "/experiments", plan, 201, "ExperimentCreated", nil},
		// The verdict runs the analysis, so the status routes after it carry analysis_id.
		{"verdict", "GET", "/experiments/e_test/verdict", nil, 200, "VerdictPayload", map[string]string{"verdict": "Verdict"}},
		{"status", "GET", "/experiments/e_test", nil, 200, "ExperimentStatus", map[string]string{"slots": "SlotProgress"}},
		{"list", "GET", "/experiments", nil, 200, "ExperimentList", map[string]string{"experiments[]": "ExperimentStatus"}},
		{"start", "POST", "/experiments/e_test/start", nil, 202, "ExperimentStatus", nil},
		{"events poll", "GET", "/experiments/e_test/events?since=0", nil, 200, "EventsPoll", map[string]string{"status": "ExperimentStatus"}},
		{"diagnostics", "GET", "/experiments/e_test/diagnostics", nil, 200, "DiagnosticsPayload", nil},
		{"grid", "GET", "/experiments/e_test/grid", nil, 200, "Grid",
			map[string]string{"rows[]": "GridRow", "rows[].cells[]": "GridCell"}},
		{"run", "GET", "/runs/r_t01_A", nil, 200, "RunDetail", nil},
		{"run in flight", "GET", "/runs/r_t02_A", nil, 200, "RunDetail", nil},
		{"run events", "GET", "/runs/r_t01_A/events", nil, 200, "RunEvents", nil},
		{"run diff", "GET", "/runs/r_t01_A/diff", nil, 200, "RunDiff", nil},
		{"run receipts", "GET", "/runs/r_t01_A/receipts", nil, 200, "RunReceipts", nil},
		{"pair", "GET", "/pairs/p_t01", nil, 200, "Pair", map[string]string{"runs[]": "PairRun"}},
		{"audit", "POST", "/audit", []byte(`{"rows":[{"task":"t01","a_pass":1,"a_n":3,"b_pass":2,"b_n":3}]}`), 200, "Verdict", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var headers map[string]string
			if tt.method == "POST" {
				headers = authed("application/json")
			}
			resp := c.do(t, tt.method, APIPrefix+tt.path, tt.body, headers)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			var body map[string]any
			if err := json.Unmarshal(resp.Body(), &body); err != nil {
				t.Fatalf("decode %q: %v", resp.Body(), err)
			}
			checkKeys(t, tt.typ, body, loadKeys(t, tt.typ), true)
			for path, typ := range tt.nested {
				for i, obj := range objectsAt(t, body, path) {
					checkKeys(t, fmt.Sprintf("%s.%s #%d", tt.typ, path, i), obj, loadKeys(t, typ), false)
				}
			}
		})
	}
}

func TestContractValues(t *testing.T) {
	d := fullDeps()
	d.index = contractIndex()
	c := newTestClient(t, d)
	get := func(path string) map[string]any {
		return decodeBody(t, c.do(t, "GET", APIPrefix+path, nil, nil))
	}
	t.Run("grid cells are null until a counted run exists", func(t *testing.T) {
		cells := map[string]map[string]any{}
		for _, cell := range objectsAt(t, get("/experiments/e_test/grid"), "rows[].cells[]") {
			cells[cell["slot_id"].(string)] = cell
		}
		for _, slot := range []string{"s_t02_A", "s_t02_B"} {
			if cells[slot]["run_id"] != nil || cells[slot]["outcome"] != nil {
				t.Errorf("%s = %v, want null run_id and outcome", slot, cells[slot])
			}
		}
		if cells["s_t01_A"]["run_id"] != "r_t01_A" || cells["s_t01_A"]["outcome"] != "fail" {
			t.Errorf("s_t01_A = %v", cells["s_t01_A"])
		}
	})
	t.Run("status counts slots and sums spend", func(t *testing.T) {
		st := get("/experiments/e_test")
		slots, _ := st["slots"].(map[string]any)
		want := map[string]any{"total": float64(4), "done": float64(2), "running": float64(1), "failed": float64(0)}
		for k, v := range want {
			if slots[k] != v {
				t.Errorf("slots.%s = %v, want %v", k, slots[k], v)
			}
		}
		// The fake runs' metrics carry no cost_usd: spend is unknown, never 0.
		spent, _ := st["spent_usd"].(map[string]any)
		if st["state"] != string(schemas.ExperimentRunning) || spent["state"] != "unknown" || spent["reason"] != "cost_not_measured" {
			t.Errorf("status = %v", st)
		}
	})
	t.Run("pair runs carry metrics", func(t *testing.T) {
		for _, run := range objectsAt(t, get("/pairs/p_t01"), "runs[]") {
			if m, _ := run["metrics"].(map[string]any); m["steps"] == nil {
				t.Errorf("pair run = %v", run)
			}
		}
	})
}
