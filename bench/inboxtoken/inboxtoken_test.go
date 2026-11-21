package inboxtoken_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/inboxtoken"
)

func stores(t *testing.T) map[string]inboxtoken.Store {
	t.Helper()
	return map[string]inboxtoken.Store{"db": inboxtoken.Open(dbtest.Open(t)), "memory": inboxtoken.NewMemory()}
}

func TestTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			tok, raw, err := s.Create(ctx, "org_a", " ci ", "u1")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(raw, inboxtoken.Prefix) || tok.Last4 != raw[len(raw)-4:] || tok.Label != "ci" {
				t.Fatalf("token %+v raw %q", tok, raw)
			}
			l, _ := s.List(ctx, "org_a")
			if len(l) != 1 || l[0].LastUsedAt != nil {
				t.Fatalf("list %+v", l)
			}
			if other, _ := s.List(ctx, "org_b"); len(other) != 0 {
				t.Fatalf("org_b sees %+v", other)
			}
			if _, err := s.Verify(ctx, "org_b", raw, time.Now()); !errors.Is(err, inboxtoken.ErrNotFound) {
				t.Fatalf("other org verify: %v", err)
			}
			if _, err := s.Verify(ctx, "org_a", raw+"x", time.Now()); !errors.Is(err, inboxtoken.ErrNotFound) {
				t.Fatalf("wrong token verify: %v", err)
			}
			got, err := s.Verify(ctx, "org_a", raw, time.Now())
			if err != nil || got.ID != tok.ID || got.LastUsedAt == nil {
				t.Fatalf("verify %+v %v", got, err)
			}
			if l, _ = s.List(ctx, "org_a"); l[0].LastUsedAt == nil {
				t.Fatal("last used not recorded")
			}
			if err := s.Revoke(ctx, "org_b", tok.ID); !errors.Is(err, inboxtoken.ErrNotFound) {
				t.Fatalf("revoke from other org: %v", err)
			}
			if err := s.Revoke(ctx, "org_a", tok.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Verify(ctx, "org_a", raw, time.Now()); !errors.Is(err, inboxtoken.ErrNotFound) {
				t.Fatalf("revoked verify: %v", err)
			}
			if _, _, err := s.Create(ctx, "org_a", "bad\nlabel", "u1"); !errors.Is(err, inboxtoken.ErrInvalid) {
				t.Fatalf("label: %v", err)
			}
		})
	}
}

func TestTokenLimit(t *testing.T) {
	s := inboxtoken.NewMemory()
	for i := 0; i < inboxtoken.MaxPerOrg; i++ {
		if _, _, err := s.Create(context.Background(), "o", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Create(context.Background(), "o", "", ""); !errors.Is(err, inboxtoken.ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
}
