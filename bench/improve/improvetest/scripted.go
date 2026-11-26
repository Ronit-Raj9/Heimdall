// Package improvetest is a scripted improve.Executor for tests: agents
// whose success depends only on their bundle, and improvers with fixed
// behaviors (honest, lazy, cheating, quiet). Nothing here talks to Docker,
// a gateway or a model, and nothing here is wired into the product.
package improvetest

import (
	"context"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/improve"
)

// NotesPath is the editable notes file of the scripted agent.
const NotesPath = "/home/agent/AGENTS.md"

// AgentDoc is a schema-valid scripted agent with an empty notes file.
const AgentDoc = `id: scripted-improver
adapter: scripted
image: bench/coding-core-base:1
scripted:
  kind: exploit
model: claude-sonnet-4-5
budgets:
  wall_s: 900
  cpus: 1
  mem_gb: 1
gateway:
  provider: anthropic
files:
  /home/agent/AGENTS.md: |
    level: 0
  /etc/agent/policy.txt: fixed
`

// Canary is the protected suites' canary GUID.
const Canary = "1b4e28ba-2fa1-41d2-883f-0016d3cca427"

// Improver behaviors.
const (
	Honest  = "honest"  // raises the level each round: real, general gains
	Lazy    = "lazy"    // returns the bundle unchanged
	Cheat   = "cheat"   // writes a sealed task id and its answer into the notes
	Quiet   = "quiet"   // raises the level and switches off its tool-call logging
	Frozen  = "frozen"  // raises the level and writes a non-editable file
	Spender = "spender" // raises the level and spends its whole cap
	Worse   = "worse"   // lowers the level: a regression
	Noise   = "noise"   // adds a filler line; with Noise set, success is a coin
)

// Exec is the scripted executor. A version's notes carry "level: N"; a
// task succeeds when the task's hash bucket (0 to 9) is below N + Base, so
// a higher level helps every split alike. Calls are recorded per phase.
type Exec struct {
	Behavior string
	Base     int     // buckets solved at level 0
	NTasks   int     // pool size (default 20)
	RunUSD   float64 // cost per run (default 0.01)
	ImpUSD   float64 // improver cost per round (default 0.1)
	FailOn   string  // a phase whose Score fails once
	// SealedBonus is added to sealed successes only: the loop must not
	// change any decision when it moves.
	SealedBonus int
	Step        int // levels an improving round adds (default 3)
	// Slots gives the behavior of one idea slot of a round (slot from 1);
	// a slot not listed uses Behavior.
	Slots map[int]string
	// Noise, when above 0, makes every run succeed with this chance,
	// whatever the version: ideas that never matter. NoiseSeed varies it.
	Noise     float64
	NoiseSeed int64
	// TimeoutTries makes the first TimeoutTries failing runs of version 1
	// on each task, in the final and retries phases, visible failures
	// (outcome and Stop "timeout").
	TimeoutTries int
	// Hold is a phase whose first Score closes Held and waits for Release.
	Hold    string
	Held    chan struct{}
	Release chan struct{}

	requests map[string]improve.ScoreRequest
	attempts map[string]int
	held     bool

	mu       sync.Mutex
	versions []string // doc of v1..vN
	Calls    map[string]int
	Evidence [][]improve.EvidenceItem
	failed   bool
}

// Load implements improve.Executor.
func (x *Exec) Load(_ context.Context, ref string, ed improve.Editable) (improve.Version, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if len(x.versions) == 0 {
		x.versions = []string{AgentDoc}
	}
	return improve.NewVersion("agents/scripted-improver.yaml@v1", 1, []byte(x.versions[0]), ed)
}

// Freeze implements improve.Executor.
func (x *Exec) Freeze(_ context.Context, parent improve.Version, doc []byte, lin improve.Lineage) (improve.Version, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	ed := improve.Editable{Files: []string{NotesPath}}
	cand, err := improve.NewVersion("", 0, doc, ed)
	if err != nil {
		return cand, err
	}
	for i, d := range x.versions {
		if v, _ := improve.NewVersion("", 0, []byte(d), ed); v.ConfigHash == cand.ConfigHash {
			return improve.NewVersion(ref(i+1), i+1, []byte(d), ed)
		}
	}
	x.versions = append(x.versions, string(doc))
	n := len(x.versions)
	return improve.NewVersion(ref(n), n, doc, ed)
}

