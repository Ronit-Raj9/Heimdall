package pullagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Limits.
const (
	// PresenceTTL is how long a process counts as connected after its last
	// poll; a poll waits at most MaxWait, so a live process polls well inside it.
	PresenceTTL = 45 * time.Second
	// MaxWait caps one long poll.
	MaxWait = 25 * time.Second
	// DefaultTurnTimeout is how long a turn waits for an answer by default.
	DefaultTurnTimeout = 5 * time.Minute
	// MaxTurnTimeout caps a turn's wait.
	MaxTurnTimeout = 2 * time.Hour
	// MaxQueued caps the turns waiting for one agent.
	MaxQueued = 512
	// MaxTurnBytes caps a turn's payload, MaxAnswerBytes an answer.
	MaxTurnBytes   = 8 << 20
	MaxAnswerBytes = 16 << 20
	// MaxRepoBytes caps one repository tarball, MaxOrgRepoBytes what one
	// org holds at once.
	MaxRepoBytes    = 48 << 20
	MaxOrgRepoBytes = 512 << 20
	// keepDone is how long an answered or failed turn stays readable.
	keepDone = 10 * time.Minute
)

// Turn states.
const (
	StatePending  = "pending"  // queued, no process took it yet
	StateLeased   = "leased"   // a process took it and has not answered
	StateAnswered = "answered" // the answer is in
	StateFailed   = "failed"   // no answer in time, or refused
)

// Broker errors.
var (
	ErrNoPuller   = errors.New("no process is serving this agent")
	ErrQueueFull  = errors.New("too many turns are waiting for this agent")
	ErrNoTurn     = errors.New("no such turn")
	ErrNotLeased  = errors.New("this turn is not held by a serving process (it was answered, timed out, or never taken)")
	ErrTooLarge   = errors.New("payload too large")
	ErrRepoQuota  = errors.New("this organization holds too many repository tarballs right now")
	ErrRepoAbsent = errors.New("the repository is no longer available")
)

// NoPullerReason is the refusal a run gets when nothing serves its agent.
func NoPullerReason(agent string) string {
	return fmt.Sprintf("no process is serving pull agent %q: start heimdall.serve(agent=%q, fn=...) with an API token of this organization, then run the evaluation again", agent, agent)
}

type agentKey struct{ org, agent string }

type presence struct {
	last    time.Time
	waiting int
	clients map[string]time.Time
}

// Turn is one unit of work for a serving process.
type Turn struct {
	ID       string          `json:"id"`
	Agent    string          `json:"agent"`
	Run      string          `json:"run_id"`
	Payload  json.RawMessage `json:"payload"`
	Created  time.Time       `json:"created_at"`
	Deadline time.Time       `json:"deadline"`

	org      string
	state    string
	leasedBy string
	answer   json.RawMessage
	reason   string
	finished time.Time
	done     chan struct{}
}

type repo struct {
	org, run string
	data     []byte
	sha      string
	exp      time.Time
}

// Broker holds the queues, the turns and the repository tarballs.
type Broker struct {
	mu       sync.Mutex
	now      func() time.Time
	pullers  map[agentKey]*presence
	queues   map[agentKey][]*Turn
	turns    map[string]*Turn
	notify   map[agentKey]chan struct{}
	repos    map[string]*repo
	orgBytes map[string]int
}

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{
		now: time.Now, pullers: map[agentKey]*presence{}, queues: map[agentKey][]*Turn{},
		turns: map[string]*Turn{}, notify: map[agentKey]chan struct{}{}, repos: map[string]*repo{}, orgBytes: map[string]int{},
	}
}

