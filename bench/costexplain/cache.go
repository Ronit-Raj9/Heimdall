package costexplain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/agentdiff"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Silent cache break thresholds: a drop of at least CacheDropMin in the
// cache read share, from a share of at least CacheShareMin, over at least
// CacheMinInputTokens input tokens on each side. Smaller moves are noise
// for a prompt cache that providers evict on their own schedule.
const (
	CacheDropMin         = 0.10
	CacheShareMin        = 0.10
	CacheMinInputTokens  = 1000
	cacheMinRunsOverTime = 4
)

// cacheFindings looks for a silent cache break between the two arms and,
// within each arm, between its earlier and later runs.
func cacheFindings(m *schemas.Manifest, q Query, ga, gb *agg) []CacheFinding {
	out := []CacheFinding{}
	sa, okA := shareOf(ga.allTokens)
	sb, okB := shareOf(gb.allTokens)
	if okA && okB {
		switch {
		case sa-sb >= CacheDropMin && sa >= CacheShareMin:
			out = append(out, betweenArms(m, q, q.A, q.B, ga, gb, sa, sb))
		case sb-sa >= CacheDropMin && sb >= CacheShareMin:
			out = append(out, betweenArms(m, q, q.B, q.A, gb, ga, sb, sa))
		}
	}
	for _, side := range []struct {
		arm string
		g   *agg
	}{{q.A, ga}, {q.B, gb}} {
		if f, ok := overTime(side.arm, side.g); ok {
			out = append(out, f)
		}
	}
	return out
}

// shareOf is the cache read share of tokens, and false below the minimum
// input.
func shareOf(t schemas.Tokens) (float64, bool) {
	input := t.Fresh + t.CacheRead + t.CacheWrite
	if input < CacheMinInputTokens {
		return 0, false
	}
	return float64(t.CacheRead) / float64(input), true
}

// betweenArms names the likely cause of a lower cache read share in arm
// low against arm high, from the strongest evidence down: a different
// served model (caches are per model), a different system prompt or tool
// list in the raw rows, a prompt that differs between runs of the low arm,
// different provider keys, and the config diff. With none of these it says
// the evidence does not show the cause.
func betweenArms(m *schemas.Manifest, q Query, high, low string, gh, gl *agg, sh, sl float64) CacheFinding {
	f := CacheFinding{Kind: FindingBetweenArms, Arm: low, Baseline: high, ShareBefore: sh, ShareAfter: sl, Evidence: []string{}}
	var causes []string
	if mh, ml := sortedKeys(gh.models), sortedKeys(gl.models); len(mh) > 0 && len(ml) > 0 && disjoint(mh, ml) {
		causes = append(causes, fmt.Sprintf("served model changed (%s vs %s): a prompt cache never carries across models", strings.Join(mh, ", "), strings.Join(ml, ", ")))
	}
	if ph, pl := sortedKeys(gh.prompts), sortedKeys(gl.prompts); len(ph) > 0 && len(pl) > 0 && disjoint(ph, pl) {
		causes = append(causes, fmt.Sprintf("system prompt changed: sha256 %s vs %s in the gateway's raw rows", hashList(ph), hashList(pl)))
	}
	if th, tl := sortedKeys(gh.tools), sortedKeys(gl.tools); len(th) > 0 && len(tl) > 0 && disjoint(th, tl) {
		causes = append(causes, fmt.Sprintf("tool list changed: sha256 %s vs %s in the gateway's raw rows", hashList(th), hashList(tl)))
	}
	if gh.rawRows > 0 && gl.rawRows > 0 && gh.cacheKeyRows*2 >= gh.rawRows && gl.cacheKeyRows*2 < gl.rawRows {
		causes = append(causes, fmt.Sprintf("arm %s sends a prompt_cache_key on %d of %d rows and arm %s on %d of %d: the key routes requests with a shared prefix to the same cache",
			high, gh.cacheKeyRows, gh.rawRows, low, gl.cacheKeyRows, gl.rawRows))
	}
	if varies(gl) {
		causes = append(causes, fmt.Sprintf("the system prompt of arm %s differs between runs of the same task (%d distinct hashes over %d runs): per-run text in the prompt, such as a date, a path or a run id, defeats the cache",
			low, len(gl.prompts), gl.n))
	}
	if kh, kl := sortedKeys(gh.keys), sortedKeys(gl.keys); len(kh) > 0 && len(kl) > 0 && disjoint(kh, kl) {
		causes = append(causes, fmt.Sprintf("different provider keys served the arms (%s vs %s): a cache is scoped to the provider account", strings.Join(kh, ", "), strings.Join(kl, ", ")))
	}
	f.Evidence = append(f.Evidence, configEvidence(m, q.Condition, high, low)...)
	if len(causes) == 0 {
		for _, e := range f.Evidence {
			if strings.HasPrefix(e, "config diff: "+agentdiff.GroupPrompt) || strings.HasPrefix(e, "config diff: "+agentdiff.GroupTools) {
				causes = append(causes, "the agent config's prompt or tool settings changed (config diff); the gateway rows carry no prompt detail to confirm it")
				break
			}
		}
	}
	if len(causes) == 0 {
		f.Cause = "not shown by the evidence: the served model, system prompt, tool list, keys and config are the same, so look outside it (provider cache eviction, a cache_control change the rows do not record)"
	} else {
		f.Cause = causes[0]
		f.Evidence = append(causes[1:], f.Evidence...)
	}
	f.Evidence = append(f.Evidence, fmt.Sprintf("cache read share %s in arm %s vs %s in arm %s", pct(sh), high, pct(sl), low))
	return f
}

