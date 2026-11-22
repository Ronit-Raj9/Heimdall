package pullagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a settable time source for the broker.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestBroker() (*Broker, *clock) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	b := NewBroker()
	b.now = c.now
	return b, c
}

func claims(org, agent, run string, c *clock) Claims {
	return Claims{Org: org, Agent: agent, Run: run, Exp: c.now().Add(time.Hour).Unix()}
}

func TestSubmitRefusesWithoutAServingProcess(t *testing.T) {
	b, c := newTestBroker()
	if _, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), 0); !errors.Is(err, ErrNoPuller) {
		t.Fatalf("submit with nobody serving: %v, want ErrNoPuller", err)
	}
	if !strings.Contains(NoPullerReason("bot"), `heimdall.serve(agent="bot"`) {
		t.Fatalf("reason does not say how to fix it: %s", NoPullerReason("bot"))
	}
	// A poll counts as presence for PresenceTTL, then lapses.
	if tu, err := b.Next(context.Background(), "o1", "bot", "p1", 0); err != nil || tu != nil {
		t.Fatalf("empty next: %v %v", tu, err)
	}
	if _, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), 0); err != nil {
		t.Fatalf("submit after a poll: %v", err)
	}
	// Another org's process never serves this org's agent.
	if _, err := b.Submit(claims("o2", "bot", "r2", c), json.RawMessage(`{}`), 0); !errors.Is(err, ErrNoPuller) {
		t.Fatalf("submit for another org: %v, want ErrNoPuller", err)
	}
	c.add(PresenceTTL + time.Second)
	if _, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), 0); !errors.Is(err, ErrNoPuller) {
		t.Fatalf("submit after presence lapsed: %v, want ErrNoPuller", err)
	}
}

