// Package bifrostfeatures holds the experiment-level checks of Bifrost
// features under test (B2, docs/bifrost-features.md) that need more than one
// run: which semantic cache hits replayed a reply another run stored, and
// whether that run worked on another task.
package bifrostfeatures

import (
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RunRows is one sealed run's gateway rows with what identifies it.
type RunRows struct {
	RunID, Task, Arm, Condition string
	Trial                       int
	Rows                        []schemas.GatewayRequest
}

// Replay is one semantic cache hit traced to the run that stored its entry.
type Replay struct {
	Row        string `json:"row"`      // the hit's Bifrost row id
	CacheID    string `json:"cache_id"` // the entry it was served from
	HitRun     string `json:"hit_run"`
	HitTask    string `json:"hit_task"`
	HitArm     string `json:"hit_arm"`
	StoredRun  string `json:"stored_run,omitempty"` // empty when no run of the experiment stored the entry
	StoredTask string `json:"stored_task,omitempty"`
	StoredArm  string `json:"stored_arm,omitempty"`
	Scope      string `json:"scope"` // same_run | same_task | cross_task | unattributed
}

// ReplayReport counts an experiment's semantic cache hits by where their
// entry came from. CrossTask above zero means an answer stored for one task
// was served to another: the cached reply is evidence about the first task,
// not the second.
type ReplayReport struct {
	Hits         int      `json:"hits"`
	SameRun      int      `json:"same_run"`
	SameTask     int      `json:"same_task"` // another trial or arm of the same task
	CrossTask    int      `json:"cross_task"`
	Unattributed int      `json:"unattributed"` // no cache_id, or an entry stored outside the experiment
	Replays      []Replay `json:"replays"`
}

// CacheReplays traces every cache hit of runs to the miss row that stored
// its entry (Bifrost v2.5.1 logs the entry's cache_id on both). An entry
// stored by several runs is attributed to the first in run order.
func CacheReplays(runs []RunRows) ReplayReport {
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
	stored := map[string]*RunRows{}
	for i := range runs {
		for _, r := range runs[i].Rows {
			if !r.CacheHit && r.CacheID != "" {
				if _, seen := stored[r.CacheID]; !seen {
					stored[r.CacheID] = &runs[i]
				}
			}
		}
	}
	var rep ReplayReport
	for i := range runs {
		run := &runs[i]
		for _, r := range run.Rows {
			if !r.CacheHit {
				continue
			}
			rep.Hits++
			rp := Replay{Row: r.ID, CacheID: r.CacheID, HitRun: run.RunID, HitTask: run.Task, HitArm: run.Arm}
			src := stored[r.CacheID]
			switch {
			case r.CacheID == "" || src == nil:
				rp.Scope = "unattributed"
				rep.Unattributed++
			default:
				rp.StoredRun, rp.StoredTask, rp.StoredArm = src.RunID, src.Task, src.Arm
				switch {
				case src.RunID == run.RunID:
					rp.Scope = "same_run"
					rep.SameRun++
				case src.Task == run.Task:
					rp.Scope = "same_task"
					rep.SameTask++
				default:
					rp.Scope = "cross_task"
					rep.CrossTask++
				}
			}
			rep.Replays = append(rep.Replays, rp)
		}
	}
	return rep
}
