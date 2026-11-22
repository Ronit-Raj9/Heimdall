package engine

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// N148: the status page's worker line reads the shared pool only.
func TestStatsCountsSharedWorkers(t *testing.T) {
	c := &Coordinator{now: time.Now, workers: map[string]*remoteWorker{
		"s1": {info: schemas.WorkerInfo{ID: "s1", State: "running", Pool: schemas.SharedRunnerPool}},
		"s2": {info: schemas.WorkerInfo{ID: "s2", State: "lost", Pool: schemas.SharedRunnerPool}},
		"o1": {info: schemas.WorkerInfo{ID: "o1", State: "lost", Pool: "default"}},
		"o2": {info: schemas.WorkerInfo{ID: "o2", State: "idle"}},
	}}
	st := c.Stats()
	if st.Workers["lost"] != 2 || st.Workers["idle"] != 1 || st.Workers["running"] != 1 {
		t.Fatalf("all workers %v", st.Workers)
	}
	if len(st.SharedWorkers) != 2 || st.SharedWorkers["running"] != 1 || st.SharedWorkers["lost"] != 1 {
		t.Fatalf("shared workers %v", st.SharedWorkers)
	}
}
