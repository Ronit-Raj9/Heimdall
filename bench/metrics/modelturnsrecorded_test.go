package metrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestModelTurnFamilyOnRecordedRuns recomputes steps on every sealed run of
// the recorded bundle (read only) and checks it equals the value sealed in
// metrics.json, then checks the new family computes and obeys its
// identities: steps plus failed is total, total plus fallbacks plus retries
// is model_attempts.
func TestModelTurnFamilyOnRecordedRuns(t *testing.T) {
	var paths []string
	err := filepath.WalkDir(filepath.Join("..", "testdata", "recorded"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "evidence.json" {
			paths = append(paths, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("no recorded runs")
	}
	failedSomewhere := false
	for _, p := range paths {
		dir := filepath.Dir(p)
		var ev schemas.Evidence
		readJSON(t, p, &ev)
		var sealed map[string]schemas.Value
		readJSON(t, filepath.Join(dir, "metrics.json"), &sealed)
		got := turnFamily(t, &ev)
		want, ok := sealed[Steps]
		if !ok {
			t.Fatalf("%s: no sealed steps", dir)
		}
		if got[Steps].State != want.State || (want.IsObserved() && *got[Steps].V != *want.V) {
			t.Errorf("%s: steps recomputed %+v, sealed %+v", dir, got[Steps], want)
		}
		if !want.IsObserved() {
			continue
		}
		for _, n := range []string{ModelTurnsFailed, ModelTurnsTotal, ModelRetries, ModelFallbacks, ModelAttempts} {
			if !got[n].IsObserved() {
				t.Fatalf("%s: %s = %+v, want observed", dir, n, got[n])
			}
		}
		if *got[Steps].V+*got[ModelTurnsFailed].V != *got[ModelTurnsTotal].V {
			t.Errorf("%s: steps + failed != total", dir)
		}
		if *got[ModelTurnsTotal].V+*got[ModelFallbacks].V+*got[ModelRetries].V != *got[ModelAttempts].V {
			t.Errorf("%s: total + fallbacks + retries != attempts", dir)
		}
		failedSomewhere = failedSomewhere || *got[ModelTurnsFailed].V > 0
		t.Logf("%s: steps %v failed %v total %v retries %v fallbacks %v attempts %v", filepath.Base(dir),
			*got[Steps].V, *got[ModelTurnsFailed].V, *got[ModelTurnsTotal].V, *got[ModelRetries].V, *got[ModelFallbacks].V, *got[ModelAttempts].V)
	}
	if !failedSomewhere {
		t.Error("no recorded run has a failed model turn; the model-outage runs should")
	}
}
