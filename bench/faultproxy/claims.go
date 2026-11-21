package faultproxy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Key slot claims (docs/DESIGN.md 13.3, "Key slot ownership"). A key slot is
// one provider key in Bifrost and one plan in this proxy. Each bench process
// reserves slots in its own memory, so two processes on one gateway could
// pick the same slot, and one would replace the other's plan while its run
// is live. A claim makes the proxy the one place that decides who owns a
// slot: a bench process claims slots with a secret owner token and a TTL,
// renews them while its runs live and releases them when a run's key is
// revoked. While a claim is live, only its owner may set or clear the slot's
// plan; any other write gets 409 and changes nothing. A claim that is not
// renewed expires, so a crashed process frees its slots after one TTL. A
// slot without a live claim accepts plan writes from anyone, which keeps
// benches older than claims working.
//
//	POST /claims          ClaimRequest; all of Count slots or none; 409 lists the claims in the way
//	POST /claims/renew    RenewRequest; renews each slot, or reports it lost to another owner
//	POST /claims/release  ReleaseRequest; drops the owner's claims
//	GET  /claims          {"claims":[SlotClaim...]}: every live claim, owner tokens never shown

const (
	// OwnerHeader carries the owner token on plan writes (PUT and DELETE
	// /plans/{slot}). A bench process sends it on every control request.
	OwnerHeader = "X-Slot-Owner"
	// DefaultClaimTTL is the TTL of a claim that names none. The bench renews
	// at a third of it.
	DefaultClaimTTL = time.Minute
	// MinClaimTTL and MaxClaimTTL bound a requested TTL.
	MinClaimTTL = time.Second
	MaxClaimTTL = 10 * time.Minute

	minOwnerLen  = 16
	maxOwnerLen  = 128
	maxLabelLen  = 128
	maxClaimBody = 64 << 10
)

// ErrClaimsUnsupported is returned by the claim calls of a Client talking to
// a proxy older than the claim API (it answers 404 or 405).
var ErrClaimsUnsupported = errors.New("fault proxy has no slot claim API")

// ClaimRequest is the body of POST /claims. The proxy takes the first Count
// slots of Slots, in order, that no other owner holds a live claim on, all
// at once or none.
type ClaimRequest struct {
	Owner string   `json:"owner"`            // secret; plan writes on a claimed slot carry it in X-Slot-Owner
	Slots []string `json:"slots"`            // candidate slots in preference order
	Count int      `json:"count,omitempty"`  // how many to take; 1 when 0
	TTLS  int      `json:"ttl_s,omitempty"`  // DefaultClaimTTL when 0
	RunID string   `json:"run_id,omitempty"` // the run the slots are for: shown to other owners, and a release naming another run keeps the claim
	Label string   `json:"label,omitempty"`  // who holds the claim, for messages (host and pid)
}

