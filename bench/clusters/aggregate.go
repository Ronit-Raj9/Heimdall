package clusters

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// maxExamples is how many example run ids a cluster lists.
const maxExamples = 3

// Cluster scopes.
const (
	ScopeSingleRun    = "single_run"
	ScopeAgentAndTask = "agent_and_task"
	ScopeAgent        = "agent"
	ScopeTask         = "task"
	ScopeSystemic     = "systemic"
)

// group turns the scored runs into clusters, sorted by count then
// signature, with shares, scopes, related clusters and config changes.
func group(sc *scope) []Cluster {
	bySig := map[string][]scored{}
	for _, s := range sc.failed {
		bySig[s.sig] = append(bySig[s.sig], s)
	}
	out := make([]Cluster, 0, len(bySig))
	for sig, runs := range bySig {
		out = append(out, newCluster(sig, runs, len(sc.failed)))
	}
	slices.SortFunc(out, func(x, y Cluster) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Signature, y.Signature))
	})
	relate(out)
	for i := range out {
		out[i].AfterConfigChange = configChange(bySig[out[i].Signature], sc)
	}
	return out
}

// newCluster summarizes the runs of one signature. Runs are ordered by
// start time then id; the examples are the most recent ones.
func newCluster(sig string, runs []scored, failures int) Cluster {
	slices.SortFunc(runs, func(x, y scored) int {
		return cmp.Or(x.ref.At.Compare(y.ref.At), cmp.Compare(x.ref.RunID, y.ref.RunID))
	})
	first := runs[0]
	c := Cluster{Signature: sig, Title: Title(first.layers), Outcome: first.f.Outcome, Layers: first.layers, Count: len(runs),
		FirstSeen: runs[0].ref.At.UTC(), LastSeen: runs[len(runs)-1].ref.At.UTC()}
	if failures > 0 {
		c.Share = float64(len(runs)) / float64(failures)
	}
	if first.f.Error != nil && hasLayer(first.layers, LayerError) {
		c.ErrorText = first.f.Error.Text
	}
	var tasks, agents, hashes, conds, exps []string
	for _, s := range runs {
		tasks = append(tasks, s.ref.Suite+"/"+s.ref.Task)
		agents = append(agents, s.ref.Agent)
		hashes = append(hashes, s.ref.ConfigHash)
		conds = append(conds, s.ref.Condition)
		exps = append(exps, s.ref.ExpID)
		c.runs = append(c.runs, s.ref)
	}
	c.Tasks, c.Agents, c.ConfigHashes, c.Conditions, c.Experiments = uniq(tasks), uniq(agents), uniq(hashes), uniq(conds), uniq(exps)
	c.Title = TitleIn(first.layers, oneSuite(runs), topTool(runs))
	for i := len(runs) - 1; i >= 0 && len(c.Examples) < maxExamples; i-- {
		c.Examples = append(c.Examples, runs[i].ref.RunID)
	}
	c.Scope = scopeOf(c)
	return c
}

// oneSuite is the benchmark every run of a cluster is on, "" when they are
// on more than one.
func oneSuite(runs []scored) string {
	suite := ""
	for _, s := range runs {
		switch {
		case s.ref.Suite == "":
			return ""
		case suite == "":
			suite = s.ref.Suite
		case s.ref.Suite != suite:
			return ""
		}
	}
	return suite
}

// topTool is the last tool most runs of a cluster called, ties to the
// first name in order; "" when no run recorded one.
func topTool(runs []scored) string {
	counts := map[string]int{}
	best := ""
	for _, s := range runs {
		name := s.ref.LastTool
		if name == "" {
			continue
		}
		counts[name]++
		if best == "" || counts[name] > counts[best] || (counts[name] == counts[best] && name < best) {
			best = name
		}
	}
	return best
}

// scopeOf says where a cluster lives: one run; one agent config and one
// task; one agent config (agent-specific); one task (task-specific, maybe a
// problem with the task itself); or several of both (systemic).
func scopeOf(c Cluster) string {
	switch oneHash, oneTask := len(c.ConfigHashes) == 1, len(c.Tasks) == 1; {
	case c.Count == 1:
		return ScopeSingleRun
	case oneHash && oneTask:
		return ScopeAgentAndTask
	case oneHash:
		return ScopeAgent
	case oneTask:
		return ScopeTask
	default:
		return ScopeSystemic
	}
}

