package schemas

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Learning over time and persistence (RESEARCH_R2 rec 9, RESEARCH_R5 recs 5
// and 10). Stream mode carries the agent's memory volumes from one task of
// an experiment to the next; the world clock lets time pass between the
// sessions of one task; the process probe kills the agent mid-run and
// resumes it. Every field is optional and omitempty, so experiments, tasks
// and probes without them encode and hash exactly as before.

// Stream orders and memory modes (StreamSpec).
const (
	StreamOrderSuite  = "suite"  // the tasks in the order the condition lists them
	StreamOrderSeeded = "seeded" // one shuffle of the tasks from the experiment seed, shared by every chain
	StreamMemoryCarry = "carry"  // each run starts from the memory the previous run of its chain left
	StreamMemoryFresh = "fresh"  // ordered like a stream, memory empty every run (a control)
)

// StreamSpec is an experiment's `stream:` block. The runs of one arm, one
// condition and one trial form a chain over the condition's tasks: they run
// one after another, and with memory carry each run's memory volumes
// (/memory and the agent's memory paths) are archived into its run folder
// and seeded into the next run of the chain. FreshArm names an arm whose
// chain is ordered the same way but always starts empty: the forward
// transfer control. Replay adds, at the end of every chain, one more run of
// each of the first Replay tasks (backward transfer). Threshold is the
// success rate episodes_to_threshold waits for (default 0.8).
type StreamSpec struct {
	Order     string  `json:"order,omitempty" yaml:"order"`
	Memory    string  `json:"memory" yaml:"memory"`
	FreshArm  string  `json:"fresh_arm,omitempty" yaml:"fresh_arm"`
	Replay    int     `json:"replay,omitempty" yaml:"replay"`
	Threshold float64 `json:"threshold,omitempty" yaml:"threshold"`
}

// DefaultStreamThreshold is the success rate episodes_to_threshold waits for
// when the stream names none.
const DefaultStreamThreshold = 0.8

// MaxStreamReplay bounds the replay runs added to one chain.
const MaxStreamReplay = 20

// Validate checks the block. arms are the experiment's arm ids.
func (s *StreamSpec) Validate(arms map[string]ArmSpec) error {
	if s == nil {
		return nil
	}
	switch s.Order {
	case "", StreamOrderSuite, StreamOrderSeeded:
	default:
		return fmt.Errorf("stream.order must be suite or seeded, got %q", s.Order)
	}
	switch s.Memory {
	case StreamMemoryCarry, StreamMemoryFresh:
	default:
		return fmt.Errorf("stream.memory must be carry or fresh, got %q", s.Memory)
	}
	if s.FreshArm != "" {
		if _, ok := arms[s.FreshArm]; !ok {
			return fmt.Errorf("stream.fresh_arm %q is not an arm of the experiment", s.FreshArm)
		}
	}
	if s.Replay < 0 || s.Replay > MaxStreamReplay {
		return fmt.Errorf("stream.replay must be between 0 and %d, got %d", MaxStreamReplay, s.Replay)
	}
	if s.Threshold < 0 || s.Threshold > 1 {
		return fmt.Errorf("stream.threshold must be between 0 and 1, got %v", s.Threshold)
	}
	return nil
}

// Carries reports whether a run of arm starts from the previous run's
// memory.
func (s *StreamSpec) Carries(arm string) bool {
	return s != nil && s.Memory == StreamMemoryCarry && arm != s.FreshArm
}

// StreamControlEnv is the env var an evaluation sets on the no-memory
// control arm it adds to a score-mode stream (handlers/evaluations.go): the
// same agent, so it is no part of the version identity
// (agentversions.AgentHash drops it), but its config hash differs from the
// arm it controls, as the planner wants.
const StreamControlEnv = "HEIMDALL_STREAM_CONTROL"

// StreamControlSuffix ends the label of that control arm.
const StreamControlSuffix = " (no memory)"

// StreamControlArm reports whether arm of m is a stream's no-memory control
// (the stream's fresh arm while memory is carried). It measures forward
// transfer only: readers that rank setups (the leaderboards) leave its runs
// out, since the setup it copies is already counted under its own arm.
func StreamControlArm(m *Manifest, arm string) bool {
	if m == nil {
		return false
	}
	s := m.Experiment.Stream
	return s != nil && s.Memory == StreamMemoryCarry && s.FreshArm != "" && s.FreshArm == arm
}

// ThresholdOrDefault is Threshold, or DefaultStreamThreshold when unset.
func (s *StreamSpec) ThresholdOrDefault() float64 {
	if s == nil || s.Threshold <= 0 {
		return DefaultStreamThreshold
	}
	return s.Threshold
}

// StreamFile is the run folder file that records a stream run's place in
// its chain and where its memory came from.
const StreamFile = "stream.json"

// StreamMemoryArchive is the run folder file holding the memory volumes a
// stream run left, as a tar archive with one top-level folder per volume
// (0 is /memory, i+1 the agent's memory path i).
const StreamMemoryArchive = "memory.tar"

// StreamRecord is the content of stream.json.
type StreamRecord struct {
	Index      int    `json:"index"`                 // 1-based place in the chain
	Replay     bool   `json:"replay,omitempty"`      // a replay run (backward transfer)
	Carry      bool   `json:"carry"`                 // the run was meant to start from the previous run's memory
	PrevSlot   string `json:"prev_slot,omitempty"`   // the previous slot of the chain
	SeededFrom string `json:"seeded_from,omitempty"` // run id whose memory archive was seeded
	SeedSHA256 string `json:"seed_sha256,omitempty"` // sha256 of that archive
	SeedNote   string `json:"seed_note,omitempty"`   // why nothing was seeded although Carry is set
	Archived   bool   `json:"archived"`              // this run's memory archive was written
	ArchiveSHA string `json:"archive_sha256,omitempty"`
}

// MemoryCarrier is implemented by environments that can archive a run's
// memory volumes into a host file and seed a fresh run's memory volumes from
// one. ArchiveMemory runs after the agent exited; SeedMemory right after
// Create, before anything starts.
type MemoryCarrier interface {
	ArchiveMemory(ctx context.Context, rc *RunContext, dst string) error
	SeedMemory(ctx context.Context, rc *RunContext, src string) error
}

// World clock (task `world:` block).

// WorldTickPath is the hook every task service may serve to learn that time
// passed between two sessions. It is task-owned and never reached through
// the tool proxy, so the agent cannot call it.
const WorldTickPath = "/_world/tick"

// WorldFile is the run folder file listing every world tick of the run.
const WorldFile = "world.json"

// WorldSpec makes time pass between the sessions of a multi-session task.
// Session n (1-based) runs at StartDate + (n-1) x SessionGap, given to the
// agent as BENCH_DATE. Before every session after the first the engine
// POSTs WorldTickPath to the task's services with the new date and the
// events whose AfterSession is the session that just ended.
type WorldSpec struct {
	StartDate  string       `json:"start_date" yaml:"start_date"`   // YYYY-MM-DD or RFC 3339
	SessionGap string       `json:"session_gap" yaml:"session_gap"` // e.g. 7d, 36h, 90m
	Events     []WorldEvent `json:"events,omitempty" yaml:"events"`
}

// WorldEvent is one change a service makes between two sessions: Action is
// handed to the service's tick hook as is (an id the service knows, such as
// price_change or ship_order).
type WorldEvent struct {
	AfterSession int    `json:"after_session" yaml:"after_session"`
	Service      string `json:"service" yaml:"service"`
	Action       string `json:"action" yaml:"action"`
}

// WorldTick is one tick the engine sent, as recorded in world.json.
type WorldTick struct {
	Session int      `json:"session"` // 1-based session about to start
	Date    string   `json:"date"`
	Service string   `json:"service"`
	Actions []string `json:"actions,omitempty"`
	Status  int      `json:"status"`
	Error   string   `json:"error,omitempty"`
}

// WorldEnvironment is implemented by environments that can deliver world
// ticks to a run's services.
type WorldEnvironment interface {
	WorldTick(ctx context.Context, rc *RunContext, session int, date string, events []WorldEvent) ([]WorldTick, error)
}

// ParseGap parses a session gap: a number with d (days), h, m or s, or any
// Go duration. It is pure.
func ParseGap(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty gap")
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(n, 64)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("gap %q: days must be a number >= 0", s)
		}
		return time.Duration(days * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("gap %q: want e.g. 7d, 36h or 90m", s)
	}
	return d, nil
}

