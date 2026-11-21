package faultproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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

// OrgSlotsRequest is the body of PUT /orgslots.
type OrgSlotsRequest struct {
	Slots []OrgSlot `json:"slots"`
}

const (
	maxOrgSlotsBody = 256 << 10
	maxOrgSlots     = 4096
)

type orgSlotEntry struct {
	ref  slotRef
	auth string
	key  string
	base string
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
	return o.byName[name].ref, true
}

func (o *orgSlots) entry(name string) (orgSlotEntry, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	e, ok := o.byName[name]
	if !ok {
		return orgSlotEntry{}, false
	}
	return *e, true
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
		out.base = e.base
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
	if sl.Auth == "" || strings.TrimSpace(sl.Key) == "" {
		return sl, fmt.Errorf("slot %q needs an auth placeholder and a key", sl.Name)
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
	if len(s.org.byName)+len(clean) > maxOrgSlots {
		s.org.mu.Unlock()
		s.controlError(w, http.StatusRequestEntityTooLarge, "too many organization slots")
		return
	}
	for _, c := range clean {
		if old, ok := s.org.byName[c.Name]; ok {
			delete(s.org.byAuth, old.auth)
		}
		s.org.byName[c.Name] = &orgSlotEntry{ref: slotRef{name: c.Name, provider: c.Provider}, auth: c.Auth, key: c.Key, base: c.BaseURL}
		s.org.byAuth[c.Auth] = c.Name
	}
	s.org.mu.Unlock()
	s.logger.Info("organization key slots registered", "slots", len(clean))
	w.WriteHeader(http.StatusNoContent)
}

// RegisterOrgSlots registers organization key slots at the proxy
// (PUT /orgslots). The keys travel only in the request body.
func (c *Client) RegisterOrgSlots(ctx context.Context, slots []OrgSlot) error {
	body, err := json.Marshal(OrgSlotsRequest{Slots: slots})
	if err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodPut, "/orgslots", body, nil); err != nil {
		return fmt.Errorf("register organization slots: %w", err)
	}
	return nil
}