// overTime compares the cache read share of an arm's earlier and later runs
// (by start time) and names the cause: the same prefix throughout points
// outside the agent; a changed prefix or key is named.
func overTime(arm string, g *agg) (CacheFinding, bool) {
	if len(g.runs) < cacheMinRunsOverTime {
		return CacheFinding{}, false
	}
	half := len(g.runs) / 2
	early, late := g.runs[:half], g.runs[half:]
	se, okE := shareOf(runTokens(early))
	sl, okL := shareOf(runTokens(late))
	if !okE || !okL || se-sl < CacheDropMin || se < CacheShareMin {
		return CacheFinding{}, false
	}
	f := CacheFinding{Kind: FindingOverTime, Arm: arm, Baseline: fmt.Sprintf("earlier runs of %s", arm), ShareBefore: se, ShareAfter: sl, Evidence: []string{}}
	pe, pl := hashesOf(early, func(ev *RunEvidence) map[string]int { return ev.Prompts }), hashesOf(late, func(ev *RunEvidence) map[string]int { return ev.Prompts })
	te, tl := hashesOf(early, func(ev *RunEvidence) map[string]int { return ev.Tools }), hashesOf(late, func(ev *RunEvidence) map[string]int { return ev.Tools })
	ke, kl := hashesOf(early, func(ev *RunEvidence) map[string]int { return ev.Keys }), hashesOf(late, func(ev *RunEvidence) map[string]int { return ev.Keys })
	switch {
	case len(pe) > 0 && len(pl) > 0 && !slices.Equal(pe, pl):
		f.Cause = fmt.Sprintf("system prompt changed over time: sha256 %s earlier vs %s later", hashList(pe), hashList(pl))
	case len(te) > 0 && len(tl) > 0 && !slices.Equal(te, tl):
		f.Cause = fmt.Sprintf("tool list changed over time: sha256 %s earlier vs %s later", hashList(te), hashList(tl))
	case len(ke) > 0 && len(kl) > 0 && disjoint(ke, kl):
		f.Cause = fmt.Sprintf("provider key changed over time (%s earlier, %s later): a cache is scoped to the provider account", strings.Join(ke, ", "), strings.Join(kl, ", "))
	case len(pe) > 0 && len(pl) > 0:
		f.Cause = "same system prompt and tool list throughout: the drop comes from outside the agent (provider cache eviction or routing), not from the prefix"
	default:
		f.Cause = "not shown by the evidence: the gateway rows carry no prompt detail to compare"
	}
	f.Evidence = append(f.Evidence, fmt.Sprintf("cache read share %s over the first %d runs vs %s over the last %d (by start time)", pct(se), len(early), pct(sl), len(late)))
	return f, true
}

// varies reports a system prompt that changes between runs of the same
// task in one arm: two runs of a task whose prompt hashes share nothing.
// Prompts that differ between tasks are expected and not counted.
func varies(g *agg) bool {
	byTask := map[string][]map[string]int{}
	for _, ev := range g.runs {
		if len(ev.Prompts) > 0 {
			byTask[ev.Run.Slot.Task] = append(byTask[ev.Run.Slot.Task], ev.Prompts)
		}
	}
	for _, runs := range byTask {
		for i := 1; i < len(runs); i++ {
			if disjoint(sortedKeys(runs[0]), sortedKeys(runs[i])) {
				return true
			}
		}
	}
	return false
}

// configEvidence lists the config groups that differ between the arms'
// resolved configs, from the same field and file diff `agent diff` shows.
func configEvidence(m *schemas.Manifest, condition, a, b string) []string {
	am, okA := m.Arms[a]
	bm, okB := m.Arms[b]
	if !okA || !okB {
		return nil
	}
	diff, err := agentdiff.DiffConfigs(am, bm, condition, nil)
	if err != nil {
		return []string{"config diff unavailable: " + err.Error()}
	}
	var out []string
	for _, g := range diff.Groups {
		var paths []string
		for _, f := range g.Fields {
			paths = append(paths, f.Path)
		}
		for _, f := range g.Files {
			paths = append(paths, f.Path)
		}
		if len(paths) > 4 {
			paths = append(paths[:4], fmt.Sprintf("and %d more", len(paths)-4))
		}
		out = append(out, fmt.Sprintf("config diff: %s changed (%s)", g.ID, strings.Join(paths, ", ")))
	}
	return out
}

func runTokens(runs []*RunEvidence) schemas.Tokens {
	var t schemas.Tokens
	for _, ev := range runs {
		for _, r := range ev.Rows {
			t.Fresh += r.Tokens.Fresh
			t.CacheRead += r.Tokens.CacheRead
			t.CacheWrite += r.Tokens.CacheWrite
			t.Out += r.Tokens.Out
		}
	}
	return t
}

func hashesOf(runs []*RunEvidence, pick func(*RunEvidence) map[string]int) []string {
	all := map[string]int{}
	for _, ev := range runs {
		for k, v := range pick(ev) {
			all[k] += v
		}
	}
	return sortedKeys(all)
}

func disjoint(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// hashList writes up to three hashes, then a count.
func hashList(hs []string) string {
	if len(hs) <= 3 {
		return strings.Join(hs, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(hs[:3], ", "), len(hs)-3)
}