func TestTurnRoundTrip(t *testing.T) {
	b, c := newTestBroker()
	ctx := context.Background()
	got := make(chan *Turn, 1)
	go func() {
		tu, err := b.Next(ctx, "o1", "bot", "p1", 5*time.Second)
		if err != nil {
			t.Errorf("next: %v", err)
		}
		got <- tu
	}()
	// Wait until the poll is parked, so the agent counts as connected.
	for i := 0; ; i++ {
		if ok, _ := b.Connected("o1", "bot"); ok {
			break
		}
		if i > 200 {
			t.Fatal("poll never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	tu, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{"kind":"task"}`), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	leased := <-got
	if leased == nil || leased.ID != tu.ID || string(leased.Payload) != `{"kind":"task"}` {
		t.Fatalf("leased %+v, want turn %s", leased, tu.ID)
	}
	if st := b.Agents("o1"); len(st) != 1 || st[0].InFlight != 1 || !st[0].Connected || st[0].Processes != 1 {
		t.Fatalf("agents: %+v", st)
	}
	if len(b.Agents("o2")) != 0 {
		t.Fatal("another org sees this org's agents")
	}
	// Only the org whose process holds the turn may answer it.
	if err := b.Answer("o2", tu.ID, json.RawMessage(`{"text":"x"}`)); !errors.Is(err, ErrNoTurn) {
		t.Fatalf("answer from another org: %v", err)
	}
	if err := b.Answer("o1", tu.ID, json.RawMessage(`{"text":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	if err := b.Answer("o1", tu.ID, json.RawMessage(`{"text":"again"}`)); !errors.Is(err, ErrNotLeased) {
		t.Fatalf("second answer: %v", err)
	}
	if _, err := b.Wait(ctx, "o1", "other-run", tu.ID, 0); !errors.Is(err, ErrNoTurn) {
		t.Fatalf("wait from another run: %v", err)
	}
	res, err := b.Wait(ctx, "o1", "r1", tu.ID, time.Second)
	if err != nil || res.State != StateAnswered || string(res.Answer) != `{"text":"hello"}` {
		t.Fatalf("wait: %+v %v", res, err)
	}
}

func TestTurnDeadlines(t *testing.T) {
	b, c := newTestBroker()
	ctx := context.Background()
	b.Next(ctx, "o1", "bot", "p1", 0)
	pending, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c.add(2 * time.Minute)
	res, _ := b.Wait(ctx, "o1", "r1", pending.ID, 0)
	if res.State != StateFailed || !strings.Contains(res.Error, "no process took the turn") {
		t.Fatalf("expired pending turn: %+v", res)
	}
	// A leased turn that is never answered fails too, naming the process.
	b.Next(ctx, "o1", "bot", "p1", 0)
	leased, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if tu, _ := b.Next(ctx, "o1", "bot", "p1", 0); tu == nil || tu.ID != leased.ID {
		t.Fatalf("next did not lease %s", leased.ID)
	}
	c.add(2 * time.Minute)
	res, _ = b.Wait(ctx, "o1", "r1", leased.ID, 0)
	if res.State != StateFailed || !strings.Contains(res.Error, "did not answer") {
		t.Fatalf("expired leased turn: %+v", res)
	}
	// The deadline never outlives the run token.
	b.Next(ctx, "o1", "bot", "p1", 0)
	cl := claims("o1", "bot", "r1", c)
	cl.Exp = c.now().Add(10 * time.Second).Unix()
	short, err := b.Submit(cl, json.RawMessage(`{}`), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if short.Deadline.After(time.Unix(cl.Exp, 0)) {
		t.Fatalf("deadline %v outlives the token %v", short.Deadline, time.Unix(cl.Exp, 0))
	}
	// A process error fails the turn with its message.
	b.Next(ctx, "o1", "bot", "p1", 0)
	bad, _ := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), time.Minute)
	for {
		tu, _ := b.Next(ctx, "o1", "bot", "p1", 0)
		if tu == nil {
			t.Fatal("bad turn never leased")
		}
		if tu.ID == bad.ID {
			break
		}
	}
	if err := b.Fail("o1", bad.ID, "ValueError: boom"); err != nil {
		t.Fatal(err)
	}
	res, _ = b.Wait(ctx, "o1", "r1", bad.ID, 0)
	if res.State != StateFailed || !strings.Contains(res.Error, "ValueError: boom") {
		t.Fatalf("failed turn: %+v", res)
	}
}

func TestLimits(t *testing.T) {
	b, c := newTestBroker()
	b.Next(context.Background(), "o1", "bot", "p1", 0)
	big := json.RawMessage(`"` + strings.Repeat("x", MaxTurnBytes) + `"`)
	if _, err := b.Submit(claims("o1", "bot", "r1", c), big, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized turn: %v", err)
	}
	for i := 0; i < MaxQueued; i++ {
		if _, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), 0); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	if _, err := b.Submit(claims("o1", "bot", "r1", c), json.RawMessage(`{}`), 0); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("queue over cap: %v", err)
	}
}

func TestRepos(t *testing.T) {
	b, c := newTestBroker()
	cl := claims("o1", "bot", "r1", c)
	info, err := b.PutRepo(cl, []byte("tarball"), c.now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	data, sha, err := b.Repo(info.ID)
	if err != nil || string(data) != "tarball" || sha != info.SHA256 || len(sha) != 64 {
		t.Fatalf("repo: %q %s %v", data, sha, err)
	}
	if _, err := b.PutRepo(cl, make([]byte, MaxRepoBytes+1), c.now().Add(time.Minute)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized repo: %v", err)
	}
	// The org quota counts what is held and frees on expiry.
	for i := 0; i < MaxOrgRepoBytes/MaxRepoBytes; i++ {
		if _, err := b.PutRepo(cl, make([]byte, MaxRepoBytes), c.now().Add(time.Minute)); err != nil {
			if !errors.Is(err, ErrRepoQuota) {
				t.Fatal(err)
			}
			break
		}
	}
	if _, err := b.PutRepo(cl, make([]byte, MaxRepoBytes), c.now().Add(time.Minute)); !errors.Is(err, ErrRepoQuota) {
		t.Fatalf("over the org quota: %v", err)
	}
	if _, err := b.PutRepo(claims("o2", "bot", "r2", c), []byte("other org"), c.now().Add(time.Minute)); err != nil {
		t.Fatalf("another org's quota is its own: %v", err)
	}
	c.add(2 * time.Minute)
	if _, _, err := b.Repo(info.ID); !errors.Is(err, ErrRepoAbsent) {
		t.Fatalf("expired repo: %v", err)
	}
	if _, err := b.PutRepo(cl, make([]byte, MaxRepoBytes), c.now().Add(time.Minute)); err != nil {
		t.Fatalf("quota after expiry: %v", err)
	}
}

func TestSigner(t *testing.T) {
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
	s, _ := NewSigner([]byte("0123456789abcdef0123"))
	other, _ := NewSigner([]byte("fedcba9876543210fedc"))
	now := time.Unix(1_800_000_000, 0)
	tok, err := s.RunToken(Claims{Org: "o1", Agent: "bot", Run: "r1", Exp: now.Add(time.Minute).Unix()})
	if err != nil || !strings.HasPrefix(tok, "hpr1.") {
		t.Fatalf("token %q %v", tok, err)
	}
	c, err := s.VerifyRun(tok, now)
	if err != nil || c.Org != "o1" || c.Agent != "bot" || c.Run != "r1" {
		t.Fatalf("verify: %+v %v", c, err)
	}
	if _, err := other.VerifyRun(tok, now); !errors.Is(err, ErrBadToken) {
		t.Fatalf("token under another key: %v", err)
	}
	if _, err := s.VerifyRun(tok, now.Add(2*time.Minute)); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired token: %v", err)
	}
	// Changing the claims breaks the signature.
	parts := strings.SplitN(strings.TrimPrefix(tok, "hpr1."), ".", 2)
	forged, _ := s.RunToken(Claims{Org: "o2", Agent: "bot", Run: "r1", Exp: now.Add(time.Minute).Unix()})
	fparts := strings.SplitN(strings.TrimPrefix(forged, "hpr1."), ".", 2)
	if _, err := s.VerifyRun("hpr1."+fparts[0]+"."+parts[1], now); !errors.Is(err, ErrBadToken) {
		t.Fatalf("swapped claims: %v", err)
	}
	for _, bad := range []string{"", "hpr1.", "hpr1.x", "sk-abc", tok + "x"} {
		if _, err := s.VerifyRun(bad, now); err == nil {
			t.Fatalf("%q verified", bad)
		}
	}
	exp := now.Add(time.Minute).Unix()
	sig := s.RepoSig("repo_1", exp)
	if err := s.VerifyRepo("repo_1", itoa64(exp), sig, now); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyRepo("repo_2", itoa64(exp), sig, now); !errors.Is(err, ErrBadRepoURL) {
		t.Fatalf("sig for another repo: %v", err)
	}
	if err := s.VerifyRepo("repo_1", itoa64(exp+60), sig, now); !errors.Is(err, ErrBadRepoURL) {
		t.Fatalf("stretched expiry: %v", err)
	}
	if err := s.VerifyRepo("repo_1", itoa64(exp), sig, now.Add(2*time.Minute)); !errors.Is(err, ErrRepoExpired) {
		t.Fatalf("expired link: %v", err)
	}
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
