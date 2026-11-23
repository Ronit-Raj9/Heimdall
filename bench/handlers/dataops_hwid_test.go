package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/dataset"
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

// One dataset version read as a member names its actor without the email;
// an org admin also gets the email (group D review: H18 records a version
// with the editor's email on every authoring path).
func TestDatasetVersionDetailNamesActor(t *testing.T) {
	installMembers(t, map[string]string{"usr_1": "Ada", adaEmail: "Ada"})
	c := newDatasetSyncClient(t)
	newQASuite(t, c, "zz-test-qa")
	req := RowImportRequest{Format: "jsonl", Data: "{\"question\":\"What is 2+2?\",\"answer\":\"4\"}\n"}
	if code, body := c.do(t, "POST", "/suites/zz-test-qa/rows", jsonBody(t, req), nil); code != 201 {
		t.Fatalf("import: %d %s", code, body)
	}
	root := c.root
	vs, err := dataset.Open(root).Versions("zz-test-qa")
	if err != nil || len(vs) == 0 {
		t.Fatalf("versions: %v %v", vs, err)
	}
	// The version as a hosted import records it: the editor's email.
	if err := rewriteVersionActor(root, "zz-test-qa", adaEmail); err != nil {
		t.Fatal(err)
	}
	h := &datasetAPI{env: OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode}, now: time.Now}
	read := func(ctx *fasthttp.RequestCtx) string {
		ctx.SetUserValue("id", "zz-test-qa")
		ctx.SetUserValue("n", "1")
		h.version(ctx)
		if ctx.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("version: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
		return string(ctx.Response.Body())
	}
	// As the versions list does (newActorNames), the name is given to every
	// caller; only an org admin keeps the email.
	if member := read(asCaller(actorMember)); strings.Contains(member, adaEmail) || !strings.Contains(member, `"actor_name":"Ada"`) {
		t.Errorf("version as a member: %s", member)
	}
	if admin := read(asCaller(actorAdmin)); !strings.Contains(admin, adaEmail) || !strings.Contains(admin, `"actor_name":"Ada"`) {
		t.Errorf("version as an admin: %s", admin)
	}
}

// rewriteVersionActor sets the actor of every recorded version of id.
func rewriteVersionActor(root, id, actor string) error {
	p := filepath.Join(root, ".heimdall", "datasets", id, "versions.jsonl")
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var out []byte
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return err
		}
		v["actor"] = actor
		b, _ := json.Marshal(v)
		out = append(append(out, b...), '\n')
	}
	return os.WriteFile(p, out, 0o600)
}
