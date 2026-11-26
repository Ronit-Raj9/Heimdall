package graders

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/schemas"
)

// memJudgeCache is an in-memory JudgeCache.
type memJudgeCache struct{ m map[string]JudgeCacheEntry }

func (c *memJudgeCache) Get(k string) (JudgeCacheEntry, bool) { e, ok := c.m[k]; return e, ok }
func (c *memJudgeCache) Put(k string, e JudgeCacheEntry)      { c.m[k] = e }

func newMemJudgeCache() *memJudgeCache { return &memJudgeCache{m: map[string]JudgeCacheEntry{}} }

var cacheSpec = schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: PromptQAv1, Votes: 3, MaxTokens: 4}

func cachedJudge(t *testing.T, fj *fakeJudge, gw *fakeGateway, c JudgeCache) *judge {
	t.Helper()
	srv := httptest.NewServer(fj)
	t.Cleanup(srv.Close)
	return newJudge(JudgeConfig{Gateway: gw, URL: srv.URL, Price: testPrice, PriceRef: "snap", Cache: c})
}

func fillCache(c *memJudgeCache, spec schemas.JudgeSpec, prompt string, votes []int, reply, served string) {
	for _, v := range votes {
		c.Put(JudgeCacheKey(spec, judgeSystemFor(spec.PromptVersion), prompt, v), JudgeCacheEntry{Reply: reply, Served: served, At: time.Now()})
	}
}

func TestVoteRepliesAllCachedMintsNoKey(t *testing.T) {
	c := newMemJudgeCache()
	fillCache(c, cacheSpec, "p", []int{0, 1, 2}, "correct", "judge-model")
	fj := &fakeJudge{replies: []string{"incorrect"}, model: "judge-model"}
	gw := &fakeGateway{keyValue: "k"}
	rec, _, err := cachedJudge(t, fj, gw, c).voteReplies(context.Background(), judgedRC(t, qaSpec, strp("Max"), &cacheSpec), cacheSpec, "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.minted) != 0 || len(fj.requests) != 0 || rec.CachedVotes != 3 || len(rec.Votes) != 3 || rec.Votes[0] != VoteCorrect {
		t.Fatalf("minted %d calls %d record %+v", len(gw.minted), len(fj.requests), rec)
	}
}

func TestVoteRepliesPartialCacheCallsMissingVotesOnly(t *testing.T) {
	c := newMemJudgeCache()
	fillCache(c, cacheSpec, "p", []int{1}, "correct", "judge-model")
	fj := &fakeJudge{replies: []string{"incorrect"}, model: "judge-model"}
	gw := &fakeGateway{keyValue: "k"}
	rec, _, err := cachedJudge(t, fj, gw, c).voteReplies(context.Background(), judgedRC(t, qaSpec, strp("Max"), &cacheSpec), cacheSpec, "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gw.minted) != 1 || len(fj.requests) != 2 || rec.CachedVotes != 1 {
		t.Fatalf("minted %d calls %d record %+v", len(gw.minted), len(fj.requests), rec)
	}
	if rec.Votes[0] != VoteIncorrect || rec.Votes[1] != VoteCorrect || rec.Votes[2] != VoteIncorrect {
		t.Fatalf("votes %v", rec.Votes)
	}
	if len(c.m) != 3 {
		t.Fatalf("paid votes not stored: %d entries", len(c.m))
	}
}

func TestVoteRepliesCachedVotesCostZero(t *testing.T) {
	c := newMemJudgeCache()
	fillCache(c, cacheSpec, "p", []int{0, 1}, "correct", "judge-model")
	fj := &fakeJudge{replies: []string{"correct"}, model: "judge-model"}
	rec, _, err := cachedJudge(t, fj, &fakeGateway{keyValue: "k"}, c).voteReplies(context.Background(), judgedRC(t, qaSpec, strp("Max"), &cacheSpec), cacheSpec, "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := rec.USD.Float()
	want := 800*1e-6 + 200*1e-7 + 2*1e-5
	if !ok || v < want-1e-12 || v > want+1e-12 {
		t.Fatalf("usd %+v, want one paid vote %v", rec.USD, want)
	}
}

func TestVoteRepliesRescoresCachedReply(t *testing.T) {
	c := newMemJudgeCache()
	fillCache(c, cacheSpec, "p", []int{0, 1, 2}, "correct", "judge-model")
	sc := func(reply string) string { return VoteIncorrect }
	rec, _, err := cachedJudge(t, &fakeJudge{replies: []string{"correct"}, model: "judge-model"}, &fakeGateway{keyValue: "k"}, c).
		voteReplies(context.Background(), judgedRC(t, qaSpec, strp("Max"), &cacheSpec), cacheSpec, "p", sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rec.Votes {
		if v != VoteIncorrect {
			t.Fatalf("cached reply not scored by the current scorer: %v", rec.Votes)
		}
	}
}

func TestVoteRepliesIgnoresCachedOtherModel(t *testing.T) {
	c := newMemJudgeCache()
	fillCache(c, cacheSpec, "p", []int{0, 1, 2}, "correct", "judge-model-mini")
	fj := &fakeJudge{replies: []string{"incorrect"}, model: "judge-model"}
	gw := &fakeGateway{keyValue: "k"}
	rec, _, err := cachedJudge(t, fj, gw, c).voteReplies(context.Background(), judgedRC(t, qaSpec, strp("Max"), &cacheSpec), cacheSpec, "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fj.requests) != 3 || rec.CachedVotes != 0 || rec.Votes[0] != VoteIncorrect {
		t.Fatalf("calls %d record %+v", len(fj.requests), rec)
	}
}

func TestCalibrationNeverUsesCache(t *testing.T) {
	c := newMemJudgeCache()
	fj := &contentJudge{served: "judge-model-2026-01-01"}
	srv := httptest.NewServer(fj)
	defer srv.Close()
	s := judgedSuite(t, nil)
	plan, err := PlanCalibration(s, []calibration.Labelled{labelled("q1", "Your dog is Max.", schemas.CalibrationCorrect)}, testPrice)
	if err != nil {
		t.Fatal(err)
	}
	cfg := JudgeConfig{Gateway: &fakeGateway{keyValue: "k"}, URL: srv.URL, Price: testPrice, PriceRef: "snap", Cache: c}
	if _, _, err := RunCalibration(context.Background(), cfg, plan, nil); err != nil {
		t.Fatal(err)
	}
	first := fj.calls
	if _, _, err := RunCalibration(context.Background(), cfg, plan, nil); err != nil {
		t.Fatal(err)
	}
	if first == 0 || fj.calls != 2*first || len(c.m) != 0 {
		t.Fatalf("calls %d then %d, cache entries %d", first, fj.calls, len(c.m))
	}
}
