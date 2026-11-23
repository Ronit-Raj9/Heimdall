package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// The retention setting names who changed it; a member never gets the
// email, an org admin does, and the name is never stored (fix wave 3, L3).
func TestRetentionNamesEditor(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", adaEmail: "Ada"})
	root := t.TempDir()
	now := time.Now().UTC()
	if err := WriteRetention(root, RetentionInfo{RunDays: 30, UpdatedBy: adaEmail, UpdatedAt: &now, UpdatedByName: "stale"}); err != nil {
		t.Fatal(err)
	}
	stored, err := ReadRetention(root)
	if err != nil || stored.UpdatedByName != "" || stored.UpdatedBy != adaEmail {
		t.Fatalf("stored retention: %+v %v", stored, err)
	}
	h := &dataOpsHandler{env: OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode}}
	checkNamed(t, "retention", func(ctx *fasthttp.RequestCtx) any {
		h.getRetention(ctx)
		if ctx.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("get retention: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
		return json.RawMessage(append([]byte(nil), ctx.Response.Body()...))
	}, "updated_by_name")
	member := asCaller(actorMember)
	h.getRetention(member)
	if strings.Contains(string(member.Response.Body()), "@") {
		t.Fatalf("member answer carries an email: %s", member.Response.Body())
	}
}
