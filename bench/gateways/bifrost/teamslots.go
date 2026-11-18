package bifrost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Key slots of team runs (docs/multi-agent.md). A team of N members needs N
// slots: the lead runs on the run's own key and slot, every other member on
// a slot of its own. The run takes all N slots at once or none, and waits
// while fewer are free, so two team runs can never each hold part of the
// pool and wait for the rest.

// reservedPrefix marks a slot a team run holds for a member key it has not
// minted yet.
const reservedPrefix = "reserved:"

// slotPoll is how often a waiting mint looks for free slots again.
var slotPoll = 200 * time.Millisecond

// teamSlots is how many key slots a run needs: one per team member, else one.
func teamSlots(rc *schemas.RunContext) int {
	if n := len(utils.TeamOf(rc)); n > 1 {
		return n
	}
	return 1
}

// KeySlots is how many key slots the gateway has for a provider (the
// provider's default when empty). The planner refuses a team that needs
// more.
func (g *Gateway) KeySlots(provider string) int {
	if provider == "" {
		provider = g.providers[0].Name
		if _, ok := g.provider(DefaultProvider); ok {
			provider = DefaultProvider
		}
	}
	p, ok := g.provider(provider)
	if !ok {
		return 0
	}
	return len(p.Slots)
}

// acquireRunSlots takes the run's slot and, for a team, reserves one more
// per non-lead member, all at once. A single-agent run fails at once when no
// slot is free, as before, unless a team run holds slots: then, like a team
// run, it waits until enough are free or ctx ends.
func (g *Gateway) acquireRunSlots(ctx context.Context, p ProviderSetup, runID string, n int) (Slot, string, error) {
	if n > len(p.Slots) {
		return Slot{}, "", fmt.Errorf("a team of %d members needs %d key slots; provider %s has %d (gateway.key_slots)", n, n, p.Name, len(p.Slots))
	}
	for {
		s, id, wait, err := g.tryRunSlots(p, runID, n)
		if !wait {
			return s, id, err
		}
		select {
		case <-ctx.Done():
			return Slot{}, "", fmt.Errorf("wait for %d free key slots of %s: %w (last: %v)", n, p.Name, ctx.Err(), err)
		case <-time.After(slotPoll):
		}
	}
}

// tryRunSlots takes n free slots under one lock, or none. wait says whether
// the caller should try again later rather than fail.
func (g *Gateway) tryRunSlots(p ProviderSetup, runID string, n int) (Slot, string, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var free []Slot
	busy, teams := 0, false
	for _, s := range p.Slots {
		if holder, used := g.inUse[s.Name]; used {
			busy++
			teams = teams || strings.HasPrefix(holder, reservedPrefix) || g.isTeamHolder(holder)
			continue
		}
		if _, ok := g.keyIDs[s.Name]; ok {
			free = append(free, s)
		}
	}
	if busy == 0 && len(free) == 0 {
		return Slot{}, "", false, errors.New("gateway not set up: Setup has not run")
	}
	if len(free) < n {
		err := fmt.Errorf("no free key slot for %s: %d of %d in use, %d needed", p.Name, busy, len(p.Slots), n)
		return Slot{}, "", n > 1 || teams, err
	}
	g.inUse[free[0].Name] = "pending:" + runID
	for _, s := range free[1:n] {
		g.inUse[s.Name] = reservedPrefix + runID
	}
	return free[0], g.keyIDs[free[0].Name], false, nil
}

// isTeamHolder reports whether a slot holder is a member key of a team run.
// Called with g.mu held.
func (g *Gateway) isTeamHolder(holder string) bool {
	_, ok := g.memberOf[holder]
	return ok
}

// takeReserved claims one slot the run reserved for a member key, or, when
// none is left, any free slot.
func (g *Gateway) takeReserved(p ProviderSetup, runID, name string) (Slot, string, error) {
	g.mu.Lock()
	for _, s := range p.Slots {
		if g.inUse[s.Name] == reservedPrefix+runID {
			g.inUse[s.Name] = "pending:" + name
			id := g.keyIDs[s.Name]
			g.mu.Unlock()
			return s, id, nil
		}
	}
	g.mu.Unlock()
	return g.acquireSlot(p, name)
}

// releaseReserved frees the slots a run reserved and never used.
func (g *Gateway) releaseReserved(runID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name, holder := range g.inUse {
		if holder == reservedPrefix+runID {
			delete(g.inUse, name)
		}
	}
}
