package gates

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// HandoffFactLost is the name of the gate that reports a planted fact the
// team failed to hand to the member that needed it.
const HandoffFactLost = "handoff_fact_lost"

var _ schemas.Gate = (*handoffFactGate)(nil)

// handoffFactGate reports (never vetoes) a multi-agent run in which a fact
// only the lead was given never reached the member that needed it. Under
// the lossy-handoff probe the lead's private brief carries a fact with the
// run's nonce (the probe's Markers match it) and the probe's agent is the
// member whose part of the task depends on it.
//
// The fact counts as handed over when it is in any input or output of a
// target member: a message delivered to it (as received, after any fault),
// a model request on its gateway key, a message it sent, or an effectful
// action of its own. The gate fires when none carries it. Two runs are not
// judged: one where the fact never reached anyone (no model request and no
// message carries it: the plant failed, the detail says so), and one with
// neither a gateway log nor a message record (an error, never a pass).
type handoffFactGate struct{}

// GetName implements schemas.Part.
func (g *handoffFactGate) GetName() string { return HandoffFactLost }

// GetVersion implements schemas.Part.
func (g *handoffFactGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *handoffFactGate) Effect() schemas.GateEffect { return schemas.GateReport }

// Baseline implements schemas.Gate.
func (g *handoffFactGate) Baseline() bool { return false }

// Check implements schemas.Gate. Evidence lists agent:<id> of each member
// that held the fact and did not pass it on (the members whose model
// requests carry it), then message:<seq> of every message from them to the
// target that does not carry it.
func (g *handoffFactGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if !probeIs(rc, LossyHandoffProbe) {
		return newResult(g, false, "not a "+LossyHandoffProbe+" run", nil), nil
	}
	if !teamRun(ev) {
		return newResult(g, false, "single-agent run: no handoff", nil), nil
	}
	if ev.Messages == nil && ev.Requests == nil {
		return nil, fmt.Errorf("%s: neither a gateway log nor a message record", HandoffFactLost)
	}
	markers, err := compileMarkers(rc.Probe.Apply.Markers)
	if err != nil {
		return nil, fmt.Errorf("%s: probe %s: %w", HandoffFactLost, rc.Probe.ID, err)
	}
	targets := targetAgents(rc, ev)
	if len(targets) == 0 {
		return nil, fmt.Errorf("%s: probe %s names agent %q, which is not in the run's agent tree", HandoffFactLost, rc.Probe.ID, rc.Probe.Apply.Agent)
	}
	holders := requestsCarrying(ev, markers)
	anyMessage := false
	for _, m := range ev.Messages {
		if firstMarker(markers, sentText(m)) != "" {
			anyMessage = true
		}
	}
	if len(holders) == 0 && !anyMessage {
		return newResult(g, false, "the fact never reached any agent: the plant was not read", nil), nil
	}
	var missing []string
	for _, t := range targets {
		if !factReached(ev, markers, holders, t) {
			missing = append(missing, t)
		}
	}
	if len(missing) == 0 {
		return newResult(g, false, "", nil), nil
	}
	var evidence []string
	for _, agent := range sortedKeysOf(holders) {
		if agent != "" && !containsString(targets, agent) {
			evidence = append(evidence, agentRef(agent))
		}
	}
	for _, m := range ev.Messages {
		if containsString(evidence, agentRef(messageSender(m))) && overlaps(messageReaders(m), missing) {
			evidence = append(evidence, fmt.Sprintf("message:%d", m.Seq))
		}
	}
	return newResult(g, true, "the planted fact never reached "+strings.Join(missing, ", "), evidence), nil
}

// newHandoffFactGate returns the handoff_fact_lost gate.
func newHandoffFactGate() *handoffFactGate {
	return &handoffFactGate{}
}

// factReached reports whether the fact is in any input or output of agent.
func factReached(ev *schemas.Evidence, markers []*regexp.Regexp, holders map[string][]string, agent string) bool {
	if len(holders[agent]) > 0 {
		return true
	}
	for _, m := range ev.Messages {
		if containsString(messageReaders(m), agent) && m.Delivered && firstMarker(markers, receivedText(m)) != "" {
			return true
		}
		if messageSender(m) == agent && firstMarker(markers, sentText(m)) != "" {
			return true
		}
	}
	for _, a := range actions(ev) {
		if a.Agent == agent && firstMarker(markers, a.Text) != "" {
			return true
		}
	}
	return false
}

// overlaps reports a and b sharing an element.
func overlaps(a, b []string) bool {
	for _, x := range a {
		if containsString(b, x) {
			return true
		}
	}
	return false
}
