package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/sharelinks"
)

// Group D security review of the share link change (fix wave 3, N293).

// TestHostedSharedPayloadHasNoOrgOrPeople: the anonymous answer of a run
// trace link and a verdict link (real imported runs) carries no org id, no
// user id, no email, no runner token id and no server path.
func TestHostedSharedPayloadHasNoOrgOrPeople(t *testing.T) {
	f := newHostedFixture(t)
	p, err := f.env.layout.Paths(f.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	leaks := []string{f.acme.ID, "org_", f.users["alice"].ID, "usr_", "@example.com", "rnr_", filepath.Dir(p.ProjectRoot)}
	if len(f.aRuns) == 0 {
		t.Fatal("fixture has no runs")
	}
	for _, share := range []map[string]any{
		{"kind": "run_trace", "target": f.aRuns[0]},
		{"kind": "verdict", "target": f.aExp},
	} {
		r := f.do("POST", "/api/bench/shares", "alice", nil, share)
		if r.status != 201 {
			t.Fatalf("create %v: %d %s", share, r.status, trim(r.body))
		}
		var made struct{ Token string }
		if err := json.Unmarshal([]byte(r.body), &made); err != nil {
			t.Fatal(err)
		}
		r = f.do("GET", "/api/bench/shared/"+made.Token, "", nil, nil)
		if r.status != 200 {
			t.Fatalf("open %v: %d %s", share, r.status, trim(r.body))
		}
		if !strings.Contains(r.body, `"data"`) || len(r.body) < 200 {
			t.Fatalf("shared %v answer is empty: %s", share, trim(r.body))
		}
		for _, leak := range leaks {
			if strings.Contains(r.body, leak) {
				i := strings.Index(r.body, leak)
				lo, hi := max(0, i-120), min(len(r.body), i+120)
				t.Errorf("shared %v keeps %q: ...%s...", share["kind"], leak, r.body[lo:hi])
			}
		}
	}
}

// TestHostedShareErrorsRevealNothing: an unknown opaque token, an old-form
// token naming an org that does not exist, and an old-form token of a real
// org with a wrong signature all answer the same 404 body; a revoked link
// answers 410. No answer names the org.
func TestHostedShareErrorsRevealNothing(t *testing.T) {
	f := newHostedFixture(t)
	r := f.do("POST", "/api/bench/shares", "alice", nil, map[string]any{"kind": "verdict", "target": f.aExp})
	var made struct{ ID, Token string }
	if r.status != 201 || json.Unmarshal([]byte(r.body), &made) != nil {
		t.Fatalf("create: %d %s", r.status, trim(r.body))
	}
	p, _ := f.env.layout.Paths(f.acme.ID)
	old, err := sharelinks.Open(p.ProjectRoot).Token(f.acme.ID, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(old, ".")
	badSig := parts[0] + "." + parts[1] + "." + strings.Repeat("A", 43)
	noOrg := "org_zzzzzzzzzzzzzzzz." + parts[1] + "." + parts[2]
	var first string
	for i, tok := range []string{"s_" + strings.Repeat("C", 43), badSig, noOrg, "s_short", "not-a-token"} {
		r := f.do("GET", "/api/bench/shared/"+tok, "", nil, nil)
		if r.status != 404 {
			t.Fatalf("%q: %d %s", tok, r.status, trim(r.body))
		}
		if strings.Contains(r.body, f.acme.ID) || strings.Contains(r.body, "ACME") {
			t.Errorf("%q answer names the org: %s", tok, r.body)
		}
		if i == 0 {
			first = r.body
		} else if r.body != first {
			t.Errorf("%q answers %s, an unknown opaque token answers %s", tok, r.body, first)
		}
	}
	if r := f.do("POST", "/api/bench/shares/"+made.ID+"/revoke", "alice", nil, nil); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, trim(r.body))
	}
	for _, tok := range []string{made.Token, old} {
		r := f.do("GET", "/api/bench/shared/"+tok, "", nil, nil)
		if r.status != 410 || strings.Contains(r.body, f.acme.ID) || strings.Contains(r.body, "ACME") {
			t.Errorf("revoked %q: %d %s", tok, r.status, r.body)
		}
	}
}
