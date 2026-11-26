package handlers

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// overheadTestSource serves run files from memory.
type overheadTestSource struct {
	gwTestSource
	files map[string][]byte // run id + "/" + file name
}

func (s overheadTestSource) RunFile(runID, name string) ([]byte, error) {
	if b, ok := s.files[runID+"/"+name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
}

// TestGatewayOverheadAnyComparison: a plain comparison (no gateway test)
// gets Bifrost's time per plugin from its clean runs, and nothing is saved.
func TestGatewayOverheadAnyComparison(t *testing.T) {
	src := overheadTestSource{gwTestSource: gwTestSource{manifests: map[string]*schemas.Manifest{"e_plain": {ExpID: "e_plain"}}, runs: map[string][]leaderboard.Run{}}, files: map[string][]byte{}}
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("r%d", i)
		src.runs["e_plain"] = append(src.runs["e_plain"], leaderboard.Run{
			Slot: schemas.Slot{SlotID: id, ExpID: "e_plain", Task: "t", Arm: "A", Condition: schemas.ConditionClean, Trial: i + 1},
			Run:  schemas.Run{RunID: id, Attempt: 1}, Sealed: true})
		var reqs []schemas.GatewayRequest
		var raw strings.Builder
		for j := 0; j < 30; j++ {
			ms := 2.0
			rid := fmt.Sprintf("%s-%d", id, j)
			reqs = append(reqs, schemas.GatewayRequest{ID: rid, Kind: "message", Status: "success", OverheadMS: &ms})
			line, _ := json.Marshal(map[string]any{"request_id": rid, "raw_available": true,
				"raw_detail": map[string]any{"overhead_breakdown": []map[string]any{{"name": "plugin.governance", "duration_us": 900}, {"name": "core", "duration_us": 1100}}}})
			raw.Write(line)
			raw.WriteByte('\n')
		}
		ev, _ := json.Marshal(map[string]any{"requests": schemas.RequestLog{Complete: true, Requests: reqs}})
		src.files[id+"/evidence.json"] = ev
		src.files[id+"/gateway.raw.jsonl"] = []byte(raw.String())
	}
	root := t.TempDir()
	r := router.New()
	h := newGatewayOverheadHandler(OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode})
	h.source = func() leaderboard.Source { return src }
	h.routes(r)
	st, out := secretsCall(r.Handler, "GET", APIPrefix+"/experiments/e_plain/gateway-overhead", "")
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var e gwfeature.Effect
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	if e.State != gwfeature.StateMeasured || e.Feature != schemas.GatewayFeatureOverhead || e.ExpID != "e_plain" || e.Source.Runs != 2 || e.Source.Rows != 60 {
		t.Fatalf("effect %+v", e)
	}
	if !strings.HasPrefix(e.Headline, "Bifrost adds 2.0 ms per call") || !strings.Contains(e.Headline, "core 1.1 ms, governance 0.9 ms") {
		t.Fatalf("headline %s", e.Headline)
	}
	if list, _ := gwfeature.List(root); len(list) != 0 {
		t.Fatalf("saved %d effects", len(list))
	}
	if st, _ := secretsCall(r.Handler, "GET", APIPrefix+"/experiments/e_missing/gateway-overhead", ""); st != 404 {
		t.Fatalf("missing comparison: %d", st)
	}
}
