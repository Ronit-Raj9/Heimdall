package sharedpool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
)

var digest = "sha256:" + strings.Repeat("a", 64)

func TestCheckImageRef(t *testing.T) {
	for _, ok := range []string{"ghcr.io/acme/agent@" + digest, "python:3.12@" + digest, "localhost:5000/x@" + digest} {
		if _, _, err := CheckImageRef(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "python:3.12", "python@sha256:abc", "Python@" + digest, "a b@" + digest, "@" + digest, "x@md5:" + strings.Repeat("a", 32)} {
		if _, _, err := CheckImageRef(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := CheckImageSize(3<<30, quota.Limits{MaxImageBytes: 2 << 30}); !errors.Is(err, ErrImageTooBig) {
		t.Fatalf("size: %v", err)
	}
}

func TestCheckSecret(t *testing.T) {
	if !errors.Is(CheckSecret("", "x"), ErrDisabled) || !errors.Is(CheckSecret("s", "x"), ErrUnauthorized) || CheckSecret("s", "s") != nil {
		t.Fatal("secret check")
	}
}

func TestImageStore(t *testing.T) {
	s := &ImageStore{DB: dbtest.OpenSQLite(t)}
	ctx := context.Background()
	img := "ghcr.io/acme/agent@" + digest
	r, err := s.Request(ctx, "org1", img, 100, "please", "u1")
	if err != nil || r.Status != StatusPending {
		t.Fatalf("%+v %v", r, err)
	}
	again, _ := s.Request(ctx, "org1", img, 100, "", "u1")
	if again.ID != r.ID {
		t.Fatal("duplicate request filed")
	}
	if _, err := s.Decide(ctx, r.ID, true, "admin", "ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, r.ID, false, "admin", ""); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second decision: %v", err)
	}
	if imgs, _ := s.Approved(ctx, "org1"); len(imgs) != 1 || imgs[0] != img {
		t.Fatalf("approved %v", imgs)
	}
	if other, _ := s.List(ctx, "org2", ""); len(other) != 0 {
		t.Fatal("org isolation")
	}
}

type fakeMinter struct {
	n      int
	tokens []schemas.RunnerTokenView
}

func (f *fakeMinter) CreateToken(_ context.Context, org, _ string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error) {
	f.n++
	v := schemas.RunnerTokenView{ID: "rtk" + string(rune('0'+f.n)), OrgID: org, Pool: req.Pool, Name: req.Name}
	f.tokens = append(f.tokens, v)
	return &schemas.RunnerTokenCreated{Token: "hmr_tok" + string(rune('0'+f.n)), Info: v}, nil
}
func (f *fakeMinter) ListTokens(context.Context, string) ([]schemas.RunnerTokenView, error) {
	return f.tokens, nil
}
func (f *fakeMinter) RevokeToken(_ context.Context, _, id string) (*schemas.RunnerTokenView, error) {
	for i := range f.tokens {
		if f.tokens[i].ID == id {
			now := time.Now()
			f.tokens[i].RevokedAt = &now
		}
	}
	return nil, nil
}

func TestAssignments(t *testing.T) {
	ctx := context.Background()
	d := dbtest.OpenSQLite(t)
	def := quota.DefaultLimits
	def.SharedPool = true
	q := quota.Open(d, quota.Options{Defaults: &def})
	keys, err := keystore.Open(d, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeMinter{}
	off := def
	off.SharedPool = false
	_ = q.Set(ctx, "org3", off, "admin")
	gv := def
	gv.Runtime = "runsc"
	_ = q.Set(ctx, "org2", gv, "admin")
	s := &Service{Quota: q, Keys: keys, Minter: m, Orgs: func(context.Context) ([]string, error) { return []string{"org1", "org2", "org3"}, nil }}
	as, err := s.Assignments(ctx, "runc")
	if err != nil || len(as.Assignments) != 1 || as.Assignments[0].OrgID != "org1" || as.Assignments[0].Pool != Pool {
		t.Fatalf("%+v %v", as, err)
	}
	if as.Assignments[0].TokenID != "" || m.n != 0 {
		t.Fatal("assignments minted a token")
	}
	c1, err := s.ClaimToken(ctx, "org1", "runc")
	if err != nil || c1.Token == "" || c1.OrgID != "org1" {
		t.Fatalf("claim: %+v %v", c1, err)
	}
	as2, _ := s.Assignments(ctx, "runc")
	if as2.Assignments[0].TokenID != c1.TokenID {
		t.Fatalf("assignment token id %q, want %q", as2.Assignments[0].TokenID, c1.TokenID)
	}
	c2, _ := s.ClaimToken(ctx, "org1", "")
	live := 0
	for _, tk := range m.tokens {
		if tk.RevokedAt == nil {
			live++
			if tk.ID != c2.TokenID {
				t.Fatal("old token still live")
			}
		}
	}
	if live != 1 {
		t.Fatalf("%d live tokens, want 1", live)
	}
	if _, err := s.ClaimToken(ctx, "org3", ""); !errors.Is(err, ErrNotAssigned) {
		t.Fatalf("claim for org off the pool: %v", err)
	}
	if _, err := s.ClaimToken(ctx, "org2", "runc"); !errors.Is(err, ErrNotAssigned) {
		t.Fatalf("claim for org of another runtime: %v", err)
	}
	// A readable copy an older server kept is removed on claim.
	if _, err := keys.Create(ctx, "org1", keystore.KindSecret, keystore.Put{Provider: TokenProvider, Label: TokenLabel, Value: "hmr_old"}, "t"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.ClaimToken(ctx, "org1", "")
	if left, _ := keys.List(ctx, "org1", keystore.KindSecret); len(left) != 0 {
		t.Fatalf("legacy token copy kept: %+v", left)
	}
	if all, _ := s.Assignments(ctx, ""); len(all.Assignments) != 2 {
		t.Fatalf("all runtimes: %+v", all)
	}
	// X19a: org3 has no shared pool but its own platform: it is served, on
	// its platform; org1 stays on the shared worker.
	s.Platform = func(_ context.Context, org string) (*PlatformAssignment, error) {
		if org == "org3" {
			return &PlatformAssignment{Name: "e2b", Token: "e2b_tok"}, nil
		}
		return nil, nil
	}
	plat, err := s.Assignments(ctx, "runc")
	if err != nil || len(plat.Assignments) != 2 || plat.Assignments[0].Platform != nil ||
		plat.Assignments[1].OrgID != "org3" || plat.Assignments[1].Platform == nil || plat.Assignments[1].Platform.Name != "e2b" {
		t.Fatalf("platform assignments: %+v %v", plat, err)
	}
	// The list never carries the platform token, only a stamp; the claim
	// hands it over.
	if p := plat.Assignments[1].Platform; p.Token != "" || p.Stamp == "" {
		t.Fatalf("assignment platform carries its token or no stamp: %+v", p)
	}
	claim, err := s.ClaimToken(ctx, "org3", "runc")
	if err != nil || claim.Platform == nil || claim.Platform.Token != "e2b_tok" || claim.Platform.Stamp != plat.Assignments[1].Platform.Stamp {
		t.Fatalf("claim platform: %+v %v", claim, err)
	}
	_ = q.SetKillSwitch(ctx, true, "admin")
	killed, _ := s.Assignments(ctx, "")
	if !killed.KillSwitch || len(killed.Assignments) != 0 {
		t.Fatalf("kill switch: %+v", killed)
	}
	if _, err := s.ClaimToken(ctx, "org1", ""); !errors.Is(err, ErrNotAssigned) {
		t.Fatalf("claim with kill switch on: %v", err)
	}
}

// TestProjectSkip: the transfer keeps agent version snapshots and improver
// configs, and leaves the rest of .heimdall, .git and releases out.
func TestProjectSkip(t *testing.T) {
	for _, c := range []struct {
		rel       string
		dir, skip bool
	}{
		{".heimdall", true, false},
		{".heimdall/agent-versions/codex/v1.yaml", false, false},
		{".heimdall/improve", true, false},
		{".heimdall/improve/imp_1", true, false},
		{".heimdall/improve/imp_1/improver-r01.yaml", false, false},
		{".heimdall/improve/imp_1/notes.json", false, true},
		{".heimdall/improve/imp_1/x/improver-r01.yaml", false, true},
		{".heimdall/webhooks/hooks.json", false, true},
		{"releases", true, true},
		{"agents/codex.yaml", false, false},
		{"sub/.git", true, true},
	} {
		if skip, _ := ProjectSkip(c.rel, c.dir); skip != c.skip {
			t.Errorf("ProjectSkip(%q, dir %v) = %v, want %v", c.rel, c.dir, skip, c.skip)
		}
	}
}
