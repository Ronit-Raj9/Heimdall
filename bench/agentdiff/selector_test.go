package agentdiff

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestResolveOne(t *testing.T) {
	f := flipFixture()
	t1 := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	twin := botArm("aaaaaaaa"+strings.Repeat("f", 56), "v9", "m9", "p\n")
	f.addExp("e2", "s@2", t1, map[string]schemas.ArmManifest{"A": twin})
	arms := collectArms(f)
	cases := []struct {
		sel, hash, exp, err string
	}{
		{sel: "e1:B", hash: hashB, exp: "e1"},
		{sel: " " + hashB + " ", hash: hashB},
		{sel: strings.ToUpper(hashB[:12]), hash: hashB},
		{sel: "aaaaaaaa", err: "matches 2 config hashes"},
		{sel: "aaaaaaaaa", hash: hashA},
		{sel: "dddddddd", err: "no arm resolved"},
		{sel: "e3:A", err: "no experiment"},
		{sel: "g123", err: "neither"},
	}
	for _, c := range cases {
		t.Run(c.sel, func(t *testing.T) {
			r, err := resolveOne(arms, c.sel)
			if c.err != "" {
				if err == nil || !errors.Is(err, ErrBadQuery) || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err %v want %q", err, c.err)
				}
				return
			}
			if err != nil || r.hash != c.hash || r.expID != c.exp {
				t.Fatalf("got %+v %v", r, err)
			}
		})
	}
}

func TestVersions(t *testing.T) {
	f := flipFixture()
	t1 := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	f.addExp("e2", "s@2", t1, map[string]schemas.ArmManifest{"X": botArm(hashB, "v2-again", "m2", "p\n")})
	f.add(runSpec{exp: "e2", arm: "X", task: "t1", outcome: schemas.OutcomePass, sealed: true})
	got := Versions(f).Versions
	if len(got) != 2 || got[0].ConfigHash != hashB || got[1].ConfigHash != hashA {
		t.Fatalf("order %+v", got)
	}
	b := got[0]
	if b.Label != "v2-again" || len(b.Arms) != 2 || b.Arms[0].ExpID != "e2" || b.SealedRuns != 5 || len(b.Suites) != 2 || !b.LatestAt.Equal(t1) {
		t.Fatalf("version b %+v", b)
	}
}

func TestPickSuite(t *testing.T) {
	f := flipFixture()
	f.addExp("e2", "s@2", time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), map[string]schemas.ArmManifest{
		"A": botArm(hashA, "v1", "m1", "p\n"), "B": botArm(hashB, "v2", "m2", "p\n"),
	})
	f.addExp("e3", "other@1", time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), map[string]schemas.ArmManifest{"C": botArm(hashC, "v3", "m3", "p\n")})
	arms := collectArms(f)
	ra, rb, rc := resolved{hash: hashA}, resolved{hash: hashB}, resolved{hash: hashC}
	if s, err := pickSuite(arms, ra, rb, ""); err != nil || s != "s@2" {
		t.Fatalf("newest common suite: %s %v", s, err)
	}
	if s, err := pickSuite(arms, resolved{hash: hashA, expID: "e1", arm: "A"}, rb, ""); err != nil || s != "s@1" {
		t.Fatalf("arm pins its suite: %s %v", s, err)
	}
	if _, err := pickSuite(arms, ra, rc, ""); err == nil || !errors.Is(err, ErrBadQuery) {
		t.Fatalf("no common suite: %v", err)
	}
}