func newID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("pullagent: random id: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

// connectedLocked reports whether a process served k recently.
func (b *Broker) connectedLocked(k agentKey, now time.Time) bool {
	p := b.pullers[k]
	return p != nil && (p.waiting > 0 || now.Sub(p.last) <= PresenceTTL)
}

// Connected reports whether a process serves org's agent, and when it last polled.
func (b *Broker) Connected(org, agent string) (bool, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := agentKey{org, agent}
	p := b.pullers[k]
	if p == nil {
		return false, time.Time{}
	}
	return b.connectedLocked(k, b.now()), p.last
}

// AgentStatus is one served agent of an org.
type AgentStatus struct {
	Agent     string    `json:"agent"`
	Connected bool      `json:"connected"`
	LastPoll  time.Time `json:"last_poll"`
	Processes int       `json:"processes"` // distinct client ids seen within the presence window
	Queued    int       `json:"queued"`
	InFlight  int       `json:"in_flight"`
}

// Agents lists org's served agents, sorted by name.
func (b *Broker) Agents(org string) []AgentStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweepLocked(now)
	inflight := map[string]int{}
	for _, t := range b.turns {
		if t.org == org && t.state == StateLeased {
			inflight[t.Agent]++
		}
	}
	out := []AgentStatus{}
	for k, p := range b.pullers {
		if k.org != org {
			continue
		}
		procs := 0
		for _, seen := range p.clients {
			if now.Sub(seen) <= PresenceTTL {
				procs++
			}
		}
		out = append(out, AgentStatus{Agent: k.agent, Connected: b.connectedLocked(k, now), LastPoll: p.last,
			Processes: procs, Queued: len(b.queues[k]), InFlight: inflight[k.agent]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}

// Submit queues a turn of run c for its agent. It refuses with ErrNoPuller
// when no process served the agent within PresenceTTL.
func (b *Broker) Submit(c Claims, payload json.RawMessage, timeout time.Duration) (*Turn, error) {
	if len(payload) > MaxTurnBytes {
		return nil, ErrTooLarge
	}
	if timeout <= 0 {
		timeout = DefaultTurnTimeout
	}
	timeout = min(timeout, MaxTurnTimeout)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweepLocked(now)
	k := agentKey{c.Org, c.Agent}
	if !b.connectedLocked(k, now) {
		return nil, ErrNoPuller
	}
	if len(b.queues[k]) >= MaxQueued {
		return nil, ErrQueueFull
	}
	t := &Turn{ID: newID("turn_"), Agent: c.Agent, Run: c.Run, Payload: payload, Created: now, Deadline: now.Add(timeout),
		org: c.Org, state: StatePending, done: make(chan struct{})}
	if exp := time.Unix(c.Exp, 0); exp.Before(t.Deadline) {
		t.Deadline = exp
	}
	b.turns[t.ID] = t
	b.queues[k] = append(b.queues[k], t)
	b.wakeLocked(k)
	return t, nil
}

// ServedBy is the client name of turns this server answers itself.
const ServedBy = "heimdall-server"

// SubmitServed queues a turn of run c that this server answers itself:
// serve runs in its own goroutine with a context that ends at the turn's
// deadline, and its answer (or error) ends the turn exactly as a serving
// process's would, so the launcher reads it the same way (Wait). No
// process needs to be connected.
func (b *Broker) SubmitServed(c Claims, payload json.RawMessage, timeout time.Duration, serve func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error)) (*Turn, error) {
	if len(payload) > MaxTurnBytes {
		return nil, ErrTooLarge
	}
	if timeout <= 0 {
		timeout = DefaultTurnTimeout
	}
	timeout = min(timeout, MaxTurnTimeout)
	b.mu.Lock()
	now := b.now()
	b.sweepLocked(now)
	t := &Turn{ID: newID("turn_"), Agent: c.Agent, Run: c.Run, Payload: payload, Created: now, Deadline: now.Add(timeout),
		org: c.Org, state: StateLeased, leasedBy: ServedBy, done: make(chan struct{})}
	if exp := time.Unix(c.Exp, 0); exp.Before(t.Deadline) {
		t.Deadline = exp
	}
	b.turns[t.ID] = t
	deadline := t.Deadline
	b.mu.Unlock()
	go func() {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		answer, err := serve(ctx, payload)
		b.mu.Lock()
		defer b.mu.Unlock()
		switch {
		case err != nil:
			b.failLocked(t, err.Error())
		case len(answer) > MaxAnswerBytes:
			b.failLocked(t, "the answer is larger than the turn limit")
		case t.state == StateLeased:
			t.state, t.answer, t.finished = StateAnswered, append(json.RawMessage(nil), answer...), b.now()
			close(t.done)
		}
	}()
	return t, nil
}

func (b *Broker) wakeLocked(k agentKey) {
	if ch, ok := b.notify[k]; ok {
		close(ch)
		delete(b.notify, k)
	}
}

func (b *Broker) waitChanLocked(k agentKey) chan struct{} {
	ch, ok := b.notify[k]
	if !ok {
		ch = make(chan struct{})
		b.notify[k] = ch
	}
	return ch
}

// Next hands the oldest pending turn of org's agent to client, waiting up
// to wait for one. It returns nil, nil when none came. Every call counts as
// the process being connected.
func (b *Broker) Next(ctx context.Context, org, agent, client string, wait time.Duration) (*Turn, error) {
	wait = min(max(wait, 0), MaxWait)
	k := agentKey{org, agent}
	b.mu.Lock()
	p := b.pullers[k]
	if p == nil {
		p = &presence{clients: map[string]time.Time{}}
		b.pullers[k] = p
	}
	p.waiting++
	defer func() {
		b.mu.Lock()
		p.waiting--
		p.last = b.now()
		if client != "" {
			p.clients[client] = p.last
		}
		b.mu.Unlock()
	}()
	p.last = b.now()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		now := b.now()
		b.sweepLocked(now)
		if t := b.popLocked(k, now); t != nil {
			t.state, t.leasedBy = StateLeased, client
			b.mu.Unlock()
			return t, nil
		}
		if wait == 0 { // a plain check: no wait
			b.mu.Unlock()
			return nil, nil
		}
		ch := b.waitChanLocked(k)
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, nil
		case <-ch:
		}
		b.mu.Lock()
	}
}

