package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/sharelinks"
)

// TestShareIndexBackfillIdempotent (fix wave 3, N293): the start-up
// backfill indexes every active link of every org, made before the change
// included, and a second run adds nothing.
func TestShareIndexBackfillIdempotent(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	p, err := f.env.layout.Paths(f.acme.ID)
	if err != nil {
		t.Fatal(err)
	}
	st := sharelinks.Open(p.ProjectRoot)
	live, err := st.Create(sharelinks.NewShare{Kind: sharelinks.KindVerdict, Target: f.aExp}, "usr_x")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := st.Create(sharelinks.NewShare{Kind: sharelinks.KindVerdict, Target: f.aExp}, "usr_x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Revoke(gone.ID, "usr_x"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if n, err := backfillShareIndexOnce(ctx, f.env); err != nil || n != 1 {
			t.Fatalf("backfill %d: %d %v", i, n, err)
		}
	}
	var rows int
	if err := f.d.QueryRow(ctx, `SELECT count(*) FROM share_tokens`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("index rows %d %v", rows, err)
	}
	tok, _ := st.TokenV2(live.ID)
	if org, share, err := f.d.ShareToken(ctx, tok); err != nil || org != f.acme.ID || share != live.ID {
		t.Fatalf("indexed %q %q %v", org, share, err)
	}
}

// TestHostedShareLinkV2: on the hosted server a new link is s_ only (no org
// id), opens without login, an old org.id.signature link still opens and
// names its new address, the shared JSON holds no worker path, and a
// revoked s_ link is 410.
func TestHostedShareLinkV2(t *testing.T) {
	f := newHostedFixture(t)
	r := f.do("POST", "/api/bench/shares", "alice", nil, map[string]any{"kind": "verdict", "target": f.aExp})
	if r.status != 201 {
		t.Fatalf("create share: %d %s", r.status, trim(r.body))
	}
	var made struct {
		ID, Token, Path, URL string
	}
	if err := json.Unmarshal([]byte(r.body), &made); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(made.Token, "s_") || strings.Contains(made.Path+made.URL, "org_") || strings.Contains(made.Path+made.URL, f.acme.ID) {
		t.Fatalf("hosted link %+v", made)
	}
	if r := f.do("GET", "/api/bench/shared/"+made.Token, "", nil, nil); r.status != 200 || strings.Contains(r.body, "canonical_token") {
		t.Fatalf("anonymous v2: %d %s", r.status, trim(r.body))
	}
	p, _ := f.env.layout.Paths(f.acme.ID)
	old, err := sharelinks.Open(p.ProjectRoot).Token(f.acme.ID, made.ID)
	if err != nil {
		t.Fatal(err)
	}
	r = f.do("GET", "/api/bench/shared/"+old, "", nil, nil)
	var item struct {
		Canonical string `json:"canonical_token"`
	}
	if r.status != 200 || json.Unmarshal([]byte(r.body), &item) != nil || item.Canonical != made.Token {
		t.Fatalf("old link: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/shares/"+made.ID+"/revoke", "alice", nil, nil); r.status != 200 {
		t.Fatalf("revoke: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/shared/"+made.Token, "", nil, nil); r.status != 410 {
		t.Fatalf("revoked v2: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/shared/s_"+strings.Repeat("B", 43), "", nil, nil); r.status != 404 {
		t.Fatalf("unknown v2: %d %s", r.status, trim(r.body))
	}
}
