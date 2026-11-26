package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/apptasks"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/maximhq/bifrost/bench/tracebulk"
	"github.com/valyala/fasthttp"
)

// Fix wave 3, N33: From API rules, Maxim tasks and policy documents name
// their people; a member never gets an email, an org admin does.

const adaEmail = "ada@example.com"

// asCaller is a request context of the given principal.
func asCaller(p *auth.Principal) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	auth.SetPrincipal(ctx, p)
	return ctx
}

// answerOf is v as JSON text.
func answerOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func checkNamed(t *testing.T, what string, build func(*fasthttp.RequestCtx) any, nameKey string) {
	t.Helper()
	member := answerOf(t, build(asCaller(actorMember)))
	if strings.Contains(member, adaEmail) || !strings.Contains(member, `"`+nameKey+`":"Ada"`) {
		t.Errorf("%s as a member: %s", what, member)
	}
	admin := answerOf(t, build(asCaller(actorAdmin)))
	if !strings.Contains(admin, adaEmail) || !strings.Contains(admin, `"`+nameKey+`":"Ada"`) {
		t.Errorf("%s as an admin: %s", what, admin)
	}
	local := answerOf(t, build(&fasthttp.RequestCtx{}))
	if !strings.Contains(local, adaEmail) {
		t.Errorf("%s in local mode keeps the actor: %s", what, local)
	}
}

func TestAppTasksDraftNamesApprover(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", adaEmail: "Ada"})
	d := &apptasks.Draft{ID: "ag_1", Suite: "shop", Status: apptasks.StatusRulesApproved, ApprovedBy: adaEmail, CreatedBy: adaEmail,
		Written: []apptasks.Written{{ID: "t1", ReviewedBy: adaEmail}}}
	h := &appTasksArea{suiteAuthoring: &suiteAuthoring{}}
	checkNamed(t, "app tasks draft", func(ctx *fasthttp.RequestCtx) any {
		return h.view(ctx, apptasks.Store{Dir: t.TempDir()}, d)
	}, "approved_by_name")
	if d.ApprovedBy != adaEmail || d.Written[0].ReviewedBy != adaEmail {
		t.Fatalf("the stored draft changed: %+v", d)
	}
}

func TestMaximTasksNameReviewer(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", adaEmail: "Ada"})
	b := &tracebulk.Batch{ID: "mb_1", CreatedBy: adaEmail, Tasks: []tracebulk.TaskDraft{{Suite: "s", ID: "t1", ReviewedBy: adaEmail}}}
	checkNamed(t, "maxim import", func(ctx *fasthttp.RequestCtx) any { return maximBatchView(ctx, b) }, "reviewed_by_name")
	checkNamed(t, "maxim task", func(ctx *fasthttp.RequestCtx) any {
		return MaximTaskResult{Task: maximTaskView(callerNames(ctx), b.Tasks[0])}
	}, "reviewed_by_name")
	if strings.Count(answerOf(t, maximBatchView(asCaller(actorMember), b)), `"tasks":`) != 1 {
		t.Fatal("the import answer has one tasks list")
	}
}

func TestPolicyDocNamesUploader(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", adaEmail: "Ada"})
	d := &policybench.Doc{ID: "refunds", UploadedBy: adaEmail, Drafts: []policybench.Draft{{Task: "t1", DecidedBy: adaEmail}}}
	checkNamed(t, "policy document", func(ctx *fasthttp.RequestCtx) any {
		return (&policyDocs{}).view(ctx, "shop", t.TempDir(), d)
	}, "uploaded_by_name")
	if d.UploadedBy != adaEmail || d.Drafts[0].DecidedBy != adaEmail {
		t.Fatalf("the stored document changed: %+v", d)
	}
}