// parseStart reads StartDate as a date or an RFC 3339 time.
func parseStart(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("start_date %q must be YYYY-MM-DD or RFC 3339", s)
	}
	return t.UTC(), nil
}

// Validate checks the block against the task's sessions and service ids.
func (w *WorldSpec) Validate(sessions int, services map[string]bool) error {
	if w == nil {
		return nil
	}
	if sessions < 2 {
		return errors.New("world needs a task with at least two sessions")
	}
	if _, err := parseStart(w.StartDate); err != nil {
		return fmt.Errorf("world: %w", err)
	}
	if _, err := ParseGap(w.SessionGap); err != nil {
		return fmt.Errorf("world.session_gap: %w", err)
	}
	for i, e := range w.Events {
		if e.AfterSession < 1 || e.AfterSession >= sessions {
			return fmt.Errorf("world.events[%d]: after_session must be between 1 and %d", i, sessions-1)
		}
		if e.Service == "" || !services[e.Service] {
			return fmt.Errorf("world.events[%d]: service %q is not a service of the task or its suite", i, e.Service)
		}
		if strings.TrimSpace(e.Action) == "" {
			return fmt.Errorf("world.events[%d]: action is empty", i)
		}
	}
	return nil
}

// SessionDate is the world date of the 0-based session index, RFC 3339 in
// UTC.
func (w *WorldSpec) SessionDate(index int) (string, error) {
	start, err := parseStart(w.StartDate)
	if err != nil {
		return "", err
	}
	gap, err := ParseGap(w.SessionGap)
	if err != nil {
		return "", err
	}
	return start.Add(time.Duration(index) * gap).Format(time.RFC3339), nil
}

