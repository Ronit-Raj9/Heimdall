package taskgates

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The automatic held-out split: pick which tasks go held out (by a seeded
// hash, within strata of a metadata field, or by date), mark them
// split: held_out, and stamp the suite's canary on their instructions
// (docs/held-out-and-canaries.md). It refuses to hold out a task that is
// already public (imported from a public benchmark or found in the
// library) unless the person confirms, since such a score would not be
// held out at all.

// SplitPlan says how to choose the held-out tasks.
type SplitPlan struct {
	HeldOutPct int    `json:"held_out_pct"`          // 0..90: share of tasks held out, per stratum
	Seed       int64  `json:"seed"`                  // same seed, same split
	StratifyBy string `json:"stratify_by,omitempty"` // difficulty, kind, project, capability, or tag:<prefix>
	TimeAfter  string `json:"time_after,omitempty"`  // YYYY-MM-DD: tasks first seen after this date go held out (no hash pick)
	// Confirm lets public tasks be held out (imported from a public
	// benchmark, or overlapping the library).
	Confirm bool `json:"confirm,omitempty"`
}

// SplitChoice is one task's place in the split.
type SplitChoice struct {
	Task    string `json:"task"`
	Stratum string `json:"stratum,omitempty"`
	Was     string `json:"was"`               // split before
	Split   string `json:"split"`             // split after
	Public  string `json:"public,omitempty"`  // why the task is public data, when it is
	Changed bool   `json:"changed,omitempty"` // task.yaml or its instruction is rewritten
}

// SplitResult is what PlanSplit decided.
type SplitResult struct {
	Suite   string        `json:"suite"`
	Plan    SplitPlan     `json:"plan"`
	Tasks   []SplitChoice `json:"tasks"`
	HeldOut int           `json:"held_out"`
	Public  int           `json:"public"`
	Canary  string        `json:"canary,omitempty"`  // the suite canary stamped on held-out instructions
	Refused []string      `json:"refused,omitempty"` // public tasks that would go held out, when not confirmed
	Next    string        `json:"next"`
}

// FileEdit is one file PlanSplit rewrites, relative to the suite root.
type FileEdit struct {
	Rel  string
	Old  []byte
	Data []byte
}

// ErrPublicHeldOut is PlanSplit's refusal to hold out public tasks without
// confirmation; the result lists them.
var ErrPublicHeldOut = errors.New("public tasks would be held out")

// maxHeldOutPct keeps at least a tenth of a suite public, so the suite still
// has a set anyone can run and inspect.
const maxHeldOutPct = 90

// CheckSplitPlan validates p.
func CheckSplitPlan(p SplitPlan) error {
	if p.HeldOutPct < 0 || p.HeldOutPct > maxHeldOutPct {
		return fmt.Errorf("held_out_pct %d: want 0 to %d", p.HeldOutPct, maxHeldOutPct)
	}
	if p.TimeAfter != "" {
		if _, err := time.Parse("2006-01-02", p.TimeAfter); err != nil {
			return fmt.Errorf("time_after %q: want YYYY-MM-DD", p.TimeAfter)
		}
	}
	switch {
	case p.StratifyBy == "", p.StratifyBy == "difficulty", p.StratifyBy == "kind", p.StratifyBy == "project", p.StratifyBy == "capability":
	case strings.HasPrefix(p.StratifyBy, "tag:") && len(p.StratifyBy) > 4:
	default:
		return fmt.Errorf("stratify_by %q: want difficulty, kind, project, capability or tag:<prefix>", p.StratifyBy)
	}
	if p.HeldOutPct == 0 && p.TimeAfter == "" {
		return errors.New("set held_out_pct (a share of tasks) or time_after (a date)")
	}
	return nil
}

// PublicReason says why t is public data ("" when it is the org's own):
// imported from a benchmark, or under an open licence.
func PublicReason(t *schemas.Task) string {
	p := t.Provenance
	if p == nil {
		return ""
	}
	if p.ImportedFrom != "" {
		return "imported from " + p.ImportedFrom
	}
	switch l := strings.ToLower(strings.TrimSpace(p.License)); l {
	case "", "proprietary", "your data", "private", "unlicensed":
	default:
		return "published under " + p.License
	}
	return ""
}

// stratum is t's value of the plan's stratify field.
func stratum(t *schemas.Task, by string) string {
	switch {
	case by == "":
		return ""
	case by == "difficulty":
		return t.Difficulty
	case by == "kind":
		return string(t.Kind)
	case by == "project":
		return t.Project
	case by == "capability":
		if len(t.Capabilities) > 0 {
			return t.Capabilities[0]
		}
		return ""
	case strings.HasPrefix(by, "tag:"):
		pre := strings.TrimPrefix(by, "tag:")
		for _, tag := range t.Tags {
			if strings.HasPrefix(tag, pre) {
				return tag
			}
		}
	}
	return ""
}

// firstSeen is the date a task's source was first seen: its incident's
// first_seen, else the earliest generated field.
func firstSeen(t *schemas.Task) (time.Time, bool) {
	if t.Provenance == nil {
		return time.Time{}, false
	}
	if inc := t.Provenance.Incident; inc != nil && inc.FirstSeen != "" {
		if at, err := time.Parse(time.RFC3339, inc.FirstSeen); err == nil {
			return at, true
		}
	}
	var best time.Time
	for _, g := range t.Provenance.Generated {
		if at, err := time.Parse(time.RFC3339, g.At); err == nil && (best.IsZero() || at.Before(best)) {
			best = at
		}
	}
	return best, !best.IsZero()
}

// rank is the seeded order of a task inside its stratum.
func rank(seed int64, id string) uint64 {
	sum := sha256.Sum256([]byte(strconv.FormatInt(seed, 10) + "\x00" + id))
	return binary.BigEndian.Uint64(sum[:8])
}

// PlanSplit chooses the held-out tasks of s under p and returns the file
// edits that apply it: split: held_out in each chosen task.yaml, the canary
// in suite.yaml when it has none, and the canary line on every held-out
// instruction. A task already held out stays held out and counts toward its
// stratum's share; the split never moves a task back to public. ix, when
// set, marks tasks overlapping the library as public data. Without
// p.Confirm, holding out a public task is refused with ErrPublicHeldOut and
// no edit. Nothing is written.
func PlanSplit(s *schemas.Suite, p SplitPlan, ix *Index) (*SplitResult, []FileEdit, error) {
	if err := CheckSplitPlan(p); err != nil {
		return nil, nil, err
	}
	res := &SplitResult{Suite: s.ID, Plan: p}
	held := map[string]bool{}
	public := map[string]string{}
	contaminated := map[string]string{}
	if ix != nil {
		for _, h := range ix.Check(ItemsOf(s), nil) {
			contaminated[strings.TrimPrefix(h.Task, s.ID+"/")] = "overlaps " + strings.Join(h.Matches, ", ")
		}
	}
	for _, t := range s.Tasks {
		if t.Split == schemas.SplitHeldOut {
			held[t.ID] = true
		}
		if r := PublicReason(t); r != "" {
			public[t.ID] = r
		} else if r := contaminated[t.ID]; r != "" {
			public[t.ID] = r
		}
	}
	if p.TimeAfter != "" {
		after, _ := time.Parse("2006-01-02", p.TimeAfter)
		for _, t := range s.Tasks {
			if at, ok := firstSeen(t); ok && at.After(after) {
				held[t.ID] = true
			}
		}
	} else {
		strata := map[string][]*schemas.Task{}
		for _, t := range s.Tasks {
			k := stratum(t, p.StratifyBy)
			strata[k] = append(strata[k], t)
		}
		for _, ts := range strata {
			want := int(math.Round(float64(len(ts)) * float64(p.HeldOutPct) / 100))
			if want == 0 && p.HeldOutPct > 0 && len(ts) >= 4 {
				want = 1
			}
			have := 0
			for _, t := range ts {
				if held[t.ID] {
					have++
				}
			}
			sort.Slice(ts, func(i, j int) bool { return rank(p.Seed, ts[i].ID) < rank(p.Seed, ts[j].ID) })
			for _, t := range ts {
				if have >= want {
					break
				}
				if !held[t.ID] {
					held[t.ID] = true
					have++
				}
			}
		}
	}
	for _, t := range s.Tasks {
		c := SplitChoice{Task: t.ID, Stratum: stratum(t, p.StratifyBy), Was: splitOf(t), Split: schemas.SplitPublic, Public: public[t.ID]}
		if held[t.ID] {
			c.Split = schemas.SplitHeldOut
			res.HeldOut++
			if c.Public != "" && c.Was != schemas.SplitHeldOut && !p.Confirm {
				res.Refused = append(res.Refused, t.ID)
			}
		} else {
			res.Public++
		}
		res.Tasks = append(res.Tasks, c)
	}
	if len(res.Refused) > 0 {
		res.Next = "These tasks are public data, so a held-out score on them would not be held out: " + strings.Join(res.Refused, ", ") + ". Confirm to hold them out anyway, or change the seed or share."
		return res, nil, ErrPublicHeldOut
	}
	if res.HeldOut == 0 {
		res.Next = "No task goes held out under this plan: raise the share, or pick an earlier date."
		return res, nil, nil
	}
	edits, guid, err := splitEdits(s, held, res)
	if err != nil {
		return nil, nil, err
	}
	res.Canary = guid
	res.Next = fmt.Sprintf("%d tasks held out, each instruction marked with this benchmark's contamination marker. Lock the benchmark and run the readiness check again: the marked instructions changed.", res.HeldOut)
	return res, edits, nil
}

func splitOf(t *schemas.Task) string {
	if t.Split == "" {
		return schemas.SplitPublic
	}
	return t.Split
}

// splitEdits builds the file edits for the held-out set.
func splitEdits(s *schemas.Suite, held map[string]bool, res *SplitResult) ([]FileEdit, string, error) {
	var edits []FileEdit
	suiteYAML := filepath.Join(s.Root, "suite.yaml")
	raw, err := os.ReadFile(suiteYAML)
	if err != nil {
		return nil, "", fmt.Errorf("read suite.yaml: %w", err)
	}
	guid := utils.SuiteYAMLCanary(raw)
	if guid != "" && !utils.ValidCanary(guid) {
		return nil, "", fmt.Errorf("suite.yaml: canary %q is not a lowercase GUID; fix or remove the line first", guid)
	}
	if guid == "" {
		guid = utils.NewCanary()
		out, _ := utils.SetSuiteYAMLCanary(raw, guid)
		edits = append(edits, FileEdit{Rel: "suite.yaml", Old: raw, Data: out})
	}
	idx := map[string]int{}
	for i, c := range res.Tasks {
		idx[c.Task] = i
	}
	for _, t := range s.Tasks {
		if !held[t.ID] {
			continue
		}
		changed := false
		if t.Split != schemas.SplitHeldOut {
			taskRel, err := relTo(s.Root, filepath.Join(t.Dir, "task.yaml"))
			if err != nil {
				return nil, "", err
			}
			old, err := os.ReadFile(filepath.Join(s.Root, taskRel))
			if err != nil {
				return nil, "", fmt.Errorf("task %s: read task.yaml: %w", t.ID, err)
			}
			out, err := scaffold.SetTaskSplit(old, true)
			if err != nil {
				return nil, "", fmt.Errorf("task %s: task.yaml: %w", t.ID, err)
			}
			edits = append(edits, FileEdit{Rel: taskRel, Old: old, Data: out})
			changed = true
		}
		rels := []string{t.Instruction}
		for _, sess := range t.Sessions {
			rels = append(rels, sess.Instruction)
		}
		seen := map[string]bool{}
		for _, rel := range rels {
			if rel == "" {
				continue
			}
			abs, err := utils.ResolveInside(t.Dir, rel)
			if err != nil {
				return nil, "", fmt.Errorf("task %s: instruction %s: %w", t.ID, rel, err)
			}
			if seen[abs] {
				continue
			}
			seen[abs] = true
			old, err := os.ReadFile(abs)
			if err != nil {
				return nil, "", fmt.Errorf("task %s: read instruction: %w", t.ID, err)
			}
			out, diff := stampText(filepath.Base(abs), string(old), guid)
			if !diff {
				continue
			}
			r, err := relTo(s.Root, abs)
			if err != nil {
				return nil, "", err
			}
			edits = append(edits, FileEdit{Rel: r, Old: old, Data: []byte(out)})
			changed = true
		}
		res.Tasks[idx[t.ID]].Changed = changed
	}
	return edits, guid, nil
}

// stampText is suite.StampText (kept here so this package does not import
// the suite loader): text with exactly one canary line for guid, last.
func stampText(name, text, guid string) (string, bool) {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	out := StripCanary(text)
	switch {
	case out == "":
	case strings.HasSuffix(out, nl+nl):
	case strings.HasSuffix(out, nl):
		out += nl
	default:
		out += nl + nl
	}
	out += utils.CanaryLine(name, guid) + nl
	return out, out != text
}

func relTo(root, abs string) (string, error) {
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the suite folder", abs)
	}
	return filepath.ToSlash(r), nil
}