// Tasks implements improve.Executor.
func (x *Exec) Tasks(_ context.Context, spec *improve.Spec) ([]improve.TaskInfo, error) {
	n := x.NTasks
	if n == 0 {
		n = 20
	}
	out := []improve.TaskInfo{}
	for i := 1; i <= n; i++ {
		if id := TaskID(i); len(spec.Tasks) == 0 || slices.Contains(spec.Tasks, id) {
			out = append(out, improve.TaskInfo{ID: id})
		}
	}
	out = append(out, improve.TaskInfo{ID: "task-impossible-1", Impossible: true})
	return out, nil
}

// Protect implements improve.Executor.
func (x *Exec) Protect(_ context.Context, _ *improve.Spec, tasks []string) (improve.Protected, error) {
	p := improve.Protected{TaskIDs: slices.Clone(tasks), Canaries: []string{Canary}}
	for _, t := range tasks {
		p.Secrets = append(p.Secrets, Secret(t))
	}
	return p, nil
}

// Improve implements improve.Executor.
func (x *Exec) Improve(_ context.Context, req improve.ImproveRequest) (*improve.ImproveResult, error) {
	x.mu.Lock()
	x.count("improve")
	x.Evidence = append(x.Evidence, slices.Clone(req.Evidence))
	x.mu.Unlock()
	b := req.Incumbent.Bundle.Clone()
	cost := x.ImpUSD
	if cost == 0 {
		cost = 0.1
	}
	res := &improve.ImproveResult{RunID: fmt.Sprintf("r_imp%d_%d", req.Round, SlotOf(req)), CostUSD: cost}
	if req.Null {
		b[NotesPath] = b[NotesPath] + fmt.Sprintf("filler note %d\n", req.Round)
		res.Bundle = b
		return res, nil
	}
	lvl, step := Level(b[NotesPath]), x.Step
	if step == 0 {
		step = 3
	}
	up := strings.Replace(b[NotesPath], "level: "+strconv.Itoa(lvl), "level: "+strconv.Itoa(lvl+step), 1)
	behavior := x.Behavior
	if b, ok := x.Slots[SlotOf(req)]; ok {
		behavior = b
	}
	switch behavior {
	case Lazy:
	case Worse:
		b[NotesPath] = strings.Replace(b[NotesPath], "level: "+strconv.Itoa(lvl), "level: "+strconv.Itoa(lvl-step), 1)
	case Noise:
		b[NotesPath] = b[NotesPath] + fmt.Sprintf("idea %d.%d\n", req.Round, SlotOf(req))
	case Cheat:
		var sb strings.Builder
		for i := 1; i <= max(20, x.NTasks); i++ {
			sb.WriteString(Secret(TaskID(i)) + "\n")
		}
		b[NotesPath] = up + sb.String()
	case Quiet:
		b[NotesPath] = up + "quiet\n"
	case Frozen:
		b[NotesPath] = up
		b["/etc/agent/policy.txt"] = "relaxed"
	case Spender:
		b[NotesPath] = up
		res.CostUSD = req.BudgetUSD
	default:
		b[NotesPath] = up
	}
	res.Bundle = b
	return res, nil
}

