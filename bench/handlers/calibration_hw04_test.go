package handlers

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// Fix wave 3, N286: on the hosted server the labeller of a calibration label
// is the signed-in member, never a name typed in the body, and members never
// read an email in the label list.

// hostedCalibrationClient serves the calibration routes as the hosted server
// does: a session principal on every request (admin when X-Test-Role is
// "admin") and no write token.
func hostedCalibrationClient(t *testing.T, src CalibrationSource) *testClient {
	t.Helper()
	r := router.New()
	none := func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
	NewCalibrationHandler(schemas.ModeLive, src, none).RegisterRoutes(r)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		p := actorMember
		if string(ctx.Request.Header.Peek("X-Test-Role")) == "admin" {
			p = actorAdmin
		}
		auth.SetPrincipal(ctx, p)
		r.Handler(ctx)
	}}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestCalibrationHostedLabellerIsSignedInMember(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada", "usr_2": "Bo", "bo@example.com": "Bo"})
	src := newFakeCalibration(t)
	c := hostedCalibrationClient(t, src)
	for _, body := range []string{
		`{"task":"q1","answer":"Rex","label":"incorrect","labeller":"mallory@evil.io"}`,
		`{"task":"q1","answer":"Fido","label":"incorrect"}`,
	} {
		resp := c.do(t, "POST", APIPrefix+"/suites/qa/calibration/labels", []byte(body), map[string]string{"Content-Type": "application/json"})
		if resp.StatusCode() != fasthttp.StatusCreated {
			t.Fatalf("post %s: %d %s", body, resp.StatusCode(), resp.Body())
		}
		it := decodeBody(t, resp)
		if it["labeller"] != "usr_2" || it["labeller_name"] != "Bo" {
			t.Fatalf("labeller = %v, name %v; want usr_2, Bo", it["labeller"], it["labeller_name"])
		}
	}
	l, err := src.CalibrationLabels(context.Background(), "qa")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range l.Items[1:] {
		if it.Labeller != "usr_2" {
			t.Fatalf("stored labeller %q, want the member's user id", it.Labeller)
		}
	}
}

func TestCalibrationLabelsHideEmailFromMembers(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	src := newFakeCalibration(t)
	if _, err := src.AddCalibrationLabel(context.Background(), "qa", &schemas.CalibrationLabelRequest{Task: "q1", Answer: "Rex", Label: "incorrect", Labeller: "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	c := hostedCalibrationClient(t, src)
	member := c.do(t, "GET", APIPrefix+"/suites/qa/calibration/labels", nil, nil)
	if member.StatusCode() != 200 || strings.Contains(string(member.Body()), "ada@example.com") || !strings.Contains(string(member.Body()), `"labeller_name":"Ada"`) {
		t.Fatalf("member: %d %s", member.StatusCode(), member.Body())
	}
	var l schemas.CalibrationLabels
	if err := json.Unmarshal(member.Body(), &l); err != nil || len(l.Items) != 2 {
		t.Fatalf("member labels %s: %v", member.Body(), err)
	}
	admin := c.do(t, "GET", APIPrefix+"/suites/qa/calibration/labels", nil, map[string]string{"X-Test-Role": "admin"})
	if !strings.Contains(string(admin.Body()), `"labeller":"ada@example.com"`) {
		t.Fatalf("admin: %s", admin.Body())
	}
}
