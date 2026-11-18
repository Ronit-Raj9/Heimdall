package bifrost

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Key slot ownership across bench processes (docs/DESIGN.md 13.3, "Key slot
// ownership"). A slot is one Bifrost provider key and one fault proxy plan.
// The in-process reservation (g.inUse) keeps the runs of one process apart;
// it cannot see another process on the same gateway. So, when the fault
// proxy has the claim API, a mint also claims its slots there with this
// process's owner token before it creates the virtual key: a slot another
// process holds is skipped, and when every free slot is held elsewhere the
// mint waits (Config.ClaimWait) and then fails with a reason. A team run
// claims all its slots in one call or none. Claims are renewed while the
// process holds them and released when a key is revoked and at process exit
// (Close); a crashed process's claims expire after their TTL. The proxy
// refuses any plan write on a claimed slot from another owner, so a run's
// plan can never be replaced under it.
//
// A fault proxy without the claim API (an older image) is detected from
// /health: the bench warns once and keeps today's process-local reservation.
// A direct provider has no fault proxy and no plan, so its slots stay
// process-local too.

// SlotClaimer is the fault proxy's claim API. *faultproxy.Client implements
// it; the same client must make the bench's plan writes, because the proxy
// matches them to the claim by its owner token.
type SlotClaimer interface {
	Capabilities(ctx context.Context) (*faultproxy.Health, error)
	ClaimSlots(ctx context.Context, req faultproxy.ClaimRequest) (*faultproxy.ClaimResult, error)
	RenewSlots(ctx context.Context, req faultproxy.RenewRequest) (*faultproxy.RenewResult, error)
	ReleaseSlots(ctx context.Context, req faultproxy.ReleaseRequest) error
	Claims(ctx context.Context) ([]faultproxy.SlotClaim, error)
	OwnerID() string
}

var _ SlotClaimer = (*faultproxy.Client)(nil)

// Slot ownership modes.
const (
	OwnershipFaultProxy   = "fault_proxy"   // claims at the fault proxy keep processes apart
	OwnershipProcessLocal = "process_local" // only this process's memory does
)

// DefaultClaimWait is how long a mint waits while every free slot of its
// provider is claimed by another bench process.
const DefaultClaimWait = 2 * time.Minute

// claimCallTimeout bounds one claim API call.
const claimCallTimeout = 10 * time.Second

// claimPoll is how often a waiting mint asks the proxy again.
var claimPoll = time.Second

// claimState is the gateway's view of its claims.
type claimState struct {
	modeMu sync.Mutex
	mode   string // "" until detected

	// takeMu serializes everything this process changes at the proxy:
	// claims, renewals and releases. A slot taker holds it from reading the
	// free slots to recording what it claimed, so no other run of this
	// process takes the same slot in between. Lock order: takeMu, then g.mu.
	takeMu sync.Mutex

	// Guarded by g.mu.
	claimed  map[string]string // slot -> the run label it is claimed for at the proxy
	renewing bool
	closed   bool
	direct   map[string]bool // direct providers already noted in the log

	stop     chan struct{}
	stopOnce sync.Once
}

func newClaimState() *claimState {
	return &claimState{claimed: map[string]string{}, direct: map[string]bool{}, stop: make(chan struct{})}
}

// heldElsewhereError is a slot claim refused because another bench process
// holds the free slots. The mint waits and tries again.
type heldElsewhereError struct {
	provider string
	want     int
	offered  int
	held     []faultproxy.SlotClaim
}

func (e *heldElsewhereError) Error() string {
	return fmt.Sprintf("no free key slot for %s: %d needed, %d of the %d free in this process are claimed by another bench process on this gateway (%s)",
		e.provider, e.want, len(e.held), e.offered, e.holders())
}

func (e *heldElsewhereError) holders() string {
	parts := make([]string, 0, len(e.held))
	for _, c := range e.held {
		parts = append(parts, c.String())
	}
	return strings.Join(parts, "; ")
}

func (g *Gateway) claimTTL() time.Duration {
	if g.cfg.ClaimTTL > 0 {
		return g.cfg.ClaimTTL
	}
	return faultproxy.DefaultClaimTTL
}

func (g *Gateway) claimWait() time.Duration {
	if g.cfg.ClaimWait > 0 {
		return g.cfg.ClaimWait
	}
	return DefaultClaimWait
}

func (g *Gateway) ttlSeconds() int {
	s := int(g.claimTTL() / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

// claimsFor reports whether slots of p are claimed at the fault proxy.
func (g *Gateway) claimsFor(ctx context.Context, p ProviderSetup) (bool, error) {
	if g.cfg.Claims == nil {
		return false, nil
	}
	if p.Mode == ModeDirect {
		g.mu.Lock()
		first := !g.claims.direct[p.Name]
		g.claims.direct[p.Name] = true
		g.mu.Unlock()
		if first {
			g.logger.Info("key slot ownership of a direct provider is process-local: no fault proxy, no plan to protect", "provider", p.Name)
		}
		return false, nil
	}
	mode, err := g.ownershipMode(ctx)
	return mode == OwnershipFaultProxy, err
}

// ownershipMode detects once whether the fault proxy enforces claims. A
// failed check is not cached: the proxy may simply be starting.
func (g *Gateway) ownershipMode(ctx context.Context) (string, error) {
	c := g.claims
	c.modeMu.Lock()
	defer c.modeMu.Unlock()
	if c.mode != "" {
		return c.mode, nil
	}
	cctx, cancel := context.WithTimeout(ctx, claimCallTimeout)
	defer cancel()
	h, err := g.cfg.Claims.Capabilities(cctx)
	if err != nil {
		return "", fmt.Errorf("check key slot ownership at the fault proxy: %w", err)
	}
	if !h.SlotClaims {
		c.mode = OwnershipProcessLocal
		g.logger.Warn("key slot ownership: process-local only (fault proxy too old): another bench process on this gateway " +
			"can take the same key slot and replace this process's fault plans; run one bench process per gateway or update the fault proxy image")
		return c.mode, nil
	}
	c.mode = OwnershipFaultProxy
	g.logger.Info("key slot ownership: claims at the fault proxy", "owner", g.cfg.Claims.OwnerID(), "ttl_s", g.ttlSeconds())
	return c.mode, nil
}

// claimSlots claims n of free at the proxy for runID, all or none, and
// returns the slots claimed in the order offered.
func (g *Gateway) claimSlots(ctx context.Context, p ProviderSetup, runID string, free []Slot, n int) ([]Slot, error) {
	names := make([]string, 0, len(free))
	byName := make(map[string]Slot, len(free))
	for _, s := range free {
		names = append(names, s.Name)
		byName[s.Name] = s
	}
	cctx, cancel := context.WithTimeout(ctx, claimCallTimeout)
	defer cancel()
	res, err := g.cfg.Claims.ClaimSlots(cctx, faultproxy.ClaimRequest{Slots: names, Count: n, TTLS: g.ttlSeconds(), RunID: runID})
	var cc *faultproxy.ClaimConflictError
	switch {
	case errors.As(err, &cc):
		return nil, &heldElsewhereError{provider: p.Name, want: n, offered: len(free), held: cc.Held}
	case errors.Is(err, faultproxy.ErrClaimsUnsupported):
		g.claims.modeMu.Lock()
		g.claims.mode = "" // the proxy was replaced by an older one: detect again on the next mint
		g.claims.modeMu.Unlock()
		return nil, fmt.Errorf("claim key slots at the fault proxy: %w", err)
	case err != nil:
		return nil, fmt.Errorf("claim key slots at the fault proxy: %w", err)
	}
	got := make([]Slot, 0, len(res.Claimed))
	for _, name := range res.Claimed {
		s, ok := byName[name]
		if !ok {
			g.dropClaims(map[string]string{name: runID})
			return nil, fmt.Errorf("claim key slots at the fault proxy: it claimed slot %s, which was not offered", name)
		}
		got = append(got, s)
	}
	if len(got) != n {
		drop := map[string]string{}
		for _, s := range got {
			drop[s.Name] = runID
		}
		g.dropClaims(drop)
		return nil, fmt.Errorf("claim key slots at the fault proxy: %d claimed, %d wanted", len(got), n)
	}
	return got, nil
}

// unclaimLocked forgets this process's claim on slot and returns it for
// releaseClaims. Called with g.mu held.
func (g *Gateway) unclaimLocked(slot string, drop map[string]string) {
	if run, ok := g.claims.claimed[slot]; ok {
		drop[slot] = run
		delete(g.claims.claimed, slot)
	}
}

// releaseClaims releases claims at the proxy, after the slots were freed in
// this process. Each release names the run it was claimed for, so when
// another run of this process claimed the slot again in between, the proxy
// keeps that newer claim.
func (g *Gateway) releaseClaims(drop map[string]string) {
	if len(drop) == 0 || g.cfg.Claims == nil {
		return
	}
	g.claims.takeMu.Lock()
	defer g.claims.takeMu.Unlock()
	g.dropClaims(drop)
}

// dropClaims sends the releases. Called with takeMu held. A failed release
// is logged, not returned: the claim expires after its TTL anyway.
func (g *Gateway) dropClaims(drop map[string]string) {
	byRun := map[string][]string{}
	for s, run := range drop {
		byRun[run] = append(byRun[run], s)
	}
	for run, slots := range byRun {
		sort.Strings(slots)
		ctx, cancel := context.WithTimeout(context.Background(), claimCallTimeout)
		err := g.cfg.Claims.ReleaseSlots(ctx, faultproxy.ReleaseRequest{Slots: slots, RunID: run})
		cancel()
		if err != nil {
			g.logger.Warn("release key slot claims failed; they expire after their TTL", "run_id", run,
				"slots", strings.Join(slots, ","), "ttl_s", g.ttlSeconds(), "error", err.Error())
		}
	}
}

// startRenewal starts the renewal loop unless it runs already.
func (g *Gateway) startRenewal() {
	g.mu.Lock()
	if g.claims.renewing || g.claims.closed || len(g.claims.claimed) == 0 {
		g.mu.Unlock()
		return
	}
	g.claims.renewing = true
	g.mu.Unlock()
	go g.renewLoop()
}

// renewLoop renews this process's claims every third of their TTL, until it
// holds none or Close stops it.
func (g *Gateway) renewLoop() {
	t := time.NewTicker(g.claimTTL() / 3)
	defer t.Stop()
	for {
		select {
		case <-g.claims.stop:
			return
		case <-t.C:
		}
		if !g.renewOnce() {
			return
		}
	}
}

// renewOnce renews every claim this process holds. It returns false, and
// marks the loop stopped, when there is none. A slot another process
// claimed after this one's claim lapsed (renewals failed for a whole TTL) is
// logged as an error: its run may have lost its plan, and its teardown will
// be refused at the proxy.
func (g *Gateway) renewOnce() bool {
	c := g.claims
	c.takeMu.Lock()
	defer c.takeMu.Unlock()
	g.mu.Lock()
	if len(c.claimed) == 0 || c.closed {
		c.renewing = false
		g.mu.Unlock()
		return false
	}
	slots := make([]faultproxy.RenewSlot, 0, len(c.claimed))
	for s, run := range c.claimed {
		slots = append(slots, faultproxy.RenewSlot{Slot: s, RunID: run})
	}
	g.mu.Unlock()
	sort.Slice(slots, func(i, j int) bool { return slots[i].Slot < slots[j].Slot })
	ctx, cancel := context.WithTimeout(context.Background(), claimCallTimeout)
	defer cancel()
	res, err := g.cfg.Claims.RenewSlots(ctx, faultproxy.RenewRequest{Slots: slots, TTLS: g.ttlSeconds()})
	if err != nil {
		g.logger.Warn("renew key slot claims failed; they lapse if renewal keeps failing for a whole TTL", "slots", len(slots), "ttl_s", g.ttlSeconds(), "error", err.Error())
		return true
	}
	for _, l := range res.Lost {
		g.logger.Error("key slot claim lost to another bench process: this process's claim lapsed and the run on the slot may have lost its fault plan",
			"slot", l.Slot, "holder", l.String())
		g.mu.Lock()
		delete(c.claimed, l.Slot)
		g.mu.Unlock()
	}
	return true
}

// Close stops claim renewal and releases every claim this process holds.
// Call it before the process exits; a claim not released expires after its
// TTL. Safe to call more than once.
func (g *Gateway) Close(_ context.Context) error {
	if g.cfg.Claims == nil {
		return nil
	}
	c := g.claims
	c.stopOnce.Do(func() { close(c.stop) })
	c.takeMu.Lock()
	defer c.takeMu.Unlock()
	g.mu.Lock()
	drop := make(map[string]string, len(c.claimed))
	for s, run := range c.claimed {
		drop[s] = run
	}
	c.claimed = map[string]string{}
	c.closed = true
	c.renewing = false
	g.mu.Unlock()
	g.dropClaims(drop)
	return nil
}

// liveClaimRuns returns every run that holds a live claim at the fault
// proxy, by any owner, so the janitor keeps its keys and routing rules: a
// run of another bench process is not in this process's active set. nil
// when ownership is process-local.
func (g *Gateway) liveClaimRuns(ctx context.Context) (map[string]bool, error) {
	if g.cfg.Claims == nil || !g.anyProxied() {
		return nil, nil
	}
	mode, err := g.ownershipMode(ctx)
	if err != nil || mode != OwnershipFaultProxy {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, claimCallTimeout)
	defer cancel()
	claims, err := g.cfg.Claims.Claims(cctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, c := range claims {
		if c.RunID == "" {
			continue
		}
		out[c.RunID] = true
		if run, ok := staleRun(c.RunID); ok { // a member slot claimed under its member key name
			out[run] = true
		}
	}
	return out, nil
}

// anyProxied reports whether any provider goes through the fault proxy.
func (g *Gateway) anyProxied() bool {
	for _, p := range g.providers {
		if p.Mode == ModeProxied {
			return true
		}
	}
	return false
}

// SlotOwnership is doctor's "slot ownership" row: whether several bench
// processes can share this gateway safely.
func (g *Gateway) SlotOwnership(ctx context.Context) schemas.DoctorCheck {
	row := schemas.DoctorCheck{ID: "slot ownership", Status: schemas.DoctorWarn}
	var direct []string
	for _, p := range g.providers {
		if p.Mode == ModeDirect {
			direct = append(direct, p.Name)
		}
	}
	switch {
	case !g.anyProxied():
		row.Detail = "process-local only (direct mode, no fault proxy): run one bench process per gateway"
		return row
	case g.cfg.Claims == nil:
		row.Detail = "process-local only (no fault proxy control client)"
		return row
	}
	mode, err := g.ownershipMode(ctx)
	switch {
	case err != nil:
		row.Detail = "unknown: " + err.Error()
		return row
	case mode == OwnershipProcessLocal:
		row.Detail = "process-local only (fault proxy too old)"
		return row
	}
	claims, err := g.cfg.Claims.Claims(ctx)
	if err != nil {
		row.Detail = "claims at the fault proxy, but listing them failed: " + err.Error()
		return row
	}
	mine := g.cfg.Claims.OwnerID()
	others := 0
	for _, c := range claims {
		if c.OwnerID != mine {
			others++
		}
	}
	row.Status = schemas.DoctorPass
	row.Detail = fmt.Sprintf("claimed at the fault proxy (ttl %ds): %d slots claimed now, %d by other bench processes", g.ttlSeconds(), len(claims), others)
	if len(direct) > 0 {
		row.Detail += "; direct providers " + strings.Join(direct, ", ") + " stay process-local"
	}
	return row
}
