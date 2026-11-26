package improve

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// UpdateCounters adds the screen's flips to the helpful and harmful counts
// of the items an edit set touched (A3.3 pruning). flips maps a practice
// task id to "fail_to_pass" or "pass_to_fail". For each applied edit with
// predicts_fixed, a predicted task that flipped fail_to_pass adds 1 to
// helpful of the edited or added item, pass_to_fail adds 1 to harmful.
func UpdateCounters(pb PlaybookObject, applied []AppliedEdit, flips map[string]string) PlaybookObject {
	out := PlaybookObject{Next: pb.Next, Items: slices.Clone(pb.Items)}
	idx := map[string]int{}
	for i, it := range out.Items {
		idx[it.ID] = i
	}
	for _, a := range applied {
		id := appliedItemID(a)
		i, ok := idx[id]
		if !ok || len(a.PredictsFixed) == 0 {
			continue
		}
		for _, task := range a.PredictsFixed {
			switch flips[task] {
			case "fail_to_pass":
				out.Items[i].Helpful++
			case "pass_to_fail":
				out.Items[i].Harmful++
			}
		}
	}
	return out
}

// appliedItemID is the playbook item an applied edit ended up in: the
// edited id, the item it was merged into, or the id ApplyEdits gave a new
// item (written back into the edit's ID).
func appliedItemID(a AppliedEdit) string {
	if id, ok := strings.CutPrefix(a.Result, "merged_into:"); ok {
		return id
	}
	if a.Result != "applied" {
		return ""
	}
	return a.ID
}

// PruneHints are the next round's prune edits (A3.3): delete_item for items
// with harmful >= helpful + 2, and retire_skill for skills not opened in
// any practice run for 2 consecutive rounds. opened maps a skill path to
// the last round a practice run opened it (0 when never since it was
// added); skills missing from opened are not judged.
func PruneHints(pb PlaybookObject, opened map[string]int, round int) []Edit {
	var out []Edit
	for _, it := range pb.Items {
		if it.Harmful >= it.Helpful+2 {
			out = append(out, Edit{Op: "delete_item", ID: it.ID,
				Reason: fmt.Sprintf("harmful %d times against helpful %d times in practice runs", it.Harmful, it.Helpful)})
		}
	}
	paths := make([]string, 0, len(opened))
	for p := range opened {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !strings.HasSuffix(p, "/SKILL.md") {
			continue
		}
		if round-opened[p] >= 2 {
			out = append(out, Edit{Op: "retire_skill", Path: p, Reason: "not opened in practice runs for 2 rounds"})
		}
	}
	return out
}