// relate links clusters whose failed set is the same: same hidden tests (or
// dimensions, category, state checks) with a different error fingerprint or
// other layer. They are listed side by side as merge suggestions and never
// merged.
func relate(out []Cluster) {
	byFailed := map[string][]int{}
	for i, c := range out {
		if key := failedKey(c.Layers); key != "" {
			byFailed[key] = append(byFailed[key], i)
		}
	}
	for _, idx := range byFailed {
		if len(idx) < 2 {
			continue
		}
		for _, i := range idx {
			for _, j := range idx {
				if i == j {
					continue
				}
				out[i].Related = append(out[i].Related, Related{Signature: out[j].Signature, Title: out[j].Title,
					Count: out[j].Count, Reason: relatedReason(out[i].Layers, out[j].Layers)})
			}
		}
	}
}

// failedKey is the canonical failed set of a cluster's layers, or "".
func failedKey(layers []Layer) string {
	for _, l := range layers {
		switch l.Kind {
		case LayerFailedTests, LayerFailedDimensions, LayerAnswerCategory, LayerStateChecks:
			return l.Kind + "=" + l.key
		}
	}
	return ""
}

// relatedReason names what two clusters with the same failed set differ in.
func relatedReason(a, b []Layer) string {
	kinds := map[string]bool{}
	for _, l := range append(slices.Clone(a), b...) {
		kinds[l.Kind] = true
	}
	var diff []string
	for _, kind := range []string{LayerOutcome, LayerGates, LayerError, LayerProvider, LayerLastTool, LayerCondition} {
		if !kinds[kind] {
			continue
		}
		if layerKey(a, kind) != layerKey(b, kind) {
			diff = append(diff, kind)
		}
	}
	if len(diff) == 0 {
		return "same failed set"
	}
	if len(diff) == 1 && diff[0] == LayerError {
		return "same failed set, different error fingerprint"
	}
	return "same failed set, different " + strings.ReplaceAll(strings.Join(diff, ", "), "_", " ")
}

// layerKey returns the key of a layer kind, or "" when absent.
func layerKey(layers []Layer, kind string) string {
	for _, l := range layers {
		if l.Kind == kind {
			return l.key
		}
	}
	return ""
}

// hasLayer reports whether a kind is among the layers.
func hasLayer(layers []Layer, kind string) bool {
	return slices.ContainsFunc(layers, func(l Layer) bool { return l.Kind == kind })
}

// configChange reports that a cluster appeared after an agent config
// changed: all its runs share one config id and one config hash, an older
// hash of the same config id ran before that hash was first seen, the older
// hash ran at least one of the cluster's tasks (so its absence from the
// cluster means something), and no run of another hash has this signature.
func configChange(runs []scored, sc *scope) *ConfigChange {
	agent, hash := runs[0].agent, runs[0].hash
	tasks := map[string]bool{}
	for _, s := range runs {
		if s.agent != agent || s.hash != hash || hash == "" {
			return nil
		}
		tasks[s.ref.Suite+"/"+s.ref.Task] = true
	}
	firstSeen := map[string]time.Time{}
	ranTask := map[string]bool{}
	for _, r := range sc.seen {
		if r.Agent != agent || r.ConfigHash == "" {
			continue
		}
		if t, ok := firstSeen[r.ConfigHash]; !ok || r.At.Before(t) {
			firstSeen[r.ConfigHash] = r.At
		}
		if tasks[r.Suite+"/"+r.Task] {
			ranTask[r.ConfigHash] = true
		}
	}
	to, ok := firstSeen[hash]
	if !ok {
		return nil
	}
	var from string
	var fromAt time.Time
	for h, t := range firstSeen {
		if h == hash || !t.Before(to) || !ranTask[h] {
			continue
		}
		if from == "" || t.After(fromAt) || (t.Equal(fromAt) && h < from) {
			from, fromAt = h, t
		}
	}
	if from == "" {
		return nil
	}
	return &ConfigChange{Agent: agent, FromHash: from, ToHash: hash, At: to.UTC()}
}

// uniq returns the sorted distinct non-empty values.
func uniq(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
