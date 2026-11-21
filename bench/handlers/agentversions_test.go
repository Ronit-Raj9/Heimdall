package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func versionsRouter(t *testing.T, index ReadIndex, library string) (*router.Router, string, projectfs.Store) {
	t.Helper()
	r, st := builderRouter(t)
	pf := &projectFilesHandler{mode: schemas.Mode("live"), store: st}
	r.PUT(APIPrefix+"/project/files/{area}/{path:*}", pf.put)
	h := &agentVersionsHandler{mode: schemas.Mode("live"), projects: st, index: index, library: library}
	r.GET(APIPrefix+"/agents/{id}/versions", h.list)
	r.GET(APIPrefix+"/agents/{id}/versions/{n}", h.get)
	r.POST(APIPrefix+"/agents/{id}/versions", h.create)
	r.GET(APIPrefix+"/agents/diff/versions", func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(299) })
	return r, st.Root(), st
}

func versionsOf(t *testing.T, r *router.Router, id string) AgentVersionList {
	t.Helper()
	code, body := abCall(r, "GET", APIPrefix+"/agents/"+id+"/versions", nil)
	if code != 200 {
		t.Fatalf("list %s: %d %s", id, code, body)
	}
	var out AgentVersionList
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAgentVersionsOnEverySave(t *testing.T) {
	r, root, st := versionsRouter(t, nil, "")
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "my-codex"}); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	vs := versionsOf(t, r, "my-codex")
	if vs.Agent != "my-codex" || len(vs.Versions) != 1 || vs.Versions[0].Version != 1 || vs.Versions[0].Hash == "" {
		t.Fatalf("after create: %+v", vs)
	}
	code, body := abCall(r, "GET", APIPrefix+"/agents/my-codex/config", nil)
	if code != 200 {
		t.Fatal(code)
	}
	var doc AgentConfigDoc
	json.Unmarshal(body, &doc)
	// Same config again: no new version.
	if code, body := abCall(r, "PUT", APIPrefix+"/agents/my-codex/config", AgentConfigPut{YAML: doc.YAML}); code != 200 {
		t.Fatalf("put same: %d %s", code, body)
	}
	if n := len(versionsOf(t, r, "my-codex").Versions); n != 1 {
		t.Fatalf("same config made a version: %d", n)
	}
	changed := doc.YAML + "\n# trailing comment only\n"
	abCall(r, "PUT", APIPrefix+"/agents/my-codex/config", AgentConfigPut{YAML: changed})
	if n := len(versionsOf(t, r, "my-codex").Versions); n != 1 {
		t.Fatalf("comment-only edit made a version: %d", n)
	}
	f := doc.Fields
	f.Model = "gpt-5-mini"
	if code, body := abCall(r, "PUT", APIPrefix+"/agents/my-codex/config", AgentConfigPut{Fields: &f}); code != 200 {
		t.Fatalf("put fields: %d %s", code, body)
	}
	vs = versionsOf(t, r, "my-codex")
	if len(vs.Versions) != 2 || vs.Versions[1].Version != 2 {
		t.Fatalf("after model change: %+v", vs)
	}
	// Files API edit of agents/<id>.yaml.
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("PUT")
	ctx.Request.SetRequestURI(APIPrefix + "/project/files/agents/my-codex.yaml?note=hand+edit")
	ctx.Request.SetBody([]byte(doc.YAML))
	r.Handler(&ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("files put: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	vs = versionsOf(t, r, "my-codex")
	if len(vs.Versions) != 3 || vs.Versions[2].Note != "hand edit" || vs.Versions[2].Hash != vs.Versions[0].Hash {
		t.Fatalf("after files edit: %+v", vs)
	}
	// Every version links the Files API version of its save; one save, one
	// agent version at most.
	fvs, _ := st.Versions(context.Background(), projectfs.AreaAgents, "my-codex.yaml")
	if len(fvs) != 5 || vs.Versions[2].FileVersion != fvs[0].ID || vs.Versions[1].FileVersion != fvs[1].ID || vs.Versions[0].FileVersion != fvs[4].ID {
		t.Fatalf("file version links: %+v\n%+v", vs.Versions, fvs)
	}
	code, body = abCall(r, "GET", APIPrefix+"/agents/my-codex/versions/2", nil)
	var v AgentVersionDoc
	json.Unmarshal(body, &v)
	if code != 200 || v.Version != 2 || !strings.Contains(v.YAML, "gpt-5-mini") || v.Hash != vs.Versions[1].Hash {
		t.Fatalf("get v2: %d %s", code, body)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/agents/my-codex/versions/9", nil); code != 404 {
		t.Fatalf("missing version: %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, ".heimdall", "agent-versions", "my-codex", "v3.yaml")); err != nil {
		t.Fatal(err)
	}
	// Explicit snapshot of an unchanged config returns the latest, 200.
	code, body = abCall(r, "POST", APIPrefix+"/agents/my-codex/versions", AgentVersionCreate{Note: "release"})
	var c AgentVersionCreated
	json.Unmarshal(body, &c)
	if code != 200 || c.Created || c.Version != 3 {
		t.Fatalf("post unchanged: %d %s", code, body)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/agents/diff/versions", nil); code != 299 {
		t.Fatalf("static diff route shadowed: %d", code)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/agents/none/versions", nil); code != 404 {
		t.Fatalf("unknown agent: %d", code)
	}
}

// An agent saved before versioning (Files API history only) gets its
// versions from that history, linked by file version id; runs are counted
// by config hash.
func TestAgentVersionsFromFileHistoryAndRuns(t *testing.T) {
	r, st := builderRouter(t)
	h := &agentVersionsHandler{mode: schemas.Mode("live"), projects: st}
	r.GET(APIPrefix+"/agents/{id}/versions", h.list)
	r.POST(APIPrefix+"/agents/{id}/versions", h.create)
	old := strings.Replace(versionAgentDoc, "id: exploit", "id: old", 1)
	ctx := context.Background()
	for _, doc := range []string{old, strings.Replace(old, "900", "60", 1), "# comment\n" + strings.Replace(old, "900", "60", 1)} {
		if _, err := st.Write(ctx, projectfs.AreaAgents, "old.yaml", []byte(doc), projectfs.WriteOpts{Actor: "ana"}); err != nil {
			t.Fatal(err)
		}
	}
	fvs, _ := st.Versions(ctx, projectfs.AreaAgents, "old.yaml") // newest first
	vs := versionsOf(t, r, "old")
	if len(vs.Versions) != 2 || vs.Versions[0].FileVersion != fvs[2].ID || vs.Versions[1].FileVersion != fvs[1].ID || vs.Versions[1].CreatedBy != "ana" {
		t.Fatalf("backfill: %+v\nfile versions %+v", vs, fvs)
	}
	code, body := abCall(r, "POST", APIPrefix+"/agents/old/versions", AgentVersionCreate{Note: "baseline"})
	var c AgentVersionCreated
	json.Unmarshal(body, &c)
	if code != 200 || c.Created || c.Version != 2 {
		t.Fatalf("post unchanged: %d %s", code, body)
	}
	idx := &versionsIndex{hash: c.Hash}
	h2 := &agentVersionsHandler{index: idx}
	if got := h2.runsByHash()[c.Hash]; got != 3 {
		t.Fatalf("runs %d", got)
	}
}

func TestAgentVersionsLibraryAgent(t *testing.T) {
	lib := t.TempDir()
	os.MkdirAll(filepath.Join(lib, "agents"), 0o755)
	os.WriteFile(filepath.Join(lib, "agents", "exploit.yaml"), []byte(versionAgentDoc), 0o644)
	r, _, _ := versionsRouter(t, nil, lib)
	vs := versionsOf(t, r, "exploit")
	want, _ := agentversions.ConfigHash([]byte(versionAgentDoc))
	if !vs.Builtin || len(vs.Versions) != 1 || vs.Versions[0].Version != 1 || vs.Versions[0].Hash != want {
		t.Fatalf("%+v", vs)
	}
	if code, body := abCall(r, "GET", APIPrefix+"/agents/exploit/versions/1", nil); code != 200 || !strings.Contains(string(body), "kind: exploit") {
		t.Fatalf("v1: %d %s", code, body)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/agents/exploit/versions/2", nil); code != 404 {
		t.Fatalf("v2: %d", code)
	}
	if code, _ := abCall(r, "POST", APIPrefix+"/agents/exploit/versions", nil); code != 403 {
		t.Fatalf("post builtin: %d", code)
	}
}

const versionAgentDoc = `id: exploit
adapter: scripted
image: bench/coding-core-base:1
scripted:
  kind: exploit
model: claude-sonnet-4-5
budgets:
  wall_s: 900
  cpus: 1
  mem_gb: 1
gateway:
  provider: anthropic
`

// versionsIndex has one experiment: two runs carry the hash, one gets it
// from its manifest arm, one belongs to another config.
type versionsIndex struct {
	ReadIndex
	hash string
}

func (v *versionsIndex) Experiments() []string { return []string{"exp_1"} }
func (v *versionsIndex) Manifest(string) (*schemas.Manifest, bool) {
	return &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {ConfigHash: v.hash}, "B": {ConfigHash: "other"}}}, true
}
func (v *versionsIndex) Runs(string) []RunView {
	run := func(arm, hash string) RunView {
		var rv RunView
		rv.Slot.Arm = arm
		rv.Run.Agent.ConfigHash = hash
		return rv
	}
	return []RunView{run("A", v.hash), run("A", v.hash), run("A", ""), run("B", "")}
}

