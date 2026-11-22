package runners

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

var _ engine.RunnerBook = (*Service)(nil)
var _ Live = (*engine.RemoteHub)(nil)

// fakeLive records what the service asks of the coordinators.
type fakeLive struct {
	mu        sync.Mutex
	workers   map[string][]schemas.WorkerInfo
	forgotten []string
}

func (f *fakeLive) Workers(org string) []schemas.WorkerInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.workers[org]
}

func (f *fakeLive) Forget(org, worker, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, org+"/"+worker)
	return true
}

func (f *fakeLive) ForgetToken(org, token, _ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgotten = append(f.forgotten, org+"/token/"+token)
	return 1
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newService(t *testing.T, d *db.DB) (*Service, *clock, *fakeLive) {
	t.Helper()
	ctx := context.Background()
	for _, org := range []string{"org_a", "org_b"} {
		if _, err := d.Exec(ctx, `INSERT INTO orgs (id, slug, name, public_read, created_at) VALUES (?, ?, ?, 0, ?)`,
			org, strings.ReplaceAll(org, "_", "-"), org, db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	c := &clock{t: time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)}
	live := &fakeLive{workers: map[string][]schemas.WorkerInfo{}}
	return New(d, Options{Now: c.now, Live: live, PublicURL: "https://heimdall.example.com/"}), c, live
}

func TestTokenShape(t *testing.T) {
	tok, prefix, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "hmr_"+prefix+"_") || len(prefix) != 8 {
		t.Fatalf("token %q prefix %q", tok, prefix)
	}
	if p, ok := ParseToken(tok); !ok || p != prefix {
		t.Fatalf("parse %q: %q %v", tok, p, ok)
	}
	other, _, _ := NewToken()
	if other == tok {
		t.Fatal("two tokens alike")
	}
	for _, bad := range []string{"", "hm_" + tok[4:], tok[:len(tok)-1], "hmr_ABCDEFGH_" + tok[13:], strings.Replace(tok, "_", "-", 2), tok + "x"} {
		if _, ok := ParseToken(bad); ok {
			t.Fatalf("malformed token %q accepted", bad)
		}
	}
}

// TestTokenScoping: a token resolves to its own organization and pool only;
// revoked and expired tokens are refused; tokens are stored hashed.
func TestTokenScoping(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, c, live := newService(t, d)
		ctx := context.Background()
		gpu, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "gpu", Name: "box-01"})
		if err != nil {
			t.Fatal(err)
		}
		def, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{ExpiresInDays: 1})
		if err != nil {
			t.Fatal(err)
		}
		bTok, err := s.CreateToken(ctx, "org_b", "usr_2", schemas.RunnerTokenCreate{Pool: "gpu"})
		if err != nil {
			t.Fatal(err)
		}
		if gpu.Info.Pool != "gpu" || def.Info.Pool != schemas.DefaultRunnerPool || gpu.Info.State != schemas.RunnerTokenActive {
			t.Fatalf("infos %+v %+v", gpu.Info, def.Info)
		}
		if !strings.Contains(gpu.Command, "runner start -server https://heimdall.example.com -token-file ~/.heimdall-runner-token -pool gpu -name box-01") ||
			!strings.Contains(gpu.Install, "--pool gpu --name box-01") || strings.Contains(gpu.Command, gpu.Token) {
			t.Fatalf("commands %q / %q", gpu.Command, gpu.Install)
		}
		for _, tc := range []struct {
			tok  string
			want schemas.RunnerScope
		}{
			{gpu.Token, schemas.RunnerScope{OrgID: "org_a", Pool: "gpu", TokenID: gpu.Info.ID}},
			{def.Token, schemas.RunnerScope{OrgID: "org_a", Pool: "default", TokenID: def.Info.ID}},
			{bTok.Token, schemas.RunnerScope{OrgID: "org_b", Pool: "gpu", TokenID: bTok.Info.ID}},
		} {
			got, err := s.Verify(ctx, tc.tok)
			if err != nil || got != tc.want {
				t.Fatalf("verify: %+v %v, want %+v", got, err, tc.want)
			}
		}
		p, err := s.VerifyPrincipal(ctx, gpu.Token)
		if err != nil || p.Kind != auth.KindRunner || p.OrgID != "org_a" || p.Pool != "gpu" || p.TokenID != gpu.Info.ID || p.Role != "" {
			t.Fatalf("principal %+v %v", p, err)
		}
		// Stored hashed, never in clear.
		var hash string
		if err := d.QueryRow(ctx, `SELECT hash FROM runner_tokens WHERE id = ?`, gpu.Info.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		if hash != auth.HashSecret(gpu.Token) || strings.Contains(hash, gpu.Info.Prefix+"_") {
			t.Fatalf("stored %q", hash)
		}
		// A forged token of the right shape, and garbage, are refused.
		forged, _, _ := NewToken()
		for _, bad := range []string{forged, "hmr_x", ""} {
			if _, err := s.Verify(ctx, bad); !errors.Is(err, ErrBadToken) {
				t.Fatalf("verify %q: %v", bad, err)
			}
		}
		// Another organization can neither see nor revoke the token.
		if _, err := s.RevokeToken(ctx, "org_b", gpu.Info.ID); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("revoke from another organization: %v", err)
		}
		if list, err := s.ListTokens(ctx, "org_b"); err != nil || len(list) != 1 || list[0].ID != bTok.Info.ID {
			t.Fatalf("org_b tokens %+v %v", list, err)
		}
		// Revoked: refused at once, and its runners are dropped.
		rv, err := s.RevokeToken(ctx, "org_a", gpu.Info.ID)
		if err != nil || rv.State != schemas.RunnerTokenRevoked || rv.RevokedAt == nil {
			t.Fatalf("revoke %+v %v", rv, err)
		}
		if _, err := s.Verify(ctx, gpu.Token); !errors.Is(err, ErrBadToken) {
			t.Fatalf("revoked token: %v", err)
		}
		if len(live.forgotten) != 1 || live.forgotten[0] != "org_a/token/"+gpu.Info.ID {
			t.Fatalf("forgotten %v", live.forgotten)
		}
		// Expired: refused once its day has passed.
		c.add(25 * time.Hour)
		if _, err := s.Verify(ctx, def.Token); !errors.Is(err, ErrBadToken) {
			t.Fatalf("expired token: %v", err)
		}
		list, err := s.ListTokens(ctx, "org_a")
		if err != nil || len(list) != 2 {
			t.Fatalf("org_a tokens %+v %v", list, err)
		}
		states := map[string]string{}
		for _, v := range list {
			states[v.ID] = v.State
		}
		if states[gpu.Info.ID] != schemas.RunnerTokenRevoked || states[def.Info.ID] != schemas.RunnerTokenExpired {
			t.Fatalf("states %v", states)
		}
		if _, err := s.Verify(ctx, bTok.Token); err != nil {
			t.Fatalf("org_b token after org_a changes: %v", err)
		}
		// Bad requests.
		for _, req := range []schemas.RunnerTokenCreate{{Pool: "has space"}, {ExpiresInDays: -1}, {Name: "a\nb"}} {
			if _, err := s.CreateToken(ctx, "org_a", "usr_1", req); !errors.Is(err, schemas.ErrWorkerRequest) {
				t.Fatalf("create %+v: %v", req, err)
			}
		}
		if _, err := s.CreateToken(ctx, "org_missing", "usr_1", schemas.RunnerTokenCreate{}); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("create for a missing organization: %v", err)
		}
	})
}

