package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

type feedbackFixture struct {
	*ingestFixture
	h      *FeedbackHandler
	cfg    *traceinbox.Config
	secret string
}

func newFeedbackFixture(t *testing.T, cfg *traceinbox.Config) *feedbackFixture {
	t.Helper()
	if cfg == nil {
		cfg = &traceinbox.Config{}
	}
	f := &feedbackFixture{ingestFixture: &ingestFixture{roots: map[string]string{"org_a": t.TempDir(), "org_b": t.TempDir()}}, cfg: cfg}
	dir := ingestDir{"org_a": {ID: "org_a", Slug: "acme"}, "org_b": {ID: "org_b", Slug: "beta"}}
	tokens := inboxtoken.NewMemory()
	f.h = NewFeedbackHandler(NewInboxTokensHandler(schemas.ModeLive, tokens, "", dir, func(_ context.Context, o tenancy.Org) (*traceinbox.Inbox, *traceinbox.Source, error) {
		root := f.roots[o.ID]
		return traceinbox.New(filepath.Join(root, traceinbox.Dir), f.cfg, filepath.Join(root, "benchmarks")), &traceinbox.Source{Name: "webhook", Kind: traceinbox.KindWebhook}, nil
	}))
	f.r = router.New()
	f.h.RegisterRoutes(f.r)
	_, raw, err := tokens.Create(context.Background(), "org_a", "app", "u1")
	if err != nil {
		t.Fatal(err)
	}
	f.secret = raw
	return f
}

func (f *feedbackFixture) send(t *testing.T, org, bearer string, body any) (*fasthttp.RequestCtx, FeedbackResult) {
	t.Helper()
	b, _ := json.Marshal(body)
	c := f.do("POST", strings.Replace(FeedbackPath, "{org}", org, 1), "", "", bearer, string(b))
	var res FeedbackResult
	_ = json.Unmarshal(c.Response.Body(), &res)
	return c, res
}

func (f *feedbackFixture) item(t *testing.T, org, id string) traceinbox.Item {
	t.Helper()
	l, err := f.inbox(org).List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range l {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, l)
	return traceinbox.Item{}
}

func (f *feedbackFixture) feedbackLog(t *testing.T, org string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(f.roots[org], traceinbox.Dir, FeedbackFile))
	return string(b)
}

func feedbackTrace() map[string]any {
	return map[string]any{"id": "s-123", "model": "gpt-x", "messages": []any{
		map[string]any{"role": "user", "content": "Refund order A-1"},
		map[string]any{"role": "assistant", "content": "Your refund was issued."},
	}}
}

