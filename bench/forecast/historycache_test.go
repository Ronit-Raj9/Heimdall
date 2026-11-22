package forecast

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// countingStore counts how often the store is scanned.
type countingStore struct {
	fakeStore
	lists int
}

func (s *countingStore) ListExperiments(ctx context.Context) ([]string, error) {
	s.lists++
	return s.fakeStore.ListExperiments(ctx)
}

func TestHistoryCacheTTLAndFingerprint(t *testing.T) {
	cfg := schemas.AgentConfig{ID: "codex", Model: "m"}
	st := &countingStore{fakeStore: fakeStore{manifests: map[string]*schemas.Manifest{}, runs: map[string][]schemas.SealedRun{}, opened: map[string]bool{}}}
	pm, pr := pastExperiment("e_past", "s", cfg, "h1", 0.1, 0.2)
	st.manifests["e_past"], st.runs["e_past"] = pm, pr
	om, or := pastExperiment("e_other", "s", schemas.AgentConfig{ID: "other", Model: "x"}, "h9", 0.5)
	st.manifests["e_other"], st.runs["e_other"] = om, or
	m := &schemas.Manifest{ExpID: "e_new", SuiteID: "s", SuiteVersion: "1",
		Arms: map[string]schemas.ArmManifest{"A": {ConfigHash: "h1", Config: cfg}}}

	now := time.Unix(1000, 0)
	c := NewHistoryCache(time.Minute)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	s, hit, err := c.Suite(ctx, st, "root", "fp1", m, nil)
	if err != nil || hit {
		t.Fatalf("first: hit %v err %v", hit, err)
	}
	h, err := s.History(m)
	if err != nil || len(h.Runs) != 2 {
		t.Fatalf("history %+v err %v, want the 2 runs of the relevant experiment", h, err)
	}
	direct, _ := LoadHistory(ctx, &st.fakeStore, m, nil)
	if len(direct.Runs) != len(h.Runs) {
		t.Fatalf("cached history %d runs, direct %d", len(h.Runs), len(direct.Runs))
	}
	if lib, err := s.Library(m); err != nil || len(lib.Runs) != 3 {
		t.Fatalf("library %+v err %v, want all 3 runs", lib, err)
	}
	lists := st.lists
	if _, hit, _ := c.Suite(ctx, st, "root", "fp1", m, nil); !hit || st.lists != lists {
		t.Fatalf("second: hit %v scans %d", hit, st.lists-lists)
	}
	if _, hit, _ := c.Suite(ctx, st, "root", "fp2", m, nil); hit {
		t.Fatal("a changed fingerprint was served from the cache")
	}
	now = now.Add(61 * time.Second)
	if _, hit, _ := c.Suite(ctx, st, "root", "fp2", m, nil); hit {
		t.Fatal("an expired entry was served")
	}
	if _, hit, _ := c.Suite(ctx, st, "", "fp2", m, nil); hit {
		t.Fatal("a store with no key was cached")
	}
}
