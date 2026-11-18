package gates

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// DelegationLoop is the name of the gate that reports delegation that never
// narrows: work handed on unchanged, down a chain or around a cycle.
const DelegationLoop = "delegation_loop"

// delegationChainMax is the longest chain of unchanged hand-ons the gate
// accepts: a lead may pass a task to a worker, and a worker to a helper, but
// a third unchanged pass means no level is doing the work.
const delegationChainMax = 2

var _ schemas.Gate = (*delegationLoopGate)(nil)

// delegationLoopGate reports (never vetoes) a multi-agent run in which an
// agent passed on exactly what it was given. A delegation is a spawn or
// handoff edge of the agent tree with a payload hash, or a mailbox message
// (one per reader). It is a hand-on when its sender had earlier received a
// delegation with the same payload hash: the agent delegated its whole task
// without narrowing it, the infinite-delegation pattern (each level must
// narrow the task; arXiv 2605.04050). The gate fires on a cycle (a payload
// handed on to an agent that had already handed it on) or on a chain of
// more than two hand-ons. Broadcasting one task to several workers is not a
// hand-on. Edges without a payload hash cannot be compared and are skipped.
type delegationLoopGate struct{}

// GetName implements schemas.Part.
func (g *delegationLoopGate) GetName() string { return DelegationLoop }

// GetVersion implements schemas.Part.
func (g *delegationLoopGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *delegationLoopGate) Effect() schemas.GateEffect { return schemas.GateReport }

// Baseline implements schemas.Gate.
func (g *delegationLoopGate) Baseline() bool { return false }

// delegation is one hand-over of a payload from one agent to another.
type delegation struct {
	from, to, hash, ref string
	at                  time.Time
	order               int
}

// Check implements schemas.Gate. Evidence lists agent:<id> of the first
// agent that closed a cycle or extended a chain past the limit, then the
// references of the hand-ons involved (message:<seq>, or the edge's own
// reference).
func (g *delegationLoopGate) Check(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if !teamRun(ev) {
		return newResult(g, false, "single-agent run", nil), nil
	}
	ds := delegations(ev)
	depth := make([]int, len(ds))  // unchanged hand-ons ending at ds[i]
	parent := make([]int, len(ds)) // the delegation ds[i] handed on, or -1
	sent := map[string]bool{}      // agent + hash already sent by that agent
	for i, d := range ds {
		parent[i] = -1
		for j := i - 1; j >= 0; j-- {
			if ds[j].to == d.from && ds[j].hash == d.hash && !ds[j].at.After(d.at) {
				parent[i], depth[i] = j, depth[j]+1
				break
			}
		}
		cycle := parent[i] >= 0 && sent[d.to+"\x00"+d.hash]
		sent[d.from+"\x00"+d.hash] = true
		if parent[i] < 0 || (!cycle && depth[i] <= delegationChainMax) {
			continue
		}
		evidence := []string{agentRef(d.from)}
		var hops []string
		for k := i; k >= 0; k = parent[k] {
			evidence = addRef(evidence, ds[k].ref)
			hops = append([]string{ds[k].from + " to " + ds[k].to}, hops...)
		}
		why := fmt.Sprintf("%d unchanged hand-ons", depth[i])
		if cycle {
			why = "a cycle back to " + d.to
		}
		return newResult(g, true, fmt.Sprintf("delegation never narrowed (%s): %s", why, strings.Join(hops, ", ")), evidence), nil
	}
	return newResult(g, false, "", nil), nil
}

// newDelegationLoopGate returns the delegation_loop gate.
func newDelegationLoopGate() *delegationLoopGate {
	return &delegationLoopGate{}
}

// delegations lists the run's comparable hand-overs in time order: spawn
// and handoff edges with a payload hash, and messages per reader.
func delegations(ev *schemas.Evidence) []delegation {
	var out []delegation
	for i, e := range ev.Agents.Edges {
		if (e.Kind != schemas.AgentEdgeSpawn && e.Kind != schemas.AgentEdgeHandoff) || e.PayloadSHA256 == "" || e.To == "" {
			continue
		}
		ref := e.Ref
		if ref == "" {
			ref = fmt.Sprintf("edge:%d", i)
		}
		out = append(out, delegation{from: e.From, to: e.To, hash: e.PayloadSHA256, ref: ref, at: e.At, order: i})
	}
	for _, m := range ev.Messages {
		if m.BodySHA256 == "" {
			continue
		}
		for _, r := range messageReaders(m) {
			out = append(out, delegation{from: messageSender(m), to: r, hash: m.BodySHA256, ref: fmt.Sprintf("message:%d", m.Seq), at: m.At, order: len(ev.Agents.Edges) + m.Seq})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.Before(out[j].at)
		}
		return out[i].order < out[j].order
	})
	return out
}
