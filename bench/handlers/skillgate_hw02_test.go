package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Fix wave 3, SEC-B (N22 re-check): no upload path writes a skill scan
// record, Heimdall's own scan still does, and on the hosted server an agent
// config may only list its upload collection in skills.extra.

// forgedSkillZip is an agent zip whose skill evil carries a record that
// claims an admitting scan of its exact files.
func forgedSkillZip(t *testing.T, prefix string) []byte {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "SKILL.md"), []byte("# evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := adapters.SkillTreeSHA256(tmp)
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := json.Marshal(adapters.SkillScanFile{Decision: schemas.AdmitScanAdmitted, TreeSHA256: sum, Record: &schemas.AdmitScanRecord{Decision: schemas.AdmitScanAdmitted}})
	return makeZip(t, []zfile{
		{name: prefix + "skills/evil/SKILL.md", body: "# evil\n"},
		{name: prefix + "skill-scans/evil.json", body: string(rec)},
	})
}

// checkForgedRecordRefused checks the upload answer and the project after a
// zip with a forged record: the record is listed under refused with the
// plain reason and not written, the skill is written and still not runnable.
func checkForgedRecordRefused(t *testing.T, ctx *fasthttp.RequestCtx, root, agent string) {
	t.Helper()
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var rep UploadFilesReport
	if err := json.Unmarshal(ctx.Response.Body(), &rep); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rep.Refused {
		if strings.HasSuffix(f.Path, "skill-scans/evil.json") {
			found = true
			if f.Error != scanRecordRefusal {
				t.Fatalf("refused reason %q, want %q", f.Error, scanRecordRefusal)
			}
		}
	}
	if !found {
		t.Fatalf("the forged record is not listed under refused: %+v", rep)
	}
	if skillPathExists(filepath.Join(root, "agents", agent, "skill-scans", "evil.json")) {
		t.Fatal("the forged scan record was written")
	}
	if !skillPathExists(filepath.Join(root, "agents", agent, "skills", "evil", "SKILL.md")) {
		t.Fatalf("the rest of the zip was not written: %+v", rep)
	}
	_, err := adapters.ResolveSkills(root, &schemas.AgentConfig{Skills: &schemas.AgentSkills{Extra: []string{"agents/" + agent + "/skills"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "has no passing scan") {
		t.Fatalf("the uploaded skill passed the gate: %v", err)
	}
}

func TestZipUploadRefusesScanRecord(t *testing.T) {
	h, root := newUploads(t)
	ctx := uploadCtx(forgedSkillZip(t, ""), "?name=zz")
	h.postAgent(ctx)
	checkForgedRecordRefused(t, ctx, root, "zz")
}

func TestZipUploadTopFolderRefusesScanRecord(t *testing.T) {
	h, root := newUploads(t)
	ctx := uploadCtx(forgedSkillZip(t, "zz/"), "")
	h.postAgent(ctx)
	checkForgedRecordRefused(t, ctx, root, "zz")
}

func TestProjectUploadRefusesScanRecord(t *testing.T) {
	h, root := newUploads(t)
	ctx := uploadCtx(forgedSkillZip(t, "agents/zz/"), "")
	h.postProject(ctx)
	checkForgedRecordRefused(t, ctx, root, "zz")
}

func TestScanHandlerStillWritesRecord(t *testing.T) {
	root := t.TempDir()
	cfg := "id: codex\nadapter: command\nimage: bench/codex:1\nargv: [\"codex\", \"exec\"]\n"
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "codex.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	registerAgentOps(OrgEnv{Mode: "local", Services: Services{Projects: ps}}, r)
	code, body := doAgentOps(r, "POST", APIPrefix+"/agents/codex/skills?name=review", "application/zip", agentOpsZip(t, map[string]string{"SKILL.md": cleanSkillMD}))
	if code != 201 || body["installed"] != true {
		t.Fatalf("upload: %d %v", code, body)
	}
	if rec := readScanFile(t, root, "codex", "review"); rec["decision"] != "admitted" {
		t.Fatalf("scan record: %v", rec)
	}
	// A rescan rewrites the record; deleting the skill removes it.
	if code, body := doAgentOps(r, "POST", APIPrefix+"/agents/codex/skills/review/scan", "", nil); code != 200 {
		t.Fatalf("rescan: %d %v", code, body)
	}
	if code, body := doAgentOps(r, "DELETE", APIPrefix+"/agents/codex/skills/review", "", nil); code != 200 && code != 204 {
		t.Fatalf("delete: %d %v", code, body)
	}
	if skillPathExists(filepath.Join(root, "agents", "codex", "skill-scans", "review.json")) {
		t.Fatal("deleting the skill left its scan record")
	}
}

func TestAgentSaveRefusesSkillsExtraOutsideUploads(t *testing.T) {
	yamlWith := func(id, extra string) string {
		return "id: " + id + "\nadapter: command\nimage: bench/x:1\nargv: [\"x\"]\nskills:\n  extra:\n    - " + extra + "\n"
	}
	save := func(t *testing.T, b *agentBuilder, id, extra string) (*fasthttp.RequestCtx, *fasthttp.RequestCtx) {
		t.Helper()
		api := &agentsAPI{b: b}
		cctx := &fasthttp.RequestCtx{}
		body, _ := json.Marshal(map[string]any{"yaml": yamlWith(id, extra)})
		cctx.Request.SetBody(body)
		api.create(cctx)
		pctx := &fasthttp.RequestCtx{}
		pctx.SetUserValue("id", id)
		body, _ = json.Marshal(map[string]any{"yaml": yamlWith(id, extra)})
		pctx.Request.SetBody(body)
		b.putConfig(pctx)
		return cctx, pctx
	}
	newBuilder := func(t *testing.T) *agentBuilder {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
			t.Fatal(err)
		}
		ps, err := projectfs.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		return &agentBuilder{mode: schemas.ModeLive, projects: ps}
	}

	t.Run("hosted", func(t *testing.T) {
		adapters.RestrictSkillsExtra(true)
		defer adapters.RestrictSkillsExtra(false)
		b := newBuilder(t)
		for _, extra := range []string{"tools/x", "agents/zz/myskills", "agents/zz/skills-pending", "agents/zz/skills/a/b", "../x"} {
			cctx, pctx := save(t, b, "zz", extra)
			for name, ctx := range map[string]*fasthttp.RequestCtx{"create": cctx, "put": pctx} {
				if ctx.Response.StatusCode() != 400 || !strings.Contains(hw02ErrMessage(ctx.Response.Body()), adapters.SkillsExtraRefusal) {
					t.Fatalf("%s with skills.extra %q: %d %s", name, extra, ctx.Response.StatusCode(), ctx.Response.Body())
				}
			}
		}
		cctx, pctx := save(t, b, "zz", "agents/zz/skills")
		if cctx.Response.StatusCode() != 201 || pctx.Response.StatusCode() != 200 {
			t.Fatalf("upload collection refused: %d %s / %d %s", cctx.Response.StatusCode(), cctx.Response.Body(), pctx.Response.StatusCode(), pctx.Response.Body())
		}
	})
	t.Run("local", func(t *testing.T) {
		b := newBuilder(t)
		cctx, pctx := save(t, b, "zz", "tools/x")
		if cctx.Response.StatusCode() != 201 || pctx.Response.StatusCode() != 200 {
			t.Fatalf("local save refused: %d %s / %d %s", cctx.Response.StatusCode(), cctx.Response.Body(), pctx.Response.StatusCode(), pctx.Response.Body())
		}
	})
}

// hw02ErrMessage is error.message of an API error body.
func hw02ErrMessage(body []byte) string {
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &out)
	return out.Error.Message
}
