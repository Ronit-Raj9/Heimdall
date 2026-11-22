package reportcard

// Comparisons pinned to a dataset version (lane Y07). An evaluation's
// child records the benchmark version it ran: suite version, suite.lock
// hash, task ids and task content hashes (schemas.EvaluationChild, fixed at
// launch; read from the child's manifest for an evaluation from before).
// A gate or release that compares with another evaluation compares, per
// benchmark, only the tasks both ran with the same content, and says so.

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// BenchmarkID is a child benchmark's suite id ("support@1.3.0" is support).
func BenchmarkID(b string) string {
	id, _, _ := strings.Cut(b, "@")
	return id
}

// childDataset is the dataset one child ran.
type childDataset struct {
	version, lock string
	tasks         map[string]string // task id -> content hash ("" unknown)
}

// ChildDataset reads the dataset of child c: its own record, else its
// manifest (an evaluation launched before children recorded it). ok is
// false when neither knows which tasks ran.
func ChildDataset(src leaderboard.Source, c schemas.EvaluationChild) (version, lock string, tasks map[string]string, ok bool) {
	d, ok := childData(src, c)
	return d.version, d.lock, d.tasks, ok
}

func childData(src leaderboard.Source, c schemas.EvaluationChild) (childDataset, bool) {
	d := childDataset{version: c.SuiteVersion, lock: c.LockHash, tasks: map[string]string{}}
	for _, id := range c.Tasks {
		d.tasks[id] = c.TaskHashes[id]
	}
	if len(d.tasks) > 0 {
		return d, true
	}
	if src == nil || c.ExperimentID == "" {
		return d, false
	}
	m, ok := src.Manifest(c.ExperimentID)
	if !ok || m == nil {
		return d, false
	}
	d = manifestDataset(m)
	return d, len(d.tasks) > 0
}

// manifestDataset is the dataset a manifest froze: its suite version and
// lock hash, and every counted task with its content hash.
func manifestDataset(m *schemas.Manifest) childDataset {
	d := childDataset{version: m.SuiteVersion, lock: m.SuiteLockHash, tasks: map[string]string{}}
	for _, ti := range m.TaskIdentities {
		if ti.TaskID != "" && (d.tasks[ti.TaskID] == "" || ti.TaskHash != "") {
			d.tasks[ti.TaskID] = ti.TaskHash
		}
	}
	for _, sl := range m.Slots {
		if sl.Counted && sl.Task != "" {
			if _, ok := d.tasks[sl.Task]; !ok {
				d.tasks[sl.Task] = ""
			}
		}
	}
	return d
}

// ChildDatasetFromManifest fills a child's dataset fields from the
// manifest it launched (evaluation launch).
func ChildDatasetFromManifest(c *schemas.EvaluationChild, m *schemas.Manifest) {
	if c == nil || m == nil {
		return
	}
	d := manifestDataset(m)
	c.SuiteVersion, c.LockHash = d.version, d.lock
	c.Tasks = slices.Sorted(maps.Keys(d.tasks))
	c.TaskHashes = map[string]string{}
	for id, h := range d.tasks {
		if h != "" {
			c.TaskHashes[id] = h
		}
	}
	if len(c.TaskHashes) == 0 {
		c.TaskHashes = nil
	}
}

