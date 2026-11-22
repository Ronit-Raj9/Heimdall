package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// prefixCipher is a reversible test cipher that binds the id.
type prefixCipher struct{}

func (prefixCipher) Seal(id, p string) (string, error) { return "sealed:" + id + ":" + p, nil }
func (prefixCipher) Open(id, s string) (string, error) {
	p, ok := strings.CutPrefix(s, "sealed:"+id+":")
	if !ok {
		return "", errors.New("wrong id")
	}
	return p, nil
}

type capture struct {
	mu     sync.Mutex
	bodies [][]byte
	hdrs   []http.Header
	code   int
}

func (c *capture) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, b)
		c.hdrs = append(c.hdrs, r.Header.Clone())
		code := c.code
		c.mu.Unlock()
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (c *capture) last(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("nothing delivered")
	}
	var m map[string]any
	if err := json.Unmarshal(c.bodies[len(c.bodies)-1], &m); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	return m
}

func regressedEvent() Event {
	ev := NewEvent(EventEvaluationRegressed, "org1", map[string]any{"evaluation_id": "ev_1"})
	ev.Summary = &Summary{Title: "codex v7 regressed: 61% (-6 points)", Text: "Score fell on <support> & billing.", Severity: SeverityError,
		Link: "https://h.example.com/evaluations/ev_1", Fields: []Field{{Label: "Score", Value: "61%"}, {Label: "Change", Value: "-6 points"}}, DedupKey: "heimdall-regressed-codex"}
	return ev
}

func TestSlackBlockKit(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	st := Open(t.TempDir())
	h, err := st.Create(Input{URL: ptr(srv.URL), Format: ptr(FormatSlack)}, "u1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !Accepts(h.Format, EventEvaluationFinished) || len(h.Events) != 3 {
		t.Fatalf("slack defaults: %+v", h.Events)
	}
	ds := Dispatch(context.Background(), st, Sender{AllowPrivate: true}, regressedEvent())
	if len(ds) != 1 || !ds[0].OK {
		t.Fatalf("deliveries %+v", ds)
	}
	m := c.last(t)
	if !strings.HasPrefix(m["text"].(string), "codex v7 regressed") {
		t.Fatalf("slack text: %v", m["text"])
	}
	blocks := m["blocks"].([]any)
	types := []string{}
	for _, b := range blocks {
		types = append(types, b.(map[string]any)["type"].(string))
	}
	if strings.Join(types, ",") != "header,section,section,actions,context" {
		t.Fatalf("block types %v", types)
	}
	sec := blocks[1].(map[string]any)["text"].(map[string]any)["text"].(string)
	if sec != "Score fell on &lt;support&gt; &amp; billing." {
		t.Fatalf("mrkdwn not escaped: %q", sec)
	}
	btn := blocks[3].(map[string]any)["elements"].([]any)[0].(map[string]any)
	if btn["url"] != "https://h.example.com/evaluations/ev_1" {
		t.Fatalf("button %v", btn)
	}
	if c.hdrs[0].Get("X-Heimdall-Signature") == "" {
		t.Fatal("slack delivery not signed")
	}
}

func TestTeamsAdaptiveCard(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	st := Open(t.TempDir())
	if _, err := st.Create(Input{URL: ptr(srv.URL), Format: ptr(FormatTeams), Events: &[]string{EventEvaluationRegressed}}, "u1", true); err != nil {
		t.Fatal(err)
	}
	Dispatch(context.Background(), st, Sender{AllowPrivate: true}, regressedEvent())
	m := c.last(t)
	att := m["attachments"].([]any)[0].(map[string]any)
	if m["type"] != "message" || att["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Fatalf("teams envelope %v", m)
	}
	card := att["content"].(map[string]any)
	body := card["body"].([]any)
	if body[1].(map[string]any)["color"] != "Attention" || body[3].(map[string]any)["type"] != "FactSet" {
		t.Fatalf("card body %v", body)
	}
	if card["actions"].([]any)[0].(map[string]any)["url"] != "https://h.example.com/evaluations/ev_1" {
		t.Fatalf("card actions %v", card["actions"])
	}
}

