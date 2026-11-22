package onboard

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// Gate names.
const (
	GateStatic    = "static"
	GateDuplicate = "duplicate"
	GateHeldOut   = "held_out"
)

// NearDuplicate is the word-shingle Jaccard similarity at or above which two
// instructions are near copies.
const NearDuplicate = 0.8

// suiteHead is what the gates read from a suite.yaml.
type suiteHead struct {
	Grader string   `yaml:"grader"`
	Tasks  []string `yaml:"tasks"`
}

func readSuiteHead(dir string) (*suiteHead, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if err != nil {
		return nil, nil, err
	}
	var h suiteHead
	if err := yaml.Unmarshal(raw, &h); err != nil {
		return nil, raw, fmt.Errorf("suite.yaml: %w", err)
	}
	return &h, raw, nil
}

// Finish fills every task's preview from its files (instruction, checks,
// controls, split), keeps what is known about it (origin, review,
// readiness), and runs the cheap gates. Sources call it after drafting;
// edits call it again.
func (s *DraftSet) Finish(ctx context.Context, env *Env) error {
	head, _, err := readSuiteHead(s.SuiteDir())
	if err != nil {
		return fmt.Errorf("draft suite: %w", err)
	}
	s.Grader = head.Grader
	known := map[string]DraftTask{}
	for _, t := range s.Tasks {
		known[t.ID] = t
	}
	tasks := make([]DraftTask, 0, len(head.Tasks))
	for _, id := range head.Tasks {
		t, ok := known[id]
		if !ok {
			t = DraftTask{ID: id, Origin: Origin{Source: s.Source, Ref: "task " + id}}
		}
		dir := filepath.Join(s.SuiteDir(), "tasks", id)
		m, err := readTaskMeta(dir)
		if err != nil {
			s.Problems = append(s.Problems, Problem{Ref: "task " + id, Message: err.Error()})
			continue
		}
		if m.Title != "" {
			t.Title = m.Title
		}
		if t.Title == "" {
			t.Title = id
		}
		t.Instruction = cut(instructionText(dir, m), 4000)
		t.Checks = CheckSentences(dir, m, s.Grader)
		origins := map[string]string{}
		for _, c := range t.Controls {
			origins[c.Path] = c.Origin
		}
		t.Controls = controlsOf(m, origins)
		t.HeldOut = m.Split == "held_out"
		if t.Review == "" {
			t.Review = ReviewAccepted
			if len(t.Generated) > 0 { // a model wrote part of it: a person accepts it first
				t.Review = ReviewPending
			}
		}
		tasks = append(tasks, t)
	}
	s.Tasks = tasks
	s.runGates(ctx, env)
	return nil
}

// runGates sets every task's gate results.
func (s *DraftSet) runGates(ctx context.Context, env *Env) {
	static := map[string][]string{}
	var setLevel []string
	if loaders := suite.Builtin(s.BenchRoot()); len(loaders) > 0 {
		st, err := loaders[0].Load(ctx, s.Suite)
		if err != nil {
			setLevel = append(setLevel, "the draft benchmark does not load: "+err.Error())
		} else {
			for _, p := range suite.StaticValidation(ctx, st, "") {
				if p.Severity != suite.SeverityError {
					continue
				}
				hit := false
				for _, t := range s.Tasks {
					if strings.Contains(filepath.ToSlash(p.Path), "tasks/"+t.ID+"/") || strings.HasSuffix(filepath.ToSlash(p.Path), "tasks/"+t.ID) {
						static[t.ID] = append(static[t.ID], p.Message)
						hit = true
					}
				}
				if !hit {
					setLevel = append(setLevel, p.Path+": "+p.Message)
				}
			}
		}
	}
	for _, m := range setLevel {
		s.Problems = appendProblem(s.Problems, Problem{Ref: "benchmark " + s.Suite, Message: m})
	}
	dups := s.duplicates(env)
	for i := range s.Tasks {
		t := &s.Tasks[i]
		var gates []GateResult
		if msgs := static[t.ID]; len(msgs) > 0 {
			gates = append(gates, GateResult{Gate: GateStatic, OK: false, Blocking: true, Message: strings.Join(msgs, "; ")})
		} else if len(setLevel) > 0 {
			gates = append(gates, GateResult{Gate: GateStatic, OK: false, Blocking: true, Message: "the draft benchmark itself has errors (listed above the tasks)"})
		} else {
			gates = append(gates, GateResult{Gate: GateStatic, OK: true, Blocking: true})
		}
		if d := dups[t.ID]; d != "" {
			gates = append(gates, GateResult{Gate: GateDuplicate, OK: false, Message: d})
		} else {
			gates = append(gates, GateResult{Gate: GateDuplicate, OK: true})
		}
		if t.HeldOut && s.Public {
			gates = append(gates, GateResult{Gate: GateHeldOut, OK: false,
				Message: "held out, but the data is public: models may have seen it in training, so it cannot prove anything a public task does not"})
		}
		t.Gates = gates
	}
}