// CompareDatasets compares the benchmarks candidate ran with those base
// ran. tasks is nil when nothing changed (the reports compare as they are);
// otherwise it lists, per compared benchmark, the tasks both ran with the
// same content, and a benchmark it leaves out is not compared.
func CompareDatasets(src leaderboard.Source, candidate, base *schemas.Evaluation) (*schemas.DatasetComparison, map[string][]string) {
	sides := func(ev *schemas.Evaluation) (map[string]childDataset, []string) {
		out := map[string]childDataset{}
		var order []string
		if ev == nil {
			return out, order
		}
		for _, c := range ev.Children {
			if c.Skipped != "" || c.ExperimentID == "" {
				continue
			}
			id := BenchmarkID(c.Benchmark)
			d, ok := childData(src, c)
			if !ok {
				continue
			}
			if _, seen := out[id]; !seen {
				order = append(order, id)
			}
			out[id] = d
		}
		return out, order
	}
	cand, order := sides(candidate)
	was, baseOrder := sides(base)
	for _, id := range baseOrder {
		if _, ok := cand[id]; !ok {
			order = append(order, id)
		}
	}
	cmp := &schemas.DatasetComparison{Benchmarks: []schemas.DatasetBenchmark{}}
	tasks := map[string][]string{}
	var lines []string
	for _, id := range order {
		c, inCand := cand[id]
		b, inBase := was[id]
		row := schemas.DatasetBenchmark{Benchmark: id, CandidateVersion: c.version, BaselineVersion: b.version, CandidateLock: c.lock, BaselineLock: b.lock}
		switch {
		case !inBase:
			row.Changed, row.NotCompared, row.NewTasks = true, true, len(c.tasks)
			lines = append(lines, fmt.Sprintf("%s did not run in the baseline; not compared", id))
		case !inCand:
			row.Changed, row.NotCompared, row.DroppedTasks = true, true, len(b.tasks)
			lines = append(lines, fmt.Sprintf("%s ran only in the baseline; not compared", id))
		default:
			var shared []string
			for t, h := range c.tasks {
				bh, ok := b.tasks[t]
				if ok && (h == "" || bh == "" || h == bh) {
					shared = append(shared, t)
				}
			}
			slices.Sort(shared)
			row.Compared = len(shared)
			row.NewTasks = len(c.tasks) - len(shared)
			row.DroppedTasks = len(b.tasks) - len(shared)
			row.Changed = row.NewTasks > 0 || row.DroppedTasks > 0 ||
				(c.lock != "" && b.lock != "" && c.lock != b.lock) || (c.version != "" && b.version != "" && c.version != b.version)
			switch {
			case len(shared) == 0:
				row.NotCompared = true
				lines = append(lines, fmt.Sprintf("%s: no task in common with the baseline; not compared", id))
			default:
				tasks[id] = shared
				if row.NewTasks > 0 || row.DroppedTasks > 0 {
					l := fmt.Sprintf("%s: compared on the %d tasks both ran", id, len(shared))
					if row.NewTasks > 0 {
						l += fmt.Sprintf("; %d new or changed tasks not compared", row.NewTasks)
					}
					if row.DroppedTasks > 0 {
						l += fmt.Sprintf("; %d baseline tasks no longer run", row.DroppedTasks)
					}
					lines = append(lines, l)
				} else if row.Changed {
					lines = append(lines, fmt.Sprintf("%s: version changed (%s), same %d tasks compared", id, versionPair(b, c), len(shared)))
				}
			}
		}
		if row.Changed {
			cmp.Changed = true
		}
		cmp.Benchmarks = append(cmp.Benchmarks, row)
	}
	if !cmp.Changed {
		cmp.Summary = "The baseline ran the same benchmark versions and tasks."
		return cmp, nil
	}
	cmp.Summary = "Benchmark changed since baseline: " + strings.Join(lines, ". ") + "."
	return cmp, tasks
}

func versionPair(b, c childDataset) string {
	from, to := b.version, c.version
	if from == to || from == "" || to == "" {
		from, to = shortLock(b.lock), shortLock(c.lock)
	}
	return "baseline " + from + ", now " + to
}

func shortLock(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 8 {
		return "lock " + h[:8]
	}
	if h == "" {
		return "unlocked"
	}
	return "lock " + h
}

// keepTasks drops every graded run of a task not in keep (Options.Tasks).
// Exclusion counts carry no task and are dropped with them, since they no
// longer describe the runs kept.
func (cr *childRuns) keepTasks(keep map[string]bool) {
	filterRuns := func(rs []graded) []graded {
		return slices.DeleteFunc(rs, func(g graded) bool { return !keep[g.task] })
	}
	for l, rs := range cr.arms {
		cr.arms[l] = filterRuns(rs)
	}
	for l, rs := range cr.replays {
		cr.replays[l] = filterRuns(rs)
	}
	cr.fresh = filterRuns(cr.fresh)
	cr.excluded = map[string]map[string]int{}
}
