package sharedpool

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestPlatformPoolNameIsValid: the runner pool of one evaluation on a
// sandbox platform is a pool name the server accepts (no "/").
func TestPlatformPoolNameIsValid(t *testing.T) {
	for _, name := range []string{"modal", "e2b", "fly"} {
		p := PlatformPool(name)
		if p != "shared-platform-"+name || !schemas.ValidPoolName(p) {
			t.Fatalf("PlatformPool(%q) = %q, valid %v", name, p, schemas.ValidPoolName(p))
		}
	}
}

// w4510Service is a service over one org that is not on the shared pool
// and has one per-evaluation platform (modal, with a registry login).
func w4510Service(t *testing.T) (*Service, *fakeMinter) {
	t.Helper()
	def := quota.DefaultLimits
	def.SharedPool = false
	def.Runtime = quota.RuntimeRunc
	q := quota.Open(dbtest.OpenSQLite(t), quota.Options{Defaults: &def})
	m := &fakeMinter{}
	s := &Service{Quota: q, Minter: m, Orgs: func(context.Context) ([]string, error) { return []string{"org1"}, nil },
		Platforms: func(context.Context, string) ([]*PlatformAssignment, error) {
			return []*PlatformAssignment{{Name: "modal", Token: "ak:as", Endpoint: "",
				Registry: &RegistryLogin{Registry: "ghcr.io/team/heimdall", Username: "bot", Token: "ghp_secret"}}}, nil
		}}
	return s, m
}

// TestAssignmentsListPerEvaluationPlatforms: an org with a per-evaluation
// platform is served even without the shared pool, and its assignment
// lists the platform stamped and without any token.
func TestAssignmentsListPerEvaluationPlatforms(t *testing.T) {
	s, _ := w4510Service(t)
	as, err := s.Assignments(context.Background(), "")
	if err != nil || len(as.Assignments) != 1 {
		t.Fatalf("%+v %v", as, err)
	}
	a := as.Assignments[0]
	if len(a.Platforms) != 1 || a.Platforms[0].Name != "modal" || a.Platforms[0].Stamp == "" {
		t.Fatalf("platforms %+v", a.Platforms)
	}
	p := a.Platforms[0]
	if p.Token != "" || p.Registry == nil || p.Registry.Token != "" {
		t.Fatalf("token in the assignment list: %+v %+v", p, p.Registry)
	}
}

// TestClaimTokenMintsPlatformTokenPerPool: a claim mints one runner token
// per platform pool, named after the platform, and revokes that pool's
// older token of the same name.
func TestClaimTokenMintsPlatformTokenPerPool(t *testing.T) {
	ctx := context.Background()
	s, m := w4510Service(t)
	c1, err := s.ClaimToken(ctx, "org1", "")
	if err != nil || len(c1.Platforms) != 1 {
		t.Fatalf("claim %+v %v", c1, err)
	}
	pc := c1.Platforms[0]
	if pc.Token == "" || pc.TokenID == "" || pc.Platform == nil || pc.Platform.Token != "ak:as" || pc.Platform.Stamp == "" {
		t.Fatalf("platform claim %+v", pc)
	}
	pool := PlatformPool("modal")
	var found bool
	for _, tk := range m.tokens {
		if tk.ID == pc.TokenID {
			found = tk.Pool == pool && tk.Name == TokenName+"-modal"
		}
	}
	if !found {
		t.Fatalf("platform token not minted for its pool: %+v", m.tokens)
	}
	c2, err := s.ClaimToken(ctx, "org1", "")
	if err != nil || len(c2.Platforms) != 1 {
		t.Fatalf("second claim %+v %v", c2, err)
	}
	live := 0
	for _, tk := range m.tokens {
		if tk.Pool == pool && tk.RevokedAt == nil {
			live++
			if tk.ID != c2.Platforms[0].TokenID {
				t.Fatal("old platform token still live")
			}
		}
		if tk.ID == pc.TokenID && tk.RevokedAt == nil {
			t.Fatal("first platform token not revoked")
		}
	}
	if live != 1 || strings.Contains(c2.Platforms[0].Platform.Stamp, "ak:as") {
		t.Fatalf("%d live platform tokens", live)
	}
}