// TestRunnerRows: registrations and activity keep the runners table up to
// date; a runner silent past the stale threshold is lost; removal drops the
// row and the live registration; organizations never see each other's rows.
func TestRunnerRows(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, c, live := newService(t, d)
		ctx := context.Background()
		tok, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "gpu"})
		if err != nil {
			t.Fatal(err)
		}
		scope := schemas.RunnerScope{OrgID: "org_a", Pool: "gpu", TokenID: tok.Info.ID}
		info := schemas.WorkerInfo{ID: "box-01", Host: "box", Version: "1.2.3", State: schemas.WorkerIdle, Slots: 2,
			Running: []string{}, Pool: "gpu", Labels: []string{"gpu", "region=eu"}, TokenID: tok.Info.ID, LastSeen: c.now()}
		s.RunnerSeen(ctx, scope, info, true)
		s.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_b", Pool: "default", TokenID: "rtk_b"},
			schemas.WorkerInfo{ID: "box-01", State: schemas.WorkerIdle, Slots: 1}, true)
		list, err := s.ListRunners(ctx, "org_a")
		if err != nil || len(list) != 1 {
			t.Fatalf("runners %+v %v", list, err)
		}
		r := list[0]
		if r.Name != "box-01" || r.Pool != "gpu" || r.Version != "1.2.3" || r.Slots != 2 || len(r.Labels) != 2 ||
			r.State != schemas.RunnerIdle || r.TokenPrefix != tok.Info.Prefix || r.Live || !strings.HasPrefix(r.ID, "rnr_") {
			t.Fatalf("runner %+v", r)
		}
		// Activity within TouchEvery with nothing changed writes nothing;
		// a change of running slots writes at once.
		c.add(5 * time.Second)
		s.RunnerSeen(ctx, scope, info, false)
		if got, _ := s.GetRunner(ctx, "org_a", r.ID); !got.LastSeen.Equal(r.LastSeen) {
			t.Fatalf("throttled touch wrote: %v -> %v", r.LastSeen, got.LastSeen)
		}
		busy := info
		busy.State, busy.Running = schemas.WorkerRunning, []string{"s_1"}
		s.RunnerSeen(ctx, scope, busy, false)
		got, err := s.GetRunner(ctx, "org_a", r.ID)
		if err != nil || got.State != schemas.RunnerRunning || got.RunningSlots != 1 || !got.LastSeen.After(r.LastSeen) {
			t.Fatalf("busy runner %+v %v", got, err)
		}
		// Live state wins while the coordinator holds the runner.
		live.workers["org_a"] = []schemas.WorkerInfo{{ID: "box-01", State: schemas.WorkerRunning, Running: []string{"s_1", "s_2"},
			Completed: 7, TokenID: tok.Info.ID, LastSeen: c.now()}}
		got, _ = s.GetRunner(ctx, "org_a", r.ID)
		if !got.Live || got.RunningSlots != 2 || got.Completed != 7 || len(got.Running) != 2 {
			t.Fatalf("live runner %+v", got)
		}
		// Held idle by a coordinator with no experiment attached (no
		// sweep runs) and silent past the stale threshold: lost too.
		live.workers["org_a"] = []schemas.WorkerInfo{{ID: "box-01", State: schemas.WorkerIdle, Running: []string{},
			TokenID: tok.Info.ID, LastSeen: c.now()}}
		c.add(3 * time.Minute)
		if got, _ = s.GetRunner(ctx, "org_a", r.ID); !got.Live || got.State != schemas.RunnerLost {
			t.Fatalf("silent idle live runner %+v", got)
		}
		// Silent past the stale threshold and not live: lost.
		live.workers["org_a"] = nil
		c.add(3 * time.Minute)
		got, _ = s.GetRunner(ctx, "org_a", r.ID)
		if got.State != schemas.RunnerLost || got.RunningSlots != 0 {
			t.Fatalf("stale runner %+v", got)
		}
		// Another organization can not read or remove it.
		if _, err := s.GetRunner(ctx, "org_b", r.ID); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("get from another organization: %v", err)
		}
		if _, err := s.RemoveRunner(ctx, "org_b", r.ID); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("remove from another organization: %v", err)
		}
		if _, err := s.RemoveRunner(ctx, "org_a", r.ID); err != nil {
			t.Fatal(err)
		}
		if list, _ := s.ListRunners(ctx, "org_a"); len(list) != 0 {
			t.Fatalf("runners after removal %+v", list)
		}
		if len(live.forgotten) != 1 || live.forgotten[0] != "org_a/box-01" {
			t.Fatalf("forgotten %v", live.forgotten)
		}
		if list, _ := s.ListRunners(ctx, "org_b"); len(list) != 1 || list[0].OrgID != "org_b" {
			t.Fatalf("org_b runners %+v", list)
		}
		// The removed runner calls again: its row comes back.
		s.RunnerSeen(ctx, scope, info, true)
		if list, _ := s.ListRunners(ctx, "org_a"); len(list) != 1 || list[0].ID == r.ID {
			t.Fatalf("re-registered runner %+v", list)
		}
	})
}

