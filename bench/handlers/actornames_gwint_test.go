package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Raw actors shown with a display name (the G18 follow-up, fix wave 2): a
// member reads the name, never an email; an admin keeps the email.

func callAsPrincipal(h fasthttp.RequestHandler, p *auth.Principal, method, path, body string) (int, []byte) {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	if body != "" {
		ctx.Request.SetBodyString(body)
	}
	auth.SetPrincipal(ctx, p)
	h(ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

func TestSecretsListNamesCreator(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	ks, err := keystore.Open(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h := secretsRouter(t, ks)
	if code, body := callAsPrincipal(h, actorAdmin, "POST", APIPrefix+"/secrets", `{"name":"TAVILY_API_KEY","value":"tvly-1234567890"}`); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	for _, tc := range []struct {
		p        *auth.Principal
		wantKeep string
	}{{actorMember, ""}, {actorAdmin, "ada@example.com"}} {
		code, body := callAsPrincipal(h, tc.p, "GET", APIPrefix+"/secrets", "")
		var l SecretList
		if code != 200 || json.Unmarshal(body, &l) != nil || len(l.Secrets) != 1 {
			t.Fatalf("%s list: %d %s", tc.p.Role, code, body)
		}
		if s := l.Secrets[0]; s.CreatedByName != "Ada" || s.CreatedBy != tc.wantKeep {
			t.Fatalf("%s: secret %+v", tc.p.Role, s)
		}
	}
}

func TestDataPolicyViewNamesEditor(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	h := &dataPolicyHandler{}
	ctx := &fasthttp.RequestCtx{}
	auth.SetPrincipal(ctx, actorMember)
	v := h.view(ctx, "faq", datapolicy.Policy{UpdatedBy: "ada@example.com"})
	if v.UpdatedByName != "Ada" || v.Policy.UpdatedBy != "" {
		t.Fatalf("member view: %+v", v)
	}
	ctx = &fasthttp.RequestCtx{}
	auth.SetPrincipal(ctx, actorAdmin)
	if v := h.view(ctx, "faq", datapolicy.Policy{UpdatedBy: "ada@example.com"}); v.UpdatedByName != "Ada" || v.Policy.UpdatedBy != "ada@example.com" {
		t.Fatalf("admin view: %+v", v)
	}
}

func TestTaskReviewDecisionNamesReviewer(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	member := auth.WithPrincipal(context.Background(), actorMember)
	d := namedDecision(newActorNames(member), schemas.TaskReviewDecision{Decision: "accepted", By: "ada@example.com"})
	if d.ByName != "Ada" || d.By != "" || d.Decision != "accepted" {
		t.Fatalf("decision %+v", d)
	}
}

func TestImprovementApprovalsNameApprover(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	member := auth.WithPrincipal(context.Background(), actorMember)
	stored := []improve.RoundView{{N: 1}, {N: 2, Approvals: []improve.Approval{{Stage: "audit", By: "ada@example.com", Reason: "known flake"}}}}
	got := namedApprovals(member, stored)
	if a := got[1].Approvals[0]; a.ByName != "Ada" || a.By != "" || a.Reason != "known flake" {
		t.Fatalf("approval %+v", a)
	}
	if stored[1].Approvals[0].By != "ada@example.com" || stored[1].Approvals[0].ByName != "" {
		t.Fatalf("the stored round changed: %+v", stored[1].Approvals[0])
	}
}

func TestRunRecordingsNameWhoTookControl(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", "ada@example.com": "Ada"})
	d := fullDeps()
	idx := recordingsFixture(t)
	idx.files["r_t01_A"][schemas.HumanActionsFile] = []byte(`{"seq":1,"service":"browser","action":"click","x":5,"y":6,"by":"ada@example.com"}` + "\n")
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/recordings", nil, nil)
	var got RunRecordings
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &got) != nil || len(got.Actions) != 1 {
		t.Fatalf("recordings: %d %s", resp.StatusCode(), resp.Body())
	}
	// No signed-in admin here: the email is left out and the name is shown.
	if a := got.Actions[0]; a.ByName == "" || a.By != "" || strings.Contains(string(resp.Body()), "@example.com") {
		t.Fatalf("action %+v", a)
	}
}