// TestAgentVersionsRegisterCandidate covers CI registration: a pull
// request's config becomes a version without changing the saved config, the
// live version stays the saved one, and the same config again (or the live
// config) reuses its version.
func TestAgentVersionsRegisterCandidate(t *testing.T) {
	r, _, _ := versionsRouter(t, nil, "")
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "ci-agent"}); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	code, body := abCall(r, "GET", APIPrefix+"/agents/ci-agent/config", nil)
	if code != 200 {
		t.Fatal(code)
	}
	var doc AgentConfigDoc
	json.Unmarshal(body, &doc)
	if vs := versionsOf(t, r, "ci-agent"); vs.Live != 1 {
		t.Fatalf("live before: %+v", vs)
	}
	pr := strings.Replace(doc.YAML, "model: "+doc.Fields.Model, "model: gpt-5-mini", 1)
	if pr == doc.YAML {
		t.Fatalf("no model line in:\n%s", doc.YAML)
	}
	code, body = abCall(r, "POST", APIPrefix+"/agents/ci-agent/versions", AgentVersionCreate{YAML: pr, Note: "pull request #7"})
	if code != 201 {
		t.Fatalf("register: %d %s", code, body)
	}
	var made AgentVersionCreated
	json.Unmarshal(body, &made)
	if made.Version != 2 || !made.Created || made.Note != "pull request #7" {
		t.Fatalf("registered: %+v", made)
	}
	// The saved config did not change and stays live.
	code, body = abCall(r, "GET", APIPrefix+"/agents/ci-agent/config", nil)
	var after AgentConfigDoc
	json.Unmarshal(body, &after)
	if after.YAML != doc.YAML {
		t.Fatal("registering a candidate changed the saved config")
	}
	if vs := versionsOf(t, r, "ci-agent"); vs.Live != 1 || len(vs.Versions) != 2 {
		t.Fatalf("live after: %+v", vs)
	}
	// Same candidate again, and the live config: no new version.
	for _, y := range []string{pr, doc.YAML} {
		code, body = abCall(r, "POST", APIPrefix+"/agents/ci-agent/versions", AgentVersionCreate{YAML: y})
		var again AgentVersionCreated
		json.Unmarshal(body, &again)
		if code != 200 || again.Created {
			t.Fatalf("re-register: %d %s", code, body)
		}
	}
	if n := len(versionsOf(t, r, "ci-agent").Versions); n != 2 {
		t.Fatalf("re-register made versions: %d", n)
	}
	// A config that does not decode is refused.
	if code, body := abCall(r, "POST", APIPrefix+"/agents/ci-agent/versions", AgentVersionCreate{YAML: "adapter: [unclosed"}); code != 400 {
		t.Fatalf("bad yaml: %d %s", code, body)
	}
}