// TestRunnerRowKeepsItsToken is the 4-B-01 regression after a restart: the
// coordinator's memory is empty, so the row is the only record of which
// token registered the name. A runner of another token under the same name
// never takes the row (its token and pool) over.
func TestRunnerRowKeepsItsToken(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, c, live := newService(t, d)
		ctx := context.Background()
		gpu, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "gpu"})
		if err != nil {
			t.Fatal(err)
		}
		def, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "default"})
		if err != nil {
			t.Fatal(err)
		}
		info := schemas.WorkerInfo{ID: "box-01", State: schemas.WorkerIdle, Slots: 1, Running: []string{}}
		s.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "gpu", TokenID: gpu.Info.ID}, info, true)
		// A fresh service: the restart that empties the in-memory runner list.
		s2 := New(d, Options{Now: c.now, Live: live, PublicURL: "https://heimdall.example.com/"})
		s2.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "default", TokenID: def.Info.ID}, info, true)
		list, err := s2.ListRunners(ctx, "org_a")
		if err != nil || len(list) != 1 {
			t.Fatalf("runners %+v %v", list, err)
		}
		if list[0].Pool != "gpu" || list[0].TokenPrefix != gpu.Info.Prefix {
			t.Fatalf("another token took the row over: %+v", list[0])
		}
	})
}

