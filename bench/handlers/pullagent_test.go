package handlers

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/pullagent"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func pullFixture(t *testing.T) (func(*router.Router), *pullagent.Signer) {
	t.Helper()
	signer, err := pullagent.NewSigner([]byte("test-pull-signing-key-0123"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewPullAgentHandler(schemas.ModeLive, pullagent.NewBroker(), signer, true, "https://heimdall.example.com/", nil)
	return func(r *router.Router) { h.RegisterRoutes(r) }, signer
}

func runToken(t *testing.T, s *pullagent.Signer, org, agent, run string) string {
	t.Helper()
	tok, err := s.RunToken(pullagent.Claims{Org: org, Agent: agent, Run: run, Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestPullRoutesRoundTrip(t *testing.T) {
	reg, signer := pullFixture(t)
	member := &auth.Principal{Kind: auth.KindToken, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleMember}
	tok := runToken(t, signer, "org_a", "support-bot", "run_1")
	runAuth := []string{"Authorization", "Bearer " + tok, "Content-Type", "application/json"}

	// Nobody serves the agent yet: the run is refused with how to fix it.
	resp := call(t, reg, nil, "POST", PullRunTurnsPath, `{"payload":{"kind":"task"}}`, runAuth...)
	if resp.StatusCode() != 409 || !strings.Contains(string(resp.Body()), "heimdall.serve") {
		t.Fatalf("submit with nobody serving: %d %s", resp.StatusCode(), resp.Body())
	}
	// The run side needs a valid run token; an account token is not one.
	if resp := call(t, reg, nil, "POST", PullRunTurnsPath, `{"payload":{}}`, "Authorization", "Bearer hm_x"); resp.StatusCode() != 401 {
		t.Fatalf("account token on the run side: %d", resp.StatusCode())
	}
	other, _ := pullagent.NewSigner([]byte("another-signing-key-98765"))
	if resp := call(t, reg, nil, "POST", PullRunTurnsPath, `{"payload":{}}`, "Authorization", "Bearer "+runToken(t, other, "org_a", "support-bot", "run_1")); resp.StatusCode() != 401 ||
		!strings.Contains(string(resp.Body()), "HEIMDALL_PULL_SIGNING_KEY") {
		t.Fatalf("token under another key: %d %s", resp.StatusCode(), resp.Body())
	}
	// The agent side needs an org principal.
	if resp := call(t, reg, nil, "POST", PullNextPath, `{"agent":"support-bot","wait_s":0}`); resp.StatusCode() != 401 {
		t.Fatalf("anonymous next: %d", resp.StatusCode())
	}
	if resp := call(t, reg, member, "POST", PullNextPath, `{"agent":"bad name!","wait_s":0}`); resp.StatusCode() != 400 {
		t.Fatalf("bad agent name: %d", resp.StatusCode())
	}
	// An empty poll registers the process and answers 204.
	if resp := call(t, reg, member, "POST", PullNextPath, `{"agent":"support-bot","client":"laptop-1","wait_s":0}`); resp.StatusCode() != 204 {
		t.Fatalf("empty next: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, nil, "POST", PullRunTurnsPath, `{"timeout_s":60,"payload":{"kind":"task","instruction":"hi"}}`, runAuth...)
	if resp.StatusCode() != 201 {
		t.Fatalf("submit: %d %s", resp.StatusCode(), resp.Body())
	}
	var created PullRunTurnCreated
	json.Unmarshal(resp.Body(), &created)
	// Pending: the run sees 202.
	if resp := call(t, reg, nil, "GET", strings.Replace(PullRunTurnPath, "{id}", created.ID, 1)+"?wait=0", "", runAuth...); resp.StatusCode() != 202 {
		t.Fatalf("pending wait: %d %s", resp.StatusCode(), resp.Body())
	}
	// Another org's process sees nothing.
	otherOrg := &auth.Principal{Kind: auth.KindToken, UserID: "usr_2", OrgID: "org_b", Role: auth.RoleMember}
	if resp := call(t, reg, otherOrg, "POST", PullNextPath, `{"agent":"support-bot","wait_s":0}`); resp.StatusCode() != 204 {
		t.Fatalf("another org took a turn: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, member, "POST", PullNextPath, `{"agent":"support-bot","client":"laptop-1","wait_s":0}`)
	var turn PullTurnView
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &turn) != nil || turn.ID != created.ID || turn.RunID != "run_1" ||
		!strings.Contains(string(turn.Payload), `"instruction":"hi"`) {
		t.Fatalf("next: %d %s", resp.StatusCode(), resp.Body())
	}
	answerPath := strings.Replace(PullAnswerPath, "{id}", turn.ID, 1)
	if resp := call(t, reg, otherOrg, "POST", answerPath, `{"answer":{"text":"x"}}`); resp.StatusCode() != 404 {
		t.Fatalf("answer from another org: %d", resp.StatusCode())
	}
	if resp := call(t, reg, member, "POST", answerPath, `{"answer":"plain"}`); resp.StatusCode() != 400 {
		t.Fatalf("non-object answer: %d", resp.StatusCode())
	}
	if resp := call(t, reg, member, "POST", answerPath, `{}`); resp.StatusCode() != 400 {
		t.Fatalf("empty answer: %d", resp.StatusCode())
	}
	resp = call(t, reg, member, "GET", PullAgentsPath, "")
	var agents PullAgentsResponse
	if json.Unmarshal(resp.Body(), &agents) != nil || len(agents.Agents) != 1 || !agents.Agents[0].Connected || agents.Agents[0].InFlight != 1 {
		t.Fatalf("agents: %s", resp.Body())
	}
	if resp := call(t, reg, member, "POST", answerPath, `{"answer":{"text":"hello there"}}`); resp.StatusCode() != 204 {
		t.Fatalf("answer: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, member, "POST", answerPath, `{"answer":{"text":"twice"}}`); resp.StatusCode() != 409 {
		t.Fatalf("second answer: %d", resp.StatusCode())
	}
	// A token of another run of the same org cannot read the answer.
	if resp := call(t, reg, nil, "GET", strings.Replace(PullRunTurnPath, "{id}", created.ID, 1)+"?wait=0", "",
		"Authorization", "Bearer "+runToken(t, signer, "org_a", "support-bot", "run_2")); resp.StatusCode() != 404 {
		t.Fatalf("another run read the answer: %d", resp.StatusCode())
	}
	resp = call(t, reg, nil, "GET", strings.Replace(PullRunTurnPath, "{id}", created.ID, 1)+"?wait=0", "", runAuth...)
	var res PullRunTurnResultDoc
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &res) != nil || res.Status != "answered" || res.Answer["text"] != "hello there" {
		t.Fatalf("answered wait: %d %s", resp.StatusCode(), resp.Body())
	}
}

func TestPullRepoLinks(t *testing.T) {
	reg, signer := pullFixture(t)
	tok := runToken(t, signer, "org_a", "coder", "run_1")
	if resp := call(t, reg, nil, "POST", PullRunReposPath, "not gzip", "Authorization", "Bearer "+tok); resp.StatusCode() != 415 {
		t.Fatalf("non-gzip repo: %d", resp.StatusCode())
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("tar bytes"))
	zw.Close()
	resp := call(t, reg, nil, "POST", PullRunReposPath, buf.String(), "Authorization", "Bearer "+tok, "Content-Type", "application/gzip")
	var created PullRepoCreated
	if resp.StatusCode() != 201 || json.Unmarshal(resp.Body(), &created) != nil || created.Bytes != buf.Len() {
		t.Fatalf("store repo: %d %s", resp.StatusCode(), resp.Body())
	}
	if !strings.HasPrefix(created.URL, "https://heimdall.example.com"+APIPrefix+"/pull/repos/") {
		t.Fatalf("link not on the public origin: %s", created.URL)
	}
	u, err := url.Parse(created.URL)
	if err != nil || u.Query().Get("sig") == "" || u.Query().Get("exp") == "" {
		t.Fatalf("link: %s %v", created.URL, err)
	}
	resp = call(t, reg, nil, "GET", u.RequestURI(), "")
	if resp.StatusCode() != 200 || !bytes.Equal(resp.Body(), buf.Bytes()) {
		t.Fatalf("download: %d", resp.StatusCode())
	}
	q := u.Query()
	q.Set("sig", strings.Repeat("0", len(q.Get("sig"))))
	if resp := call(t, reg, nil, "GET", u.Path+"?"+q.Encode(), ""); resp.StatusCode() != 403 {
		t.Fatalf("forged signature: %d", resp.StatusCode())
	}
	if resp := call(t, reg, nil, "GET", u.Path, ""); resp.StatusCode() != 403 {
		t.Fatalf("unsigned download: %d", resp.StatusCode())
	}
}

// The local server has one org, and its agent side is guarded by the write token.
func TestPullRoutesLocalMode(t *testing.T) {
	signer, _ := pullagent.NewSigner([]byte("test-pull-signing-key-0123"))
	guarded := false
	write := func(next fasthttp.RequestHandler) fasthttp.RequestHandler { guarded = true; return next }
	h := NewPullAgentHandler(schemas.ModeLive, pullagent.NewBroker(), signer, false, "", write)
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	if resp := call(t, reg, nil, "POST", PullNextPath, `{"agent":"bot","wait_s":0}`); resp.StatusCode() != 204 {
		t.Fatalf("local next: %d %s", resp.StatusCode(), resp.Body())
	}
	if !guarded {
		t.Fatal("agent-side writes are not behind the write token")
	}
	tok := runToken(t, signer, "", "bot", "run_1")
	if resp := call(t, reg, nil, "POST", PullRunTurnsPath, `{"payload":{"kind":"task"}}`, "Authorization", "Bearer "+tok); resp.StatusCode() != 201 {
		t.Fatalf("local submit: %d %s", resp.StatusCode(), resp.Body())
	}
}