func TestPagerDutyHook(t *testing.T) {
	c := &capture{code: http.StatusAccepted}
	srv := c.server(t)
	dir := t.TempDir()
	st := OpenWith(dir, prefixCipher{})
	key := "R0123456789abcdef0123456789abcdef"
	if _, err := st.Create(Input{URL: ptr(srv.URL), Format: ptr(FormatPagerDuty)}, "u1", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no routing key: %v", err)
	}
	if _, err := st.Create(Input{Format: ptr(FormatPagerDuty), RoutingKey: ptr(key), Events: &[]string{EventEvaluationFinished}}, "u1", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("pagerduty took evaluation.finished: %v", err)
	}
	h, err := st.Create(Input{URL: ptr(srv.URL), Format: ptr(FormatPagerDuty), RoutingKey: ptr(key)}, "u1", true)
	if err != nil {
		t.Fatal(err)
	}
	if v := h.View(); v.RoutingKeyLast4 != "cdef" || v.Format != FormatPagerDuty {
		t.Fatalf("view %+v", v)
	}
	raw, _ := os.ReadFile(st.hooksPath())
	if strings.Contains(string(raw), `"routing_key": "`+key) {
		t.Fatal("routing key stored in plaintext")
	}
	got, err := st.Get(h.ID)
	if err != nil || got.RoutingKey != key {
		t.Fatalf("reopened key %q %v", got.RoutingKey, err)
	}
	// The default URL is the Events API v2 endpoint.
	def, err := Open(t.TempDir()).Create(Input{Format: ptr(FormatPagerDuty), RoutingKey: ptr(key)}, "u1", false)
	if err != nil || def.URL != PagerDutyEventsURL {
		t.Fatalf("default url %q %v", def.URL, err)
	}

	Dispatch(context.Background(), st, Sender{AllowPrivate: true}, regressedEvent())
	m := c.last(t)
	p := m["payload"].(map[string]any)
	if m["routing_key"] != key || m["event_action"] != "trigger" || m["dedup_key"] != "heimdall-regressed-codex" ||
		p["severity"] != "error" || p["source"] != "heimdall" || !strings.HasPrefix(p["summary"].(string), "codex v7 regressed") {
		t.Fatalf("pagerduty event %v", m)
	}
	// The test button resolves a key no incident has: it pages nobody.
	d := Sender{AllowPrivate: true, Attempts: 1}.Send(context.Background(), got, NewEvent(EventPing, "org1", nil))
	if !d.OK || c.last(t)["event_action"] != "resolve" {
		t.Fatalf("ping %+v %v", d, c.last(t))
	}
	// The format is fixed.
	if _, err := st.Update(h.ID, Input{Format: ptr(FormatSlack)}, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("format change: %v", err)
	}
	if _, err := Open(t.TempDir()).Create(Input{URL: ptr(srv.URL), RoutingKey: ptr(key)}, "u1", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("routing key on a json hook: %v", err)
	}
}

func TestJSONHookCarriesSummary(t *testing.T) {
	c := &capture{}
	srv := c.server(t)
	st := Open(t.TempDir())
	if _, err := st.Create(Input{URL: ptr(srv.URL), Events: &[]string{EventEvaluationRegressed}}, "u1", true); err != nil {
		t.Fatal(err)
	}
	Dispatch(context.Background(), st, Sender{AllowPrivate: true}, regressedEvent())
	m := c.last(t)
	if m["type"] != EventEvaluationRegressed || m["summary"].(map[string]any)["severity"] != "error" || m["data"].(map[string]any)["evaluation_id"] != "ev_1" {
		t.Fatalf("json body %v", m)
	}
}

// INT4: a monitor incident pages on PagerDuty and its resolve closes the same incident.
func TestMonitorIncidentPagerDutyResolve(t *testing.T) {
	if !Accepts(FormatPagerDuty, EventMonitorIncident) {
		t.Fatal("pagerduty hook should take monitor.incident")
	}
	h := Hook{ID: "wh_1", Format: FormatPagerDuty, RoutingKey: "rk"}
	ev := NewEvent(EventMonitorIncident, "org_1", map[string]any{"action": "opened"})
	ev.Summary = &Summary{Title: "Incident opened on monitor m", Severity: SeverityError, DedupKey: "heimdall-monitor-m-i"}
	open := pagerDutyEvent(h, ev)
	if open["event_action"] != "trigger" || open["dedup_key"] != "heimdall-monitor-m-i" {
		t.Fatalf("opened: %v", open)
	}
	ev.Summary.Resolve = true
	done := pagerDutyEvent(h, ev)
	if done["event_action"] != "resolve" || done["dedup_key"] != "heimdall-monitor-m-i" {
		t.Fatalf("resolved: %v", done)
	}
}
