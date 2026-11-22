package faultproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// F4 key delivery (lane L05): organization key slots. A hosted runner
// registers, per organization and provider, placeholder slots whose real key
// is the organization's own (PUT /orgslots). Bifrost holds only the
// placeholders; the proxy swaps in the organization's key upstream, so the
// organization's calls are in the ledger and take fault plans like any slot.
// The keys live only in memory: a restarted proxy loses them and the runner
// registers them again with its next lease.

// OrgSlot is one organization key slot. Key is never logged or returned.
type OrgSlot struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Auth     string `json:"auth"`               // the placeholder Bifrost sends
	Key      string `json:"key"`                // the organization's provider key
	BaseURL  string `json:"base_url,omitempty"` // https endpoint of an openai-compatible provider
}

// OrgSlotsRequest is the body of PUT /orgslots. Replace lists name
// prefixes ("org_<org>_<provider>_") the request replaces whole: a slot
// already registered under one of them and missing from Slots is dropped,
// so a key set that shrank or a rotated key leaves nothing behind (G3 B7).
type OrgSlotsRequest struct {
	Slots   []OrgSlot `json:"slots"`
	Replace []string  `json:"replace,omitempty"`
}

// OrgSlotsRelease is the body of DELETE /orgslots: every slot whose name
// starts with one of Prefixes ("org_<org>_") is dropped with its key.
type OrgSlotsRelease struct {
	Prefixes []string `json:"prefixes"`
}

const (
	maxOrgSlotsBody = 256 << 10
	maxOrgSlots     = 4096
	// orgSlotTTL is how long a slot stays registered with no registration
	// and no call through it. Runners register per lease and every model
	// call refreshes it, so only the slots of an organization no runner
	// serves any more expire (G3 B7). A slot with a live claim never does.
	orgSlotTTL = 6 * time.Hour
)

type orgSlotEntry struct {
	ref  slotRef
	auth string
	key  string
	base string
	seen atomic.Int64 // unix seconds of the last registration or call
}

// orgSlots holds the registered organization slots; the zero value is ready.
type orgSlots struct {
	mu     sync.RWMutex
	byAuth map[string]string        // placeholder -> slot name
	byName map[string]*orgSlotEntry // slot name -> entry
}

func (o *orgSlots) lookupAuth(auth string) (slotRef, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	name, ok := o.byAuth[auth]
	if !ok {
		return slotRef{}, false
	}
	e := o.byName[name]
	e.seen.Store(time.Now().Unix()) // a call keeps a slot in use registered
	return e.ref, true
}

// dropLocked removes one slot. Called with o.mu held for writing.
func (o *orgSlots) dropLocked(name string) {
	if e, ok := o.byName[name]; ok {
		delete(o.byAuth, e.auth)
		delete(o.byName, name)
	}
}

// validOrgPrefix accepts a release or replace prefix: an organization slot
// name prefix of at least one organization id character ("org_x").
func validOrgPrefix(p string) bool {
	return strings.HasPrefix(p, "org_") && len(p) > len("org_") && len(p) <= 128
}

// expireOrgSlots drops slots unseen for orgSlotTTL that no live claim
// holds. Called with s.org.mu held for writing.
func (s *Server) expireOrgSlots(now time.Time) int {
	cut := now.Add(-orgSlotTTL).Unix()
	n := 0
	for name, e := range s.org.byName {
		if e.seen.Load() > cut || s.slotClaimed(name) {
			continue
		}
		s.org.dropLocked(name)
		n++
	}
	return n
}

// slotClaimed reports whether a live claim holds slot.
func (s *Server) slotClaimed(slot string) bool {
	if s.plans == nil {
		return false
	}
	s.plans.mu.Lock()
	defer s.plans.mu.Unlock()
	return s.plans.liveClaim(slot) != nil
}

// entry returns a registered slot. Entries are replaced, never changed in
// place (only seen moves), so the caller may read its fields unlocked.
func (o *orgSlots) entry(name string) (*orgSlotEntry, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	e, ok := o.byName[name]
	return e, ok
}

// knownSlot reports whether name is a configured or registered slot.
func (s *Server) knownSlot(name string) bool {
	if s.names[name] {
		return true
	}
	_, ok := s.org.entry(name)
	return ok
}

