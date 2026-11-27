package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHostedShareLinkRules (UI fix wave D1, decision 7), through the real
// hosted dispatcher with orgs acme and beta: a link that never expires is
// admin only, the organization-wide list is admin only, a viewer's
// filtered list has no token, path or url, another organization's filtered
// list holds none of acme's links, and the shared comparison names its
// creator by display name, never by email or id.
func TestHostedShareLinkRules(t *testing.T) {
	f := newHostedFixture(t)
	if r := f.do("POST", "/api/bench/shares", "tokA", nil, map[string]any{"kind": "verdict", "target": f.aExp, "expires_in_days": 0}); r.status != 403 || !strings.Contains(r.body, "never expires") {
		t.Fatalf("member never: %d %s", r.status, trim(r.body))
	}
	r := f.do("POST", "/api/bench/shares", "alice", nil, map[string]any{"kind": "verdict", "target": f.aExp})
	var made struct {
		Token     string  `json:"token"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(r.body), &made); err != nil || r.status != 201 || made.Token == "" || made.ExpiresAt == nil {
		t.Fatalf("create: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/shares", "alice", nil, map[string]any{"kind": "verdict", "target": f.aExp, "expires_in_days": 0}); r.status != 201 {
		t.Fatalf("admin never: %d %s", r.status, trim(r.body))
	}

	filtered := "/api/bench/shares?kind=verdict&target=" + f.aExp
	if r := f.do("GET", "/api/bench/shares", "vera", nil, nil); r.status != 403 {
		t.Fatalf("viewer unfiltered: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/shares", "tokA", nil, nil); r.status != 403 {
		t.Fatalf("member unfiltered: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/shares", "alice", nil, nil); r.status != 200 || !strings.Contains(r.body, made.Token) {
		t.Fatalf("admin unfiltered: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", filtered, "tokA", nil, nil); r.status != 200 || !strings.Contains(r.body, made.Token) {
		t.Fatalf("member filtered: %d %s", r.status, trim(r.body))
	}
	r = f.do("GET", filtered, "vera", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, `"state":"active"`) || strings.Contains(r.body, made.Token) || strings.Contains(r.body, `"token"`) || strings.Contains(r.body, `"url"`) || strings.Contains(r.body, `"path"`) {
		t.Fatalf("viewer filtered: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", filtered, "bob", nil, nil); r.status != 200 || strings.Contains(r.body, made.Token) || strings.Contains(r.body, `"id":"shr_`) {
		t.Fatalf("other org filtered: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/shares", "mallory", nil, nil); r.status < 400 {
		t.Fatalf("no org: %d", r.status)
	}

	r = f.do("GET", "/api/bench/shared/"+made.Token, "", nil, nil)
	var item struct {
		Share struct {
			CreatedByName string `json:"created_by_name"`
		} `json:"share"`
	}
	if err := json.Unmarshal([]byte(r.body), &item); err != nil || r.status != 200 {
		t.Fatalf("open: %d %s", r.status, trim(r.body))
	}
	if item.Share.CreatedByName != "alice" {
		t.Fatalf("created_by_name %q", item.Share.CreatedByName)
	}
	if strings.Contains(r.body, "alice@example.com") || strings.Contains(r.body, f.users["alice"].ID) {
		t.Fatalf("shared page names the creator by email or id: %s", trim(r.body))
	}
}