// ClaimResult is the answer to a claim that succeeded.
type ClaimResult struct {
	Claimed   []string  `json:"claimed"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RenewRequest is the body of POST /claims/renew. Each slot is renewed on
// its own: a slot this owner holds, or one no live claim holds (the claim
// lapsed, or the proxy restarted), is claimed again for its run; a slot
// another owner holds now is reported lost.
type RenewRequest struct {
	Owner string      `json:"owner"`
	Slots []RenewSlot `json:"slots"`
	TTLS  int         `json:"ttl_s,omitempty"`
	Label string      `json:"label,omitempty"`
}

// RenewSlot is one slot to renew and the run it is held for.
type RenewSlot struct {
	Slot  string `json:"slot"`
	RunID string `json:"run_id,omitempty"`
}

// RenewResult is the answer to a renewal.
type RenewResult struct {
	Renewed   []string    `json:"renewed"`
	Lost      []SlotClaim `json:"lost,omitempty"` // slots another owner claimed after this owner's claim lapsed
	ExpiresAt time.Time   `json:"expires_at"`
}

// ReleaseRequest is the body of POST /claims/release. Only the owner's own
// claims go; with RunID set, only those held for that run, so a late release
// from a run that already gave a slot up never frees it from the next run of
// the same process.
type ReleaseRequest struct {
	Owner string   `json:"owner"`
	Slots []string `json:"slots"`
	RunID string   `json:"run_id,omitempty"`
}

// SlotClaim is one live claim as GET /claims and a 409 report it. The owner
// token is never shown; OwnerID is a short hash of it, so a process can tell
// its own claims from another's.
type SlotClaim struct {
	Slot      string    `json:"slot"`
	OwnerID   string    `json:"owner_id"`
	RunID     string    `json:"run_id,omitempty"`
	Label     string    `json:"label,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// String describes the claim for an error message.
func (c SlotClaim) String() string {
	parts := []string{"owner " + c.OwnerID}
	if c.Label != "" {
		parts = append(parts, c.Label)
	}
	if c.RunID != "" {
		parts = append(parts, "run "+c.RunID)
	}
	return fmt.Sprintf("%s (%s, until %s)", c.Slot, strings.Join(parts, ", "), c.ExpiresAt.UTC().Format(time.RFC3339))
}

// ClaimConflictError is a claim the proxy refused because other owners hold
// too many of the offered slots. Held lists their live claims.
type ClaimConflictError struct {
	Msg  string
	Held []SlotClaim
}

func (e *ClaimConflictError) Error() string { return e.Msg }

// claimConflict is the body of a 409 from the claim API or a plan write.
type claimConflict struct {
	Error string      `json:"error"`
	Held  []SlotClaim `json:"held,omitempty"`
}

// OwnerID is the short public id of an owner token.
func OwnerID(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:6])
}

// slotClaim is one slot's claim. The owner token itself is not kept.
type slotClaim struct {
	owner   [sha256.Size]byte
	ownerID string
	runID   string
	label   string
	expires time.Time
}

func (c *slotClaim) public(slot string) SlotClaim {
	return SlotClaim{Slot: slot, OwnerID: c.ownerID, RunID: c.runID, Label: c.label, ExpiresAt: c.expires.UTC()}
}

func (c *slotClaim) ownedBy(h [sha256.Size]byte) bool {
	return subtle.ConstantTimeCompare(c.owner[:], h[:]) == 1
}

// liveClaim returns slot's claim while it has not expired. An expired claim
// is dropped. Called with t.mu held.
func (t *planTable) liveClaim(slot string) *slotClaim {
	c := t.claims[slot]
	if c == nil {
		return nil
	}
	if !t.now().Before(c.expires) {
		delete(t.claims, slot)
		return nil
	}
	return c
}

// claim takes count slots for owner, in the order offered, all or none. A
// slot is free when no live claim holds it or owner holds it already. On a
// shortfall nothing changes and the claims in the way come back.
func (t *planTable) claim(owner string, slots []string, count int, ttl time.Duration, runID, label string) ([]string, time.Time, []SlotClaim) {
	h := sha256.Sum256([]byte(owner))
	t.mu.Lock()
	defer t.mu.Unlock()
	var got []string
	var held []SlotClaim
	for _, s := range slots {
		if c := t.liveClaim(s); c != nil && !c.ownedBy(h) {
			held = append(held, c.public(s))
			continue
		}
		if len(got) < count {
			got = append(got, s)
		}
	}
	if len(got) < count {
		return nil, time.Time{}, held
	}
	exp := t.now().Add(ttl)
	for _, s := range got {
		t.claims[s] = &slotClaim{owner: h, ownerID: OwnerID(owner), runID: runID, label: label, expires: exp}
	}
	return got, exp, nil
}

// renew extends owner's claims, or claims again a slot no live claim holds.
func (t *planTable) renew(owner string, slots []RenewSlot, ttl time.Duration, label string) ([]string, []SlotClaim, time.Time) {
	h := sha256.Sum256([]byte(owner))
	t.mu.Lock()
	defer t.mu.Unlock()
	exp := t.now().Add(ttl)
	renewed := []string{}
	var lost []SlotClaim
	for _, s := range slots {
		if c := t.liveClaim(s.Slot); c != nil && !c.ownedBy(h) {
			lost = append(lost, c.public(s.Slot))
			continue
		}
		t.claims[s.Slot] = &slotClaim{owner: h, ownerID: OwnerID(owner), runID: s.RunID, label: label, expires: exp}
		renewed = append(renewed, s.Slot)
	}
	return renewed, lost, exp
}

