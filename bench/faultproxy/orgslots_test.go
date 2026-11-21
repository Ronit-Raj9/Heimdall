package faultproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestOrgSlotsSwapInTheOrganizationKey checks F4 key delivery at the proxy:
// a registered organization slot's placeholder reaches the upstream as the
// organization's key, the shared slot keeps the shared key, the ledger names
// the organization slot, and plans accept it.
func TestOrgSlotsSwapInTheOrganizationKey(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("x-api-key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer up.Close()
	env := newTestEnv(t, up.URL, "sk-shared")
	ctx := context.Background()
	slot := OrgSlot{Name: "org_acme_anthropic_1", Provider: ProviderAnthropic, Auth: "org_acme_anthropic_1", Key: "sk-org-acme"}
	if err := env.client.RegisterOrgSlots(ctx, []OrgSlot{slot}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if resp, body := env.call(t, http.MethodPost, "/v1/messages", slot.Auth, msgBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("org slot call: %d %s", resp.StatusCode, body)
	}
	if resp, _ := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody); resp.StatusCode != http.StatusOK {
		t.Fatalf("shared slot call: %d", resp.StatusCode)
	}
	mu.Lock()
	got := append([]string{}, seen...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "sk-org-acme" || got[1] != "sk-shared" {
		t.Fatalf("upstream keys %v", got)
	}
	entries := env.entries(t)
	if len(entries) != 2 || entries[0].Slot != slot.Name {
		t.Fatalf("ledger %+v", entries)
	}
	if !env.srv.knownSlot(slot.Name) || env.srv.knownSlot("org_other_anthropic_1") {
		t.Fatal("knownSlot")
	}
}

// TestOrgSlotsRejectsBadRegistrations checks the control API refuses
// configured slot names, unknown providers, empty keys, non-https base URLs
// and a missing token.
func TestOrgSlotsRejectsBadRegistrations(t *testing.T) {
	env := newTestEnv(t, "", "")
	ctx := context.Background()
	for name, sl := range map[string]OrgSlot{
		"configured name":  {Name: "slot-a", Provider: ProviderAnthropic, Auth: "x", Key: "k"},
		"no org prefix":    {Name: "acme_1", Provider: ProviderAnthropic, Auth: "x", Key: "k"},
		"unknown provider": {Name: "org_a_x_1", Provider: "nope", Auth: "x", Key: "k"},
		"empty key":        {Name: "org_a_anthropic_1", Provider: ProviderAnthropic, Auth: "x"},
		"shared auth":      {Name: "org_a_anthropic_1", Provider: ProviderAnthropic, Auth: "auth-a", Key: "k"},
		"http base":        {Name: "org_a_openai_1", Provider: ProviderOpenAI, Auth: "x", Key: "k", BaseURL: "http://10.0.0.1"},
	} {
		if err := env.client.RegisterOrgSlots(ctx, []OrgSlot{sl}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	anon := NewClient(env.ctrl.URL)
	if err := anon.RegisterOrgSlots(ctx, []OrgSlot{{Name: "org_a_anthropic_1", Provider: ProviderAnthropic, Auth: "x", Key: "k"}}); err == nil {
		t.Error("registered without the control token")
	}
}