func TestFeedbackDownWithTrace(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	c, res := f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-123", "rating": "down",
		"comment": "it said refunded but it was not", "trace": feedbackTrace(), "source": "support-widget"})
	if c.Response.StatusCode() != 201 || !res.Created || res.ItemID == "" {
		t.Fatalf("down with trace: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	it := f.item(t, "org_a", res.ItemID)
	if !strings.Contains(strings.Join(it.Flagged, "|"), "user feedback: thumbs down: it said refunded but it was not") {
		t.Fatalf("reasons %v", it.Flagged)
	}
	if !strings.Contains(f.feedbackLog(t, "org_a"), `"item_id":"`+res.ItemID+`"`) {
		t.Fatalf("feedback log %q", f.feedbackLog(t, "org_a"))
	}
}

func TestFeedbackDownLooksUpBifrost(t *testing.T) {
	sid := "s-777"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": []any{
				map[string]any{"id": "row-1", "timestamp": "2026-11-27T01:00:00Z", "status": "success", "provider": "openai", "model": "gpt-x", "session_id": "other"},
				map[string]any{"id": "row-2", "timestamp": "2026-11-27T02:00:00Z", "status": "success", "provider": "openai", "model": "gpt-x", "session_id": sid},
			}})
		case "/api/logs/row-2":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "row-2", "status": "success", "provider": "openai", "model": "gpt-x", "session_id": sid,
				"input_history":  []any{map[string]any{"role": "user", "content": "Where is my parcel?"}},
				"output_message": map[string]any{"role": "assistant", "content": "It was delivered."}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := newFeedbackFixture(t, &traceinbox.Config{Sources: []traceinbox.Source{{Name: "gateway", Kind: traceinbox.KindBifrost, URL: srv.URL}}})
	c, res := f.send(t, "org_a", f.secret, map[string]any{"session_id": sid, "rating": "down", "comment": "wrong answer"})
	if c.Response.StatusCode() != 201 || res.ItemID == "" {
		t.Fatalf("lookup: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	it := f.item(t, "org_a", res.ItemID)
	if it.Source != traceinbox.FeedbackSourceName && (len(it.Traces) == 0 || it.Traces[0].Source != traceinbox.FeedbackSourceName) {
		t.Fatalf("item source %+v", it)
	}
}

func TestFeedbackNoTraceNoSource202(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	c, res := f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-1", "rating": "down"})
	if c.Response.StatusCode() != 202 || res.ItemID != "" || res.Created || res.Note != FeedbackNoSession {
		t.Fatalf("no trace: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if f.items(t, "org_a") != 0 || !strings.Contains(f.feedbackLog(t, "org_a"), `"session_id":"s-1"`) {
		t.Fatal("want no item and one feedback line")
	}
}

func TestFeedbackUpIsGolden(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	score := 9.0
	c, res := f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-123", "score": score, "trace": feedbackTrace()})
	if c.Response.StatusCode() >= 300 || res.ItemID == "" {
		t.Fatalf("up: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	it := f.item(t, "org_a", res.ItemID)
	if !strings.Contains(strings.Join(it.Flagged, "|"), "golden candidate") {
		t.Fatalf("not golden: %v", it.Flagged)
	}
	// Up with no trace and no source keeps nothing.
	c, res = f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-9", "rating": "up"})
	if c.Response.StatusCode() != 202 || res.ItemID != "" {
		t.Fatalf("up without trace: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
}

func TestFeedbackTokenAndRateOrder(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	body := map[string]any{"session_id": "s-1", "rating": "down"}
	for name, tc := range map[string]struct{ org, tok string }{
		"wrong org": {"org_b", f.secret}, "unknown org": {"nope", f.secret}, "bad token": {"org_a", f.secret + "x"}, "no token": {"org_a", ""},
	} {
		if c, _ := f.send(t, tc.org, tc.tok, body); c.Response.StatusCode() != 401 {
			t.Fatalf("%s: %d %s", name, c.Response.StatusCode(), c.Response.Body())
		}
	}
	// A bad token with an oversized body is 401, not 413.
	big := map[string]any{"session_id": "s-1", "rating": "down", "comment": strings.Repeat("x", FeedbackMaxBytes)}
	if c, _ := f.send(t, "org_a", "hmi_wrong", big); c.Response.StatusCode() != 401 {
		t.Fatalf("bad token big body: %d", c.Response.StatusCode())
	}
	if c, _ := f.send(t, "org_a", f.secret, big); c.Response.StatusCode() != 413 {
		t.Fatalf("big body: %d", c.Response.StatusCode())
	}
	f.h.RatePerMinute = 2
	f.h.fbGates = map[string]*traceinbox.Gate{}
	for i := 0; i < 2; i++ {
		if c, _ := f.send(t, "org_a", f.secret, body); c.Response.StatusCode() != 202 {
			t.Fatalf("within rate: %d %s", c.Response.StatusCode(), c.Response.Body())
		}
	}
	// Over the rate, an oversized body is still 413 first, then 429.
	if c, _ := f.send(t, "org_a", f.secret, big); c.Response.StatusCode() != 413 {
		t.Fatalf("over rate big body: %d", c.Response.StatusCode())
	}
	if c, _ := f.send(t, "org_a", f.secret, body); c.Response.StatusCode() != 429 {
		t.Fatalf("over rate: %d", c.Response.StatusCode())
	}
	if f.items(t, "org_b") != 0 || f.feedbackLog(t, "org_b") != "" {
		t.Fatal("org_b got feedback")
	}
}

func TestFeedbackHashesUser(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-1", "rating": "down", "user": "opaque-id-42"})
	log := f.feedbackLog(t, "org_a")
	if strings.Contains(log, "opaque-id-42") || !strings.Contains(log, `"user_hash":"`+feedbackUserHash("opaque-id-42")+`"`) || len(feedbackUserHash("x")) != 16 {
		t.Fatalf("feedback log %q", log)
	}
}

func TestFeedbackRedactsComment(t *testing.T) {
	f := newFeedbackFixture(t, nil)
	c, res := f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-123", "rating": "down",
		"comment": "mail me at jane.doe@example.com", "trace": feedbackTrace()})
	if c.Response.StatusCode() != 201 {
		t.Fatalf("%d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if strings.Contains(f.feedbackLog(t, "org_a"), "jane.doe@example.com") {
		t.Fatal("comment kept raw in the feedback log")
	}
	if it := f.item(t, "org_a", res.ItemID); strings.Contains(strings.Join(it.Flagged, "|"), "jane.doe@example.com") {
		t.Fatal("comment kept raw in the item")
	}
	if c, _ := f.send(t, "org_a", f.secret, map[string]any{"session_id": "s-1", "rating": "down", "comment": strings.Repeat("y", FeedbackMaxComment+1)}); c.Response.StatusCode() != 400 {
		t.Fatalf("long comment: %d", c.Response.StatusCode())
	}
	_ = auth.RoleViewer
}