// orgUpstream is how a request on an organization slot is answered: the
// provider's real API (or the slot's own base URL) with the organization's
// key. A proxy forced to its scripted model (Upstream "scripted") stays
// scripted: no paid traffic.
func (s *Server) orgUpstream(slot string, p ProviderSpec, up upstream) upstream {
	e, ok := s.org.entry(slot)
	if !ok {
		return up
	}
	if s.cfg.Upstream == UpstreamScripted && p.Name == ProviderAnthropic {
		return up
	}
	out := upstream{mode: modeReal, base: up.base, key: e.key, sarvamKey: up.sarvamKey}
	if out.base == "" {
		out.base = p.BaseURL
	}
	if e.base != "" {
		out.base, out.trimV1, out.orgBase = e.base, true, true
	}
	return out
}

// validOrgSlot checks one registration against the provider table and the
// configured slots.
func (s *Server) validOrgSlot(sl OrgSlot) (OrgSlot, error) {
	if !strings.HasPrefix(sl.Name, "org_") || len(sl.Name) > 128 {
		return sl, fmt.Errorf("slot %q: organization slot names start with org_", sl.Name)
	}
	if s.names[sl.Name] {
		return sl, fmt.Errorf("slot %q is a configured slot", sl.Name)
	}
	if _, ok := s.providers.byName[sl.Provider]; !ok {
		return sl, fmt.Errorf("slot %q names unknown provider %q", sl.Name, sl.Provider)
	}
	spec := s.providers.byName[sl.Provider]
	if sl.Auth == "" || (strings.TrimSpace(sl.Key) == "" && !spec.Keyless) {
		return sl, fmt.Errorf("slot %q needs an auth placeholder and a key", sl.Name)
	}
	if spec.PerSlotBase && spec.BaseURL == "" && sl.BaseURL == "" {
		return sl, fmt.Errorf("slot %q: provider %s needs a base URL", sl.Name, sl.Provider)
	}
	if _, clash := s.slots[sl.Auth]; clash {
		return sl, fmt.Errorf("slot %q reuses a configured slot's auth value", sl.Name)
	}
	if sl.BaseURL != "" {
		base, err := parseBaseURL(sl.BaseURL)
		if err != nil {
			return sl, fmt.Errorf("slot %q: %w", sl.Name, err)
		}
		if !strings.HasPrefix(base, "https://") {
			return sl, fmt.Errorf("slot %q: base url must be https", sl.Name)
		}
		// Keys are saved with the OpenAI-style base (".../v1", or a base
		// like ".../api/paas/v4"; what the key test calls) while agent
		// requests carry /v1/...: the base replaces that /v1 (trimV1), so
		// the upstream is never .../v1/v1/chat/completions.
		sl.BaseURL = base
	}
	sl.Key = strings.TrimSpace(sl.Key)
	return sl, nil
}

