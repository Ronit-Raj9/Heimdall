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
// run, it waits until enough are free or ctx ends. F1: when another bench
// process has claimed the free slots at the fault proxy, any run waits up to
// the claim wait (Config.ClaimWait) and then fails with that reason.
func (g *Gateway) acquireRunSlots(ctx context.Context, p ProviderSetup, runID string, n int) (Slot, string, error) {
	if n > len(p.Slots) {
		return Slot{}, "", fmt.Errorf("a team of %d members needs %d key slots; provider %s has %d (gateway.key_slots)", n, n, p.Name, len(p.Slots))
	}
	var heldSince time.Time
	for {
		s, id, wait, err := g.tryRunSlots(ctx, p, runID, n)
		if !wait {
			return s, id, err
		}
		poll := slotPoll
		var he *heldElsewhereError
		if errors.As(err, &he) { // F1: another bench process holds them (slotclaims.go)
			poll = claimPoll
			switch {
			case heldSince.IsZero():
				heldSince = time.Now()
				g.logger.Warn("waiting for a key slot: every free slot is claimed by another bench process on this gateway",
					"run_id", runID, "provider", p.Name, "needed", n, "held", he.holders(), "max_wait", g.claimWait().String())
			case time.Since(heldSince) >= g.claimWait():
				return Slot{}, "", fmt.Errorf("%w; gave up after %s: the other process frees a slot when its run ends, and a crashed process's claims expire after %s",
					err, g.claimWait(), g.claimTTL())
			}
		}
		select {
		case <-ctx.Done():
			return Slot{}, "", fmt.Errorf("wait for %d free key slots of %s: %w (last: %v)", n, p.Name, ctx.Err(), err)
		case <-time.After(poll):
		}
	}
}

// tryRunSlots takes n free slots, or none. wait says whether the caller
// should try again later rather than fail. Without claims it works under one
// lock; with claims (F1) it holds takeMu while it claims the slots at the
// fault proxy, all n in one call, and records them only once the proxy
// granted them.
func (g *Gateway) tryRunSlots(ctx context.Context, p ProviderSetup, runID string, n int) (Slot, string, bool, error) {
	claims, err := g.claimsFor(ctx, p)
	if err != nil {
		return Slot{}, "", false, err
	}
	if claims {
		g.claims.takeMu.Lock()
		defer g.claims.takeMu.Unlock()
	}
	g.mu.Lock()
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
		g.mu.Unlock()
		return Slot{}, "", false, errors.New("gateway not set up: Setup has not run")
	}
	if len(free) < n {
		g.mu.Unlock()
		err := fmt.Errorf("no free key slot for %s: %d of %d in use, %d needed", p.Name, busy, len(p.Slots), n)
		return Slot{}, "", n > 1 || teams, err
	}
	if !claims {
		defer g.mu.Unlock()
		g.reserveLocked(runID, free[:n])
		return free[0], g.keyIDs[free[0].Name], false, nil
	}
	g.mu.Unlock()
	claimStart := time.Now()
	got, err := g.claimSlots(ctx, p, runID, free, n)
	if err != nil {
		var he *heldElsewhereError
		return Slot{}, "", errors.As(err, &he), err
	}
	g.mu.Lock()
	g.reserveLocked(runID, got)
	for _, s := range got {
		g.claims.claimed[s.Name] = runID
		g.claims.until[s.Name] = claimStart.Add(g.claimTTL())
	}
	id := g.keyIDs[got[0].Name]
	g.mu.Unlock()
	g.startRenewal()
	return got[0], id, false, nil
}

// reserveLocked marks the first slot pending for runID and reserves the
// rest for its team. Called with g.mu held.
func (g *Gateway) reserveLocked(runID string, slots []Slot) {
	g.inUse[slots[0].Name] = "pending:" + runID
	for _, s := range slots[1:] {
		g.inUse[s.Name] = reservedPrefix + runID
	}
}

// isTeamHolder reports whether a slot holder is a member key of a team run.
// Called with g.mu held.
func (g *Gateway) isTeamHolder(holder string) bool {
	_, ok := g.memberOf[holder]
	return ok
}

// takeReserved takes one slot the run reserved for a member key, or, when
// none is left, any free slot (F1: claimed at the fault proxy for the member
// key's name when claims are on).
func (g *Gateway) takeReserved(ctx context.Context, p ProviderSetup, runID, name string) (Slot, string, error) {
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
	if on, err := g.claimsFor(ctx, p); err != nil || on {
		if err != nil {
			return Slot{}, "", err
		}
		s, id, _, err := g.tryRunSlots(ctx, p, name, 1)
		return s, id, err
	}
	return g.acquireSlot(p, name)
}

// releaseReserved frees the slots a run reserved and never used, and their
// claims at the fault proxy (F1).
func (g *Gateway) releaseReserved(runID string) {
	drop := map[string]string{}
	g.mu.Lock()
	for name, holder := range g.inUse {
		if holder == reservedPrefix+runID {
			delete(g.inUse, name)
			g.unclaimLocked(name, drop)
		}
	}
	g.mu.Unlock()
	g.releaseClaims(drop)
}