// EventsAfter are the events of the session that just ended (1-based).
func (w *WorldSpec) EventsAfter(session int) []WorldEvent {
	var out []WorldEvent
	for _, e := range w.Events {
		if e.AfterSession == session {
			out = append(out, e)
		}
	}
	return out
}

// Process probe (layer process).

// LayerProcess is the probe layer that SIGKILLs the agent process mid-run
// and resumes it with the same workspace and memory (BENCH_RESUME=1).
const LayerProcess ProbeLayerName = "process"

// ProcessFault is ProbeApply.Process: kill the agent once AtModelCall model
// calls of the run went through the gateway, or AfterS seconds after it
// started, whichever comes first; in Session (an id) of a multi-session
// task, or the first session when empty.
type ProcessFault struct {
	AtModelCall int    `json:"at_model_call,omitempty" yaml:"at_model_call"`
	AfterS      int    `json:"after_s,omitempty" yaml:"after_s"`
	Session     string `json:"session,omitempty" yaml:"session"`
}

// Validate checks the rule.
func (p *ProcessFault) Validate() error {
	if p == nil {
		return errors.New("process probe has no process block")
	}
	if p.AtModelCall < 0 || p.AfterS < 0 {
		return errors.New("process: at_model_call and after_s must not be negative")
	}
	if p.AtModelCall == 0 && p.AfterS == 0 {
		return errors.New("process: set at_model_call or after_s")
	}
	return nil
}

// ProcessKillFile is the run folder file the engine writes when it killed
// the agent.
const ProcessKillFile = "process_kill.json"

// ProcessKill records one kill and the resume after it (Evidence.ProcessKill).
type ProcessKill struct {
	Session     string    `json:"session,omitempty"`
	KilledAt    time.Time `json:"killed_at"`
	ModelCalls  int       `json:"model_calls"` // model calls seen when the kill was sent
	Trigger     string    `json:"trigger"`     // model_call | after_s
	ExitCode    int       `json:"exit_code"`   // of the killed process
	Resumed     bool      `json:"resumed"`
	ResumeExit  *int      `json:"resume_exit,omitempty"`
	ResumeError string    `json:"resume_error,omitempty"`
}

// Budget disclosure (experiment budget.disclose).

// Agent environment variables of budget disclosure.
const (
	EnvBudgetUSD    = "BENCH_BUDGET_USD"
	EnvDeadline     = "BENCH_DEADLINE"
	EnvMaxToolCalls = "BENCH_MAX_TOOL_CALLS"
	EnvResume       = "BENCH_RESUME"
	EnvDate         = "BENCH_DATE"
)

// AgentKiller is implemented by environments that can SIGKILL the running
// agent process of a run (the process probe).
type AgentKiller interface {
	KillAgent(ctx context.Context, rc *RunContext) error
}