func appendProblem(ps []Problem, p Problem) []Problem {
	for _, q := range ps {
		if q == p {
			return ps
		}
	}
	return append(ps, p)
}

// words is s lower-cased, split on anything that is not a letter or digit.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// shingles is the set of 5-word shingles of s, hashed.
func shingles(s string) map[uint64]bool {
	w := words(s)
	out := map[uint64]bool{}
	const k = 5
	if len(w) < k {
		if len(w) > 0 {
			out[hash64(strings.Join(w, " "))] = true
		}
		return out
	}
	for i := 0; i+k <= len(w); i++ {
		out[hash64(strings.Join(w[i:i+k], " "))] = true
	}
	return out
}

// wordSet is the set of words of s, hashed: short texts (under 20 words)
// are compared on it, so one added word is not a new text.
func wordSet(s string) map[uint64]bool {
	out := map[uint64]bool{}
	for _, x := range words(s) {
		out[hash64(x)] = true
	}
	return out
}

// similarity is the Jaccard similarity of two items' texts.
func similarity(na, nb int, sa, sb, wa, wb map[uint64]bool) float64 {
	if na < 20 || nb < 20 {
		return jaccard(wa, wb)
	}
	return jaccard(sa, sb)
}

func hash64(s string) uint64 {
	sum := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(sum[:8])
}

