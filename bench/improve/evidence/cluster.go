package evidence

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/improve"
)

var (
	exitPrefix = regexp.MustCompile(`^exit -?\d+:\s*`)
	pathRe     = regexp.MustCompile(`(?:[A-Za-z0-9_.-]*/)+[A-Za-z0-9_.-]+`)
	hexRe      = regexp.MustCompile(`\b(?:0x)?[0-9a-fA-F]{6,}\b`)
	digitRe    = regexp.MustCompile(`\d+`)
	spaceRe    = regexp.MustCompile(`\s+`)
)

// signature is the first error's shape: paths, hex strings and digits
// replaced, punctuation that only separates words dropped.
func signature(e string) string {
	e = exitPrefix.ReplaceAllString(strings.TrimSpace(e), "")
	if i := strings.IndexByte(e, '\n'); i >= 0 {
		e = e[:i]
	}
	e = pathRe.ReplaceAllString(e, "<path>")
	e = hexRe.ReplaceAllStringFunc(e, func(s string) string {
		if strings.ContainsAny(s, "0123456789") {
			return "<hex>"
		}
		return s
	})
	e = digitRe.ReplaceAllString(e, "#")
	e = strings.NewReplacer(":", " ", ",", " ", ";", " ").Replace(e)
	e = strings.TrimSpace(spaceRe.ReplaceAllString(e, " "))
	if len(e) > 80 {
		e = e[:80]
	}
	return e
}

// markNoisy flags runs of a task whose trials of the same arm disagree.
func markNoisy(runs []*run) {
	type k struct {
		task string
		arm  int
	}
	pass, fail := map[k]bool{}, map[k]bool{}
	for _, r := range runs {
		kk := k{r.ref.Task, r.ref.Arm}
		if r.item.Success >= 1 {
			pass[kk] = true
		} else {
			fail[kk] = true
		}
	}
	for _, r := range runs {
		kk := k{r.ref.Task, r.ref.Arm}
		r.item.Noisy = pass[kk] && fail[kk]
	}
}

// markFlips sets flip on candidate runs whose incumbent run of the same
// experiment, task and trial had the other result.
func markFlips(runs []*run) {
	type k struct {
		exp, task string
		trial     int
	}
	parent := map[k]*run{}
	for _, r := range runs {
		if r.ref.Arm == 0 {
			parent[k{r.ref.ExpID, r.ref.Task, r.ref.Trial}] = r
		}
	}
	for _, r := range runs {
		if r.ref.Arm == 0 {
			continue
		}
		p := parent[k{r.ref.ExpID, r.ref.Task, r.ref.Trial}]
		if p == nil {
			continue
		}
		pp, cp := p.item.Success >= 1, r.item.Success >= 1
		switch {
		case !pp && cp:
			r.item.Flip = "fail_to_pass"
		case pp && !cp:
			r.item.Flip = "pass_to_fail"
		}
	}
}

// clusterKey is (stop reason, failed check names, error signature, last tool).
func clusterKey(r *run) string {
	stop := r.item.StopReason
	if stop == "" {
		stop = r.item.Outcome
	}
	var failed []string
	for _, c := range r.item.Checks {
		if !c.Passed {
			failed = append(failed, c.Name)
		}
	}
	sort.Strings(failed)
	return strings.Join([]string{stop, strings.Join(failed, ","), r.errSig, r.last}, "|")
}

// buildClusters groups failed, non-noisy runs, ranks them by lost score and
// keeps the top ones with their exemplars and a contrast run.
func buildClusters(runs []*run, lim improve.EvidenceLimits) []cluster {
	groups := map[string][]*run{}
	for _, r := range runs {
		if r.item.Success >= 1 || r.item.Noisy {
			continue
		}
		k := clusterKey(r)
		groups[k] = append(groups[k], r)
	}
	type g struct {
		key  string
		lost float64
		runs []*run
	}
	var all []g
	for k, rs := range groups {
		lost := 0.0
		for _, r := range rs {
			lost += 1 - r.item.Success
		}
		all = append(all, g{k, lost, rs})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].lost != all[j].lost {
			return all[i].lost > all[j].lost
		}
		return all[i].key < all[j].key
	})
	if len(all) > lim.Clusters {
		all = all[:lim.Clusters]
	}
	out := make([]cluster, 0, len(all))
	for i, x := range all {
		c := cluster{ID: fmt.Sprintf("c%d", i+1), Key: x.key, Lost: x.lost}
		for _, r := range x.runs {
			c.Runs = append(c.Runs, r.label)
			if !slices.Contains(c.Tasks, r.ref.Task) {
				c.Tasks = append(c.Tasks, r.ref.Task)
			}
			r.item.Cluster = c.ID
		}
		ex := slices.Clone(x.runs)
		sort.SliceStable(ex, func(i, j int) bool {
			if ex[i].item.Success != ex[j].item.Success {
				return ex[i].item.Success < ex[j].item.Success
			}
			return len(ex[i].steps) < len(ex[j].steps)
		})
		for j := 0; j < len(ex) && j < lim.Exemplars; j++ {
			c.Exemplars = append(c.Exemplars, ex[j].label)
		}
		last := x.runs[0].last
		for _, r := range runs {
			if r.item.Success >= 1 && r.last == last && !slices.Contains(c.Tasks, r.ref.Task) {
				c.Contrast = r.label
				break
			}
		}
		out = append(out, c)
	}
	return out
}