// TestRunnerSeenAfterTokenRotation is the N12 regression: the shared worker
// claims a fresh token on every start and the old one is revoked, under the
// same runner name. The new token takes the row over, so the row's last
// seen time, slots and token follow the running worker.
func TestRunnerSeenAfterTokenRotation(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, c, live := newService(t, d)
		ctx := context.Background()
		old, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "shared"})
		if err != nil {
			t.Fatal(err)
		}
		info := schemas.WorkerInfo{ID: "shared-runner-org_a", State: schemas.WorkerIdle, Slots: 2, Running: []string{}, Pool: "shared"}
		s.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "shared", TokenID: old.Info.ID}, info, true)
		if _, err := s.RevokeToken(ctx, "org_a", old.Info.ID); err != nil {
			t.Fatal(err)
		}
		fresh, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "shared"})
		if err != nil {
			t.Fatal(err)
		}
		c.add(time.Hour)
		// A restart: a fresh service with an empty memory.
		s2 := New(d, Options{Now: c.now, Live: live, PublicURL: "https://heimdall.example.com/"})
		info.Slots = 3
		s2.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "shared", TokenID: fresh.Info.ID}, info, true)
		live.workers["org_a"] = []schemas.WorkerInfo{{ID: info.ID, State: schemas.WorkerIdle, Running: []string{}, TokenID: fresh.Info.ID, LastSeen: c.now()}}
		list, err := s2.ListRunners(ctx, "org_a")
		if err != nil || len(list) != 1 {
			t.Fatalf("runners %+v %v", list, err)
		}
		r := list[0]
		if r.TokenID != fresh.Info.ID || r.Slots != 3 || !r.LastSeen.Equal(c.now()) || !r.Live || r.State == schemas.RunnerLost {
			t.Fatalf("row not taken over by the fresh token: %+v", r)
		}
	})
}

// TestRunnerSeenKeepsNameFromOtherValidToken: a different token that is
// still valid never takes a name over (4-B-01 holds after N12).
func TestRunnerSeenKeepsNameFromOtherValidToken(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, c, _ := newService(t, d)
		ctx := context.Background()
		a, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "gpu"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "default"})
		if err != nil {
			t.Fatal(err)
		}
		info := schemas.WorkerInfo{ID: "box-01", State: schemas.WorkerIdle, Slots: 1, Running: []string{}}
		s.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "gpu", TokenID: a.Info.ID}, info, true)
		c.add(time.Hour)
		info.Slots = 9
		s.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "default", TokenID: b.Info.ID}, info, true)
		list, err := s.ListRunners(ctx, "org_a")
		if err != nil || len(list) != 1 {
			t.Fatalf("runners %+v %v", list, err)
		}
		if list[0].TokenID != a.Info.ID || list[0].Pool != "gpu" || list[0].Slots != 1 {
			t.Fatalf("a valid token's row was taken over: %+v", list[0])
		}
	})
}

// TestRemoveSharedRunnerRefused: an org admin cannot remove the shared
// worker (it registers again at once); a server admin may remove an
// offline shared row through RemoveRunnerRecord.
func TestRemoveSharedRunnerRefused(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		s, _, _ := newService(t, d)
		ctx := context.Background()
		tok, err := s.CreateToken(ctx, "org_a", "usr_1", schemas.RunnerTokenCreate{Pool: "shared"})
		if err != nil {
			t.Fatal(err)
		}
		s.RunnerSeen(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: "shared", TokenID: tok.Info.ID},
			schemas.WorkerInfo{ID: "shared-runner-org_a", State: schemas.WorkerIdle, Slots: 2, Running: []string{}, Pool: "shared"}, true)
		list, _ := s.ListRunners(ctx, "org_a")
		if len(list) != 1 {
			t.Fatalf("runners %+v", list)
		}
		if _, err := s.RemoveRunner(ctx, "org_a", list[0].ID); !errors.Is(err, ErrShared) {
			t.Fatalf("remove shared runner: %v, want ErrShared", err)
		}
		if l, _ := s.ListRunners(ctx, "org_a"); len(l) != 1 {
			t.Fatalf("shared runner removed: %+v", l)
		}
		if _, err := s.RemoveRunnerRecord(ctx, "org_a", list[0].ID); err != nil {
			t.Fatalf("server admin remove: %v", err)
		}
		if l, _ := s.ListRunners(ctx, "org_a"); len(l) != 0 {
			t.Fatalf("record kept: %+v", l)
		}
	})
}