func (b *Broker) popLocked(k agentKey, now time.Time) *Turn {
	q := b.queues[k]
	for len(q) > 0 {
		t := q[0]
		q = q[1:]
		if t.state == StatePending && now.Before(t.Deadline) {
			b.queues[k] = q
			return t
		}
	}
	delete(b.queues, k)
	return nil
}

// Answer records the answer of a turn held by org's process. The answer
// is opaque JSON the run's launcher interprets.
func (b *Broker) Answer(org, id string, answer json.RawMessage) error {
	if len(answer) > MaxAnswerBytes {
		return ErrTooLarge
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.turns[id]
	if t == nil || t.org != org {
		return ErrNoTurn
	}
	if t.state != StateLeased {
		return ErrNotLeased
	}
	t.state, t.answer, t.finished = StateAnswered, append(json.RawMessage(nil), answer...), b.now()
	close(t.done)
	return nil
}

// Fail ends a turn held by org's process with the process's own error.
func (b *Broker) Fail(org, id, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.turns[id]
	if t == nil || t.org != org {
		return ErrNoTurn
	}
	if t.state != StateLeased {
		return ErrNotLeased
	}
	b.failLocked(t, "the agent's process reported an error: "+reason)
	return nil
}

func (b *Broker) failLocked(t *Turn, reason string) {
	if t.state == StateAnswered || t.state == StateFailed {
		return
	}
	t.state, t.reason, t.finished = StateFailed, reason, b.now()
	close(t.done)
}

// Result is what a run reads back for a turn.
type Result struct {
	State  string          `json:"status"`
	Answer json.RawMessage `json:"answer,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Wait waits up to wait for the answer of turn id of org's run.
func (b *Broker) Wait(ctx context.Context, org, run, id string, wait time.Duration) (Result, error) {
	wait = min(max(wait, 0), MaxWait)
	b.mu.Lock()
	b.sweepLocked(b.now())
	t := b.turns[id]
	if t == nil || t.Run != run || t.org != org {
		b.mu.Unlock()
		return Result{}, ErrNoTurn
	}
	done := t.done
	b.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked(b.now())
	switch t.state {
	case StateAnswered:
		return Result{State: StateAnswered, Answer: t.answer}, nil
	case StateFailed:
		return Result{State: StateFailed, Error: t.reason}, nil
	}
	return Result{State: t.state}, nil
}

// sweepLocked fails turns past their deadline and forgets old turns and
// expired repositories.
func (b *Broker) sweepLocked(now time.Time) {
	for id, t := range b.turns {
		if (t.state == StatePending || t.state == StateLeased) && !now.Before(t.Deadline) {
			who := "no process took the turn"
			if t.state == StateLeased {
				who = "the serving process took the turn and did not answer"
			}
			b.failLocked(t, fmt.Sprintf("%s within %s", who, t.Deadline.Sub(t.Created).Round(time.Second)))
		}
		if (t.state == StateAnswered || t.state == StateFailed) && now.Sub(t.finished) > keepDone {
			delete(b.turns, id)
		}
	}
	for id, r := range b.repos {
		if now.After(r.exp) {
			b.orgBytes[r.org] -= len(r.data)
			delete(b.repos, id)
		}
	}
}

// RepoInfo describes a stored tarball.
type RepoInfo struct {
	ID      string    `json:"id"`
	SHA256  string    `json:"sha256"`
	Bytes   int       `json:"bytes"`
	Expires time.Time `json:"expires_at"`
}

// PutRepo keeps a workspace tarball of run c until exp.
func (b *Broker) PutRepo(c Claims, data []byte, exp time.Time) (RepoInfo, error) {
	if len(data) > MaxRepoBytes {
		return RepoInfo{}, ErrTooLarge
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.sweepLocked(now)
	if b.orgBytes[c.Org]+len(data) > MaxOrgRepoBytes {
		return RepoInfo{}, ErrRepoQuota
	}
	sum := sha256.Sum256(data)
	r := &repo{org: c.Org, run: c.Run, data: append([]byte(nil), data...), sha: hex.EncodeToString(sum[:]), exp: exp}
	id := newID("repo_")
	b.repos[id] = r
	b.orgBytes[c.Org] += len(data)
	return RepoInfo{ID: id, SHA256: r.sha, Bytes: len(data), Expires: exp}, nil
}

// Repo returns a stored tarball.
func (b *Broker) Repo(id string) ([]byte, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked(b.now())
	r := b.repos[id]
	if r == nil {
		return nil, "", ErrRepoAbsent
	}
	return r.data, r.sha, nil
}

var shared = NewBroker()

// Shared is the process's broker: the one the server's pull routes serve.
func Shared() *Broker { return shared }