// handleOrgSlots handles PUT /orgslots: register or replace organization
// slots, all or none.
func (s *Server) handleOrgSlots(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var req OrgSlotsRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxOrgSlotsBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.controlError(w, http.StatusBadRequest, "parse organization slots: "+err.Error())
		return
	}
	if len(req.Slots) == 0 {
		s.controlError(w, http.StatusBadRequest, "slots is empty")
		return
	}
	clean := make([]OrgSlot, 0, len(req.Slots))
	seen := map[string]bool{}
	for _, sl := range req.Slots {
		c, err := s.validOrgSlot(sl)
		if err != nil {
			s.controlError(w, http.StatusBadRequest, err.Error())
			return
		}
		if seen[c.Name] || seen["auth:"+c.Auth] {
			s.controlError(w, http.StatusBadRequest, fmt.Sprintf("slot %q is listed twice", c.Name))
			return
		}
		seen[c.Name], seen["auth:"+c.Auth] = true, true
		clean = append(clean, c)
	}
	for _, p := range req.Replace {
		if !validOrgPrefix(p) {
			s.controlError(w, http.StatusBadRequest, fmt.Sprintf("replace prefix %q: want org_<org>_...", p))
			return
		}
	}
	now := time.Now()
	s.org.mu.Lock()
	if s.org.byName == nil {
		s.org.byName, s.org.byAuth = map[string]*orgSlotEntry{}, map[string]string{}
	}
	for _, c := range clean {
		if other, ok := s.org.byAuth[c.Auth]; ok && other != c.Name {
			s.org.mu.Unlock()
			s.controlError(w, http.StatusConflict, fmt.Sprintf("slot %q reuses the auth value of %q", c.Name, other))
			return
		}
	}
	expired := s.expireOrgSlots(now)
	// The cap counts unique slots: re-registering names already held adds
	// nothing, and the names a replace prefix drops are freed first.
	dropped := map[string]bool{}
	for name := range s.org.byName {
		if !seen[name] && hasAnyPrefix(name, req.Replace) && !s.slotClaimed(name) { // a claimed slot serves a run: it expires later
			dropped[name] = true
		}
	}
	added := 0
	for _, c := range clean {
		if _, ok := s.org.byName[c.Name]; !ok {
			added++
		}
	}
	if len(s.org.byName)-len(dropped)+added > maxOrgSlots {
		s.org.mu.Unlock()
		s.controlError(w, http.StatusRequestEntityTooLarge, "too many organization slots")
		return
	}
	for name := range dropped {
		s.org.dropLocked(name)
	}
	for _, c := range clean {
		if old, ok := s.org.byName[c.Name]; ok {
			delete(s.org.byAuth, old.auth)
		}
		e := &orgSlotEntry{ref: slotRef{name: c.Name, provider: c.Provider}, auth: c.Auth, key: c.Key, base: c.BaseURL}
		e.seen.Store(now.Unix())
		s.org.byName[c.Name] = e
		s.org.byAuth[c.Auth] = c.Name
	}
	total := len(s.org.byName)
	s.org.mu.Unlock()
	s.logger.Info("organization key slots registered", "slots", len(clean), "replaced", len(dropped), "expired", expired, "total", total)
	w.WriteHeader(http.StatusNoContent)
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// handleOrgSlotsRelease handles DELETE /orgslots: drop every slot under the
// given prefixes, with its key (G3 B7). A runner calls it when it stops
// serving an organization; slots already gone are not an error. A slot a
// live claim holds stays until the claim ends and the slot expires
// (orgSlotTTL), so a run in flight never loses its key.
func (s *Server) handleOrgSlotsRelease(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	var req OrgSlotsRelease
	dec := json.NewDecoder(io.LimitReader(r.Body, maxOrgSlotsBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.controlError(w, http.StatusBadRequest, "parse organization slot release: "+err.Error())
		return
	}
	if len(req.Prefixes) == 0 {
		s.controlError(w, http.StatusBadRequest, "prefixes is empty")
		return
	}
	for _, p := range req.Prefixes {
		if !validOrgPrefix(p) {
			s.controlError(w, http.StatusBadRequest, fmt.Sprintf("prefix %q: want org_<org>_...", p))
			return
		}
	}
	s.org.mu.Lock()
	n, kept := 0, 0
	for name := range s.org.byName {
		if !hasAnyPrefix(name, req.Prefixes) {
			continue
		}
		if s.slotClaimed(name) { // a run still holds it; it expires once the claim does
			kept++
			continue
		}
		s.org.dropLocked(name)
		n++
	}
	s.org.mu.Unlock()
	s.logger.Info("organization key slots released", "slots", n, "kept_claimed", kept)
	w.WriteHeader(http.StatusNoContent)
}

// RegisterOrgSlots registers organization key slots at the proxy
// (PUT /orgslots). The keys travel only in the request body.
func (c *Client) RegisterOrgSlots(ctx context.Context, slots []OrgSlot) error {
	return c.ReplaceOrgSlots(ctx, slots, nil)
}

// ReplaceOrgSlots registers slots and drops every other slot under the
// replace prefixes (PUT /orgslots with replace).
func (c *Client) ReplaceOrgSlots(ctx context.Context, slots []OrgSlot, replace []string) error {
	body, err := json.Marshal(OrgSlotsRequest{Slots: slots, Replace: replace})
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodPut, "/orgslots", body, nil); err != nil {
		return fmt.Errorf("register organization slots: %w", err)
	}
	return nil
}

// ReleaseOrgSlots drops every organization slot under prefixes at the proxy
// (DELETE /orgslots).
func (c *Client) ReleaseOrgSlots(ctx context.Context, prefixes []string) error {
	body, err := json.Marshal(OrgSlotsRelease{Prefixes: prefixes})
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodDelete, "/orgslots", body, nil); err != nil {
		return fmt.Errorf("release organization slots: %w", err)
	}
	return nil
}