// WriteEdits writes edits under root on the local disk, each through a temp
// file and a rename (the CLI and local mode; hosted mode writes through the
// project store).
func WriteEdits(root string, edits []FileEdit) error {
	for _, e := range edits {
		path := filepath.Join(root, filepath.FromSlash(e.Rel))
		mode := os.FileMode(0o644)
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", e.Rel, err)
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
		if err != nil {
			return fmt.Errorf("write %s: %w", e.Rel, err)
		}
		name := tmp.Name()
		if _, err := tmp.Write(e.Data); err != nil {
			tmp.Close()
			os.Remove(name)
			return fmt.Errorf("write %s: %w", e.Rel, err)
		}
		tmp.Close()
		if err := os.Chmod(name, mode); err != nil {
			os.Remove(name)
			return fmt.Errorf("write %s: %w", e.Rel, err)
		}
		if err := os.Rename(name, path); err != nil {
			os.Remove(name)
			return fmt.Errorf("write %s: %w", e.Rel, err)
		}
	}
	return nil
}

// ProjectWriter is the part of the org project store (projectfs.Store)
// WriteEditsVia needs.
type ProjectWriter interface {
	Write(ctx context.Context, rel string, data []byte, actor, note, ifMatch string) error
}

// WriteEditsVia writes edits through w (rel paths prefixed with prefix, the
// suite's path inside the store), each guarded by the content it was
// computed from, so a file changed meanwhile is a conflict, not lost work.
func WriteEditsVia(ctx context.Context, w ProjectWriter, prefix string, edits []FileEdit, actor, note string) error {
	for _, e := range edits {
		ifMatch := ""
		if e.Old != nil {
			sum := sha256.Sum256(e.Old)
			ifMatch = hex.EncodeToString(sum[:])
		}
		if err := w.Write(ctx, path.Join(prefix, e.Rel), e.Data, actor, note, ifMatch); err != nil {
			return fmt.Errorf("write %s: %w", e.Rel, err)
		}
	}
	return nil
}