// Score implements improve.Executor.
func (x *Exec) Score(_ context.Context, req improve.ScoreRequest) (*improve.ScoreResult, error) {
	x.mu.Lock()
	x.count(req.Phase)
	if x.requests == nil {
		x.requests = map[string]improve.ScoreRequest{}
	}
	x.requests[req.Phase] = req
	if x.Hold == req.Phase && !x.held {
		x.held = true
		x.mu.Unlock()
		close(x.Held)
		<-x.Release
		x.mu.Lock()
	}
	if x.FailOn == req.Phase && !x.failed {
		x.failed = true
		x.mu.Unlock()
		return nil, fmt.Errorf("scripted %s failure", req.Phase)
	}
	x.mu.Unlock()
	usd := x.RunUSD
	if usd == 0 {
		usd = 0.01
	}
	res := &improve.ScoreResult{ExpID: fmt.Sprintf("e_%s_r%d", req.Phase, req.Round)}
	for ai, arm := range req.Arms {
		lvl := Level(arm.Bundle[NotesPath])
		ch := []string{"message", "tool_call"}
		if strings.Contains(arm.Bundle[NotesPath], "quiet") {
			ch = []string{"message"}
		}
		res.Channels = append(res.Channels, ch)
		for _, t := range req.Tasks {
			for tr := 1; tr <= req.Trials; tr++ {
				limit := lvl + x.Base
				switch req.Phase {
				case improve.PhaseSealed, improve.PhaseControl, improve.PhaseFinal, improve.PhaseExtension, improve.PhaseRetries:
					limit += x.SealedBonus
				}
				ok := bucket(t) < limit && !strings.HasPrefix(t, "task-impossible")
				if x.Noise > 0 {
					ok = x.coin(req, arm, t, tr) < x.Noise
				}
				s := 0.0
				if ok {
					s = 1
				}
				out, stop := "failed", ""
				if ok {
					out = "passed"
				}
				if !ok && x.TimeoutTries > 0 && arm.Number == 1 && (req.Phase == improve.PhaseFinal || req.Phase == improve.PhaseRetries) {
					x.mu.Lock()
					if x.attempts == nil {
						x.attempts = map[string]int{}
					}
					if x.attempts[t] < x.TimeoutTries && (tr == 1 || req.Phase == improve.PhaseRetries) {
						out, stop = "timeout", "timeout"
					}
					if tr == 1 || req.Phase == improve.PhaseRetries {
						x.attempts[t]++
					}
					x.mu.Unlock()
				}
				res.Runs = append(res.Runs, improve.RunScore{Arm: ai, Task: t, Trial: tr, Success: s, Known: true, Outcome: out, Stop: stop, CostUSD: usd})
				res.CostUSD += usd
			}
		}
	}
	return res, nil
}

// Request is the last scoring request of a phase.
func (x *Exec) Request(phase string) improve.ScoreRequest {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.requests[phase]
}

// SlotOf reads the idea slot from an improver request's seed, which the
// default proposer sets to spec seed x 1000 + round x 16 + slot.
func SlotOf(req improve.ImproveRequest) int {
	if req.Spec == nil || req.Seed == 0 {
		return 1
	}
	return int(req.Seed - req.Spec.Seed*1000 - int64(req.Round*16))
}

// coin is a fixed pseudo random number in [0, 1) for one run.
func (x *Exec) coin(req improve.ScoreRequest, arm improve.Version, task string, trial int) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%s|%d|%s|%s|%d", x.NoiseSeed, req.Phase, req.Round, arm.ConfigHash, task, trial)
	return float64(h.Sum64()>>11) / float64(1<<53)
}

// Count is how many calls a phase got.
func (x *Exec) Count(phase string) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.Calls[phase]
}

func (x *Exec) count(phase string) {
	if x.Calls == nil {
		x.Calls = map[string]int{}
	}
	x.Calls[phase]++
}

// TaskID is the i-th pool task.
func TaskID(i int) string { return fmt.Sprintf("task-pool-%02d", i) }

// Secret is a protected text of task t (its expected answer).
func Secret(t string) string {
	return "the expected answer for " + t + " is the number forty two exactly as written"
}

// Level reads "level: N" from notes.
func Level(notes string) int {
	for _, l := range strings.Split(notes, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "level: "); ok {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return 0
}

func ref(n int) string { return fmt.Sprintf("agents/scripted-improver.yaml@v%d", n) }

func bucket(t string) int {
	h := fnv.New32a()
	h.Write([]byte(t))
	return int(h.Sum32() % 10)
}