// release drops owner's claims on slots (only those held for runID when it
// is set) and returns the slots released.
func (t *planTable) release(owner string, slots []string, runID string) []string {
	h := sha256.Sum256([]byte(owner))
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []string{}
	for _, s := range slots {
		c := t.claims[s]
		if c == nil || !c.ownedBy(h) || (runID != "" && c.runID != runID) {
			continue
		}
		delete(t.claims, s)
		out = append(out, s)
	}
	return out
}

// listClaims returns every live claim, by slot name.
func (t *planTable) listClaims() []SlotClaim {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []SlotClaim{}
	for s := range t.claims {
		if c := t.liveClaim(s); c != nil {
			out = append(out, c.public(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// heldAgainst returns the live claim that stops owner writing slot's plan,
// or nil when owner may write it. Called with t.mu held.
func (t *planTable) heldAgainst(slot, owner string) *SlotClaim {
	c := t.liveClaim(slot)
	if c == nil {
		return nil
	}
	if owner != "" && c.ownedBy(sha256.Sum256([]byte(owner))) {
		return nil
	}
	pc := c.public(slot)
	return &pc
}

// setOwned binds p to slot for owner, checking the claim and writing the
// plan under one lock, so no other owner's claim can land in between. A slot
// another owner holds keeps its plan and the claim comes back.
func (t *planTable) setOwned(slot, owner string, p schemas.FaultPlan) (*SlotClaim, error) {
	if err := ValidatePlan(p); err != nil {
		return nil, err
	}
	if p.Mode == "" {
		p.Mode = ModeNone
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if held := t.heldAgainst(slot, owner); held != nil {
		return held, nil
	}
	return nil, t.commitLocked(slot, &slotState{plan: p, hasPlan: true})
}

// clearOwned clears slot's plan for owner, or returns the claim that stops it.
func (t *planTable) clearOwned(slot, owner string) (*SlotClaim, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if held := t.heldAgainst(slot, owner); held != nil {
		return held, nil
	}
	return nil, t.commitLocked(slot, &slotState{})
}

// planRefused answers a plan write on a slot another owner holds: 409, and
// the plan in place stays.
func (s *Server) planRefused(w http.ResponseWriter, op, slot string, held *SlotClaim) {
	s.logger.Warn("fault plan write refused: key slot claimed by another owner", "op", op, "slot", slot,
		"holder", held.OwnerID, "holder_run_id", held.RunID)
	s.writeJSON(w, http.StatusConflict, claimConflict{
		Error: fmt.Sprintf("key slot %s is claimed by another bench process: %s; the plan was not changed", slot, held),
		Held:  []SlotClaim{*held},
	})
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var req ClaimRequest
	if !s.decodeClaimBody(w, r, &req) {
		return
	}
	if req.Count == 0 {
		req.Count = 1
	}
	ttl, err := claimTTL(req.TTLS)
	if err == nil {
		err = checkOwner(req.Owner)
	}
	if err == nil && (req.Count < 1 || req.Count > len(req.Slots)) {
		err = fmt.Errorf("count must be between 1 and the %d slots offered, got %d", len(req.Slots), req.Count)
	}
	if err == nil && len(req.RunID) > maxLabelLen {
		err = fmt.Errorf("run_id is longer than %d bytes", maxLabelLen)
	}
	if err != nil {
		s.controlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.knownSlots(w, req.Slots) {
		return
	}
	label := clipClaimLabel(req.Label)
	got, exp, held := s.plans.claim(req.Owner, req.Slots, req.Count, ttl, req.RunID, label)
	if got == nil {
		// Debug: a waiting mint asks again once a second, and logs its wait itself.
		s.logger.Debug("key slot claim refused", "owner", OwnerID(req.Owner), "run_id", req.RunID, "count", req.Count, "held", len(held))
		s.writeJSON(w, http.StatusConflict, claimConflict{
			Error: fmt.Sprintf("%d key slots wanted, %d of the %d offered are claimed by another bench process", req.Count, len(held), len(req.Slots)),
			Held:  held,
		})
		return
	}
	s.logger.Info("key slots claimed", "owner", OwnerID(req.Owner), "label", label, "run_id", req.RunID, "slots", strings.Join(got, ","), "ttl_s", int(ttl/time.Second))
	s.writeJSON(w, http.StatusOK, ClaimResult{Claimed: got, ExpiresAt: exp.UTC()})
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var req RenewRequest
	if !s.decodeClaimBody(w, r, &req) {
		return
	}
	ttl, err := claimTTL(req.TTLS)
	if err == nil {
		err = checkOwner(req.Owner)
	}
	names := make([]string, 0, len(req.Slots))
	for _, sl := range req.Slots {
		names = append(names, sl.Slot)
		if err == nil && len(sl.RunID) > maxLabelLen {
			err = fmt.Errorf("run_id is longer than %d bytes", maxLabelLen)
		}
	}
	if err != nil {
		s.controlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.knownSlots(w, names) {
		return
	}
	renewed, lost, exp := s.plans.renew(req.Owner, req.Slots, ttl, clipClaimLabel(req.Label))
	for _, c := range lost {
		s.logger.Warn("key slot claim lost: another owner holds it", "slot", c.Slot, "owner", OwnerID(req.Owner), "holder", c.OwnerID, "holder_run_id", c.RunID)
	}
	s.logger.Debug("key slot claims renewed", "owner", OwnerID(req.Owner), "slots", len(renewed))
	s.writeJSON(w, http.StatusOK, RenewResult{Renewed: renewed, Lost: lost, ExpiresAt: exp.UTC()})
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var req ReleaseRequest
	if !s.decodeClaimBody(w, r, &req) {
		return
	}
	if err := checkOwner(req.Owner); err != nil {
		s.controlError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := s.plans.release(req.Owner, req.Slots, req.RunID)
	if len(out) > 0 {
		s.logger.Info("key slot claims released", "owner", OwnerID(req.Owner), "run_id", req.RunID, "slots", strings.Join(out, ","))
	}
	s.writeJSON(w, http.StatusOK, map[string][]string{"released": out})
}

func (s *Server) handleClaims(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, claimsResponse{Claims: s.plans.listClaims()})
}

// claimsResponse is the body of GET /claims.
type claimsResponse struct {
	Claims []SlotClaim `json:"claims"`
}

// decodeClaimBody decodes a claim API body strictly.
func (s *Server) decodeClaimBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxClaimBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		s.controlError(w, http.StatusBadRequest, "decode claim request: "+err.Error())
		return false
	}
	return true
}

// knownSlots answers 400 for an empty or repeated slot list and 404 for a
// slot the proxy does not know.
func (s *Server) knownSlots(w http.ResponseWriter, slots []string) bool {
	if len(slots) == 0 {
		s.controlError(w, http.StatusBadRequest, "slots is empty")
		return false
	}
	seen := map[string]bool{}
	for _, sl := range slots {
		if seen[sl] {
			s.controlError(w, http.StatusBadRequest, fmt.Sprintf("slot %q is listed twice", sl))
			return false
		}
		seen[sl] = true
		if !s.knownSlot(sl) { // F4: organization slots too (orgslots.go)
			s.controlError(w, http.StatusNotFound, fmt.Sprintf("unknown slot %q", sl))
			return false
		}
	}
	return true
}

func checkOwner(owner string) error {
	if n := len(owner); n < minOwnerLen || n > maxOwnerLen {
		return fmt.Errorf("owner must be %d to %d bytes, got %d", minOwnerLen, maxOwnerLen, n)
	}
	for _, r := range owner {
		if r <= ' ' || r > '~' {
			return errors.New("owner must be printable ASCII without spaces")
		}
	}
	return nil
}

func claimTTL(s int) (time.Duration, error) {
	if s == 0 {
		return DefaultClaimTTL, nil
	}
	ttl := time.Duration(s) * time.Second
	if ttl < MinClaimTTL || ttl > MaxClaimTTL {
		return 0, fmt.Errorf("ttl_s must be between %d and %d, got %d", int(MinClaimTTL/time.Second), int(MaxClaimTTL/time.Second), s)
	}
	return ttl, nil
}

// clipClaimLabel keeps a label printable and short.
func clipClaimLabel(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range strings.TrimSpace(s) {
		if r < 0x20 || r == 0x7f {
			continue
		}
		out = append(out, r)
		if len(out) == maxLabelLen {
			break
		}
	}
	return string(out)
}

// defaultOwnerLabel names this process in claims: host and pid.
func defaultOwnerLabel() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return host + " pid " + strconv.Itoa(os.Getpid())
}

// WithOwner sets the owner token the client claims slots with and sends on
// plan writes. NewClient generates a random one; set it only to make two
// clients one owner.
func WithOwner(token string) ClientOption {
	return func(c *Client) { c.owner = token }
}

// WithOwnerLabel sets the label shown to other owners (host and pid by
// default).
func WithOwnerLabel(label string) ClientOption {
	return func(c *Client) { c.label = label }
}

// OwnerID is the public id of the client's owner token, as GET /claims
// reports it.
func (c *Client) OwnerID() string {
	return OwnerID(c.owner)
}

// ClaimSlots claims req.Count of req.Slots for the client's owner, all or
// none. A refusal comes back as *ClaimConflictError naming the claims in
// the way; a proxy without the claim API as ErrClaimsUnsupported.
func (c *Client) ClaimSlots(ctx context.Context, req ClaimRequest) (*ClaimResult, error) {
	req.Owner = c.owner
	if req.Label == "" {
		req.Label = c.label
	}
	var out ClaimResult
	if err := c.claimCall(ctx, "/claims", req, &out); err != nil {
		return nil, fmt.Errorf("claim key slots %s: %w", strings.Join(req.Slots, ","), err)
	}
	return &out, nil
}

// RenewSlots renews the client's claims on req.Slots.
func (c *Client) RenewSlots(ctx context.Context, req RenewRequest) (*RenewResult, error) {
	req.Owner = c.owner
	if req.Label == "" {
		req.Label = c.label
	}
	var out RenewResult
	if err := c.claimCall(ctx, "/claims/renew", req, &out); err != nil {
		return nil, fmt.Errorf("renew key slot claims: %w", err)
	}
	return &out, nil
}

// ReleaseSlots drops the client's claims on req.Slots.
func (c *Client) ReleaseSlots(ctx context.Context, req ReleaseRequest) error {
	req.Owner = c.owner
	if err := c.claimCall(ctx, "/claims/release", req, nil); err != nil {
		return fmt.Errorf("release key slots %s: %w", strings.Join(req.Slots, ","), err)
	}
	return nil
}

// Claims lists every live claim on the proxy.
func (c *Client) Claims(ctx context.Context) ([]SlotClaim, error) {
	var out claimsResponse
	if err := c.do(ctx, http.MethodGet, "/claims", nil, &out); err != nil {
		if unsupported(err) {
			return nil, fmt.Errorf("list key slot claims: %w (%w)", ErrClaimsUnsupported, err)
		}
		return nil, fmt.Errorf("list key slot claims: %w", err)
	}
	if out.Claims == nil {
		out.Claims = []SlotClaim{}
	}
	return out.Claims, nil
}

// claimCall posts one claim API body and maps a 409 to *ClaimConflictError
// and a 404 or 405 from an older proxy to ErrClaimsUnsupported.
func (c *Client) claimCall(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	err = c.do(ctx, http.MethodPost, path, b, out)
	var se *statusError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se) && se.code == http.StatusConflict:
		var cc claimConflict
		_ = json.Unmarshal(se.body, &cc)
		return &ClaimConflictError{Msg: se.Error(), Held: cc.Held}
	case unsupported(err):
		return fmt.Errorf("%w (%w)", ErrClaimsUnsupported, err)
	}
	return err
}

// unsupported reports whether err is a 405, or a 404 that does not name a
// slot: what a proxy older than an endpoint answers.
func unsupported(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	return se.code == http.StatusMethodNotAllowed || (se.code == http.StatusNotFound && !strings.HasPrefix(se.msg, "unknown slot"))
}

// IsSlotHeld reports whether err is the proxy refusing a plan write or a
// claim because another bench process holds the slot (409).
func IsSlotHeld(err error) bool {
	var cc *ClaimConflictError
	if errors.As(err, &cc) {
		return true
	}
	var se *statusError
	return errors.As(err, &se) && se.code == http.StatusConflict
}