func jaccard(a, b map[uint64]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for h := range a {
		if b[h] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// duplicates finds exact and near copies inside the set and against the
// target benchmark's tasks. The later task of a pair is the one flagged.
func (s *DraftSet) duplicates(env *Env) map[string]string {
	type item struct {
		id, where string
		norm      string
		n         int
		sh, ws    map[uint64]bool
	}
	mk := func(id, where, ins string) item {
		w := words(ins)
		return item{id: id, where: where, norm: strings.Join(w, " "), n: len(w), sh: shingles(ins), ws: wordSet(ins)}
	}
	common := commonLines(s.Tasks)
	var existing []item
	if env != nil {
		target := filepath.Join(env.Benchmarks(), s.Suite, "tasks")
		if ents, err := os.ReadDir(target); err == nil {
			for i, e := range ents {
				if i >= 2000 || !e.IsDir() {
					continue
				}
				dir := filepath.Join(target, e.Name())
				m, err := readTaskMeta(dir)
				if err != nil {
					continue
				}
				ins := instructionText(dir, m)
				existing = append(existing, mk(e.Name(), "benchmark "+s.Suite, stripLines(ins, common)))
			}
		}
	}
	out := map[string]string{}
	var seen []item
	for _, t := range s.Tasks {
		ins := stripLines(t.Instruction, common)
		it := mk(t.ID, "this draft", ins)
		check := func(pool []item) bool {
			for _, o := range pool {
				if o.id == it.id && o.where != "this draft" {
					continue // the same task id replaced in place is not a copy
				}
				if o.norm != "" && o.norm == it.norm {
					out[t.ID] = fmt.Sprintf("same instruction as task %s in %s", o.id, o.where)
					return true
				}
				if j := similarity(it.n, o.n, it.sh, o.sh, it.ws, o.ws); j >= NearDuplicate {
					out[t.ID] = fmt.Sprintf("near copy (%.0f%% the same words) of task %s in %s", j*100, o.id, o.where)
					return true
				}
			}
			return false
		}
		if !check(seen) {
			check(existing)
		}
		seen = append(seen, it)
	}
	return out
}

// commonLines are the instruction lines more than half the tasks share (a
// source's boilerplate), left out of the duplicate check.
func commonLines(tasks []DraftTask) map[string]bool {
	out := map[string]bool{}
	if len(tasks) < 3 {
		return out
	}
	n := map[string]int{}
	for _, t := range tasks {
		seen := map[string]bool{}
		for _, ln := range strings.Split(t.Instruction, "\n") {
			ln = strings.TrimSpace(ln)
			if ln != "" && !seen[ln] {
				seen[ln] = true
				n[ln]++
			}
		}
	}
	for ln, c := range n {
		if c*2 > len(tasks) {
			out[ln] = true
		}
	}
	return out
}

func stripLines(s string, drop map[string]bool) string {
	if len(drop) == 0 {
		return s
	}
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		if !drop[strings.TrimSpace(ln)] {
			keep = append(keep, ln)
		}
	}
	return strings.Join(keep, "\n")
}

// ApplySplit marks pct percent of the tasks held out, chosen by seed: the
// same seed and ids always pick the same tasks. Tasks the source already
// marked held out stay held out and count toward the share.
func (s *DraftSet) ApplySplit(pct int, seed int64) error {
	if pct <= 0 || len(s.Tasks) == 0 {
		return nil
	}
	if pct > 100 {
		pct = 100
	}
	want := (len(s.Tasks)*pct + 50) / 100
	if want == 0 {
		want = 1
	}
	type ranked struct {
		id string
		h  uint64
	}
	var pool []ranked
	have := 0
	for _, t := range s.Tasks {
		if t.HeldOut {
			have++
			continue
		}
		pool = append(pool, ranked{t.ID, hash64(fmt.Sprintf("%d/%s", seed, t.ID))})
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].h < pool[j].h })
	for i := 0; i < len(pool) && have < want; i++ {
		if err := s.setSplit(pool[i].id, true); err != nil {
			return err
		}
		have++
	}
	return nil
}

// setSplit edits a draft task's split in its task.yaml.
func (s *DraftSet) setSplit(id string, held bool) error {
	p := filepath.Join(s.SuiteDir(), "tasks", id, "task.yaml")
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	edited, err := scaffold.SetTaskSplit(raw, held)
	if err != nil {
		return fmt.Errorf("task %s: %w", id, err)
	}
	if t := s.Task(id); t != nil {
		t.HeldOut = held
	}
	return os.WriteFile(p, edited, 0o644)
}

// CatchRateOf counts the negative controls of rec (checks that want a
// failing grade) and how many the check failed.
func CatchRateOf(rec *schemas.AdmissionRecord) *CatchRate {
	if rec == nil {
		return nil
	}
	c := &CatchRate{}
	for _, ch := range rec.Checks {
		if ch.Want != suite.GotFail {
			continue
		}
		c.Total++
		if ch.Passed {
			c.Caught++
		}
	}
	return c
}

// ApplyReadiness stores admission records on the set's tasks.
func (s *DraftSet) ApplyReadiness(recs map[string]*schemas.AdmissionRecord, reasons map[string]string) {
	for i := range s.Tasks {
		t := &s.Tasks[i]
		rec, ok := recs[t.ID]
		if !ok {
			continue
		}
		t.Readiness, t.CatchRate = rec, CatchRateOf(rec)
		t.Reason = ""
		if rec != nil && !rec.Admitted {
			t.Reason = reasons[t.ID]
			if t.Reason == "" {
				t.Reason = failedChecks(rec)
			}
		}
	}
}

func failedChecks(rec *schemas.AdmissionRecord) string {
	var names []string
	for _, c := range rec.Checks {
		if !c.Passed {
			n := c.Name
			if c.Detail != "" {
				n += " (" + cut(c.Detail, 160) + ")"
			}
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "not ready"
	}
	return "failed: " + strings.Join(names, "; ")
}
