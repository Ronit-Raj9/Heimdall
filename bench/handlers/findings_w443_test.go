package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/findings"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

const (
	w443OldDoc = "id: bot\nadapter: command\nimage: bench/bot:1\nargv: [\"bot\"]\n"
	w443NewDoc = "id: bot\nadapter: command\nimage: bench/bot:2\nargv: [\"bot\"]\n"
)

// w443Setup is a project with agents/bot.yaml (current content cur), the
// test experiment whose arm A ran bot at the old config hash and failed t01,
// and one finding on coding-core/t01.
func w443Setup(t *testing.T, cur string) (*router.Router, string, *fakeLauncher, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "bot.yaml"), []byte(cur), 0o644); err != nil {
		t.Fatal(err)
	}
	oldHash, err := agentversions.ConfigHash([]byte(w443OldDoc))
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndex()
	m := idx.manifests[testExp]
	m.Experiment.Arms = map[string]schemas.ArmSpec{"A": {Label: "baseline", Agent: "agents/bot.yaml"}, "B": {Label: "other"}}
	m.Arms = map[string]schemas.ArmManifest{
		"A": {Label: "baseline", Config: schemas.AgentConfig{ID: "bot", Adapter: "command", Image: "bench/bot:1", Argv: []string{"bot"}}, ConfigHash: oldHash},
		"B": {Label: "other", ConfigHash: "sha256:other"},
	}
	l := &fakeLauncher{}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: idx, Launcher: l}}
	r := router.New()
	registerFindings(env, r)
	f, _, err := findings.Add(findings.Open(root), findings.Finding{Key: "cluster:sig1", Title: "Forgets tests", Source: "cluster", Origin: "verified",
		Tasks: []findings.TaskRef{{Suite: "coding-core", Task: "t01"}}}, "Ann", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return r, root, l, f.ID
}

func w443Rerun(r *router.Router, id string) (int, map[string]any) {
	body, _ := json.Marshal(map[string]any{"exp_id": testExp, "arm": "A", "trials": 3, "yes": true})
	return doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/rerun", "application/json", body)
}

func w443OldYAML(t *testing.T, root string, planned *schemas.Experiment) string {
	t.Helper()
	old, ok := planned.Arms["old"]
	if !ok {
		t.Fatalf("planned arms %v", planned.Arms)
	}
	if !strings.HasPrefix(old.Agent, "findings/reruns/") || !strings.HasSuffix(old.Agent, "/old.yaml") {
		t.Fatalf("old arm agent %q", old.Agent)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(old.Agent)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRerunRefusesSameConfig(t *testing.T) {
	r, _, l, id := w443Setup(t, w443OldDoc)
	code, body := w443Rerun(r, id)
	if code != fasthttp.StatusConflict {
		t.Fatalf("status %d (%v)", code, body)
	}
	if !strings.Contains(body["error"].(map[string]any)["message"].(string), "The agent has not changed since this failure. Change it first, then check the fix.") {
		t.Fatalf("body %v", body)
	}
	if l.planned != nil {
		t.Fatal("a rerun was planned")
	}
}

func TestRerunBuildsOldArmFromVersion(t *testing.T) {
	r, root, l, id := w443Setup(t, w443NewDoc)
	if _, _, err := agentversions.Snapshot(root, "bot", []byte(w443OldDoc), "Ann", "first"); err != nil {
		t.Fatal(err)
	}
	code, body := w443Rerun(r, id)
	if code != fasthttp.StatusAccepted {
		t.Fatalf("status %d (%v)", code, body)
	}
	p := l.planned
	if p == nil || p.RerunOf != testExp || len(p.Arms) != 2 || p.Arms["fixed"].Agent != "agents/bot.yaml" {
		t.Fatalf("planned %+v", p)
	}
	if got := w443OldYAML(t, root, p); got != w443OldDoc {
		t.Fatalf("old.yaml %q", got)
	}
	if len(p.Conditions) != 1 || len(p.Conditions[0].Tasks.IDs) != 1 || p.Conditions[0].Tasks.IDs[0] != "t01" || p.Conditions[0].Trials != 3 {
		t.Fatalf("conditions %+v", p.Conditions)
	}
	if _, ok := body["note"]; ok {
		t.Fatalf("note on a stored version: %v", body)
	}
	f, _ := findings.Open(root).Get(id)
	if len(f.Reruns) != 1 || f.Reruns[0] != testExp {
		t.Fatalf("reruns %v", f.Reruns)
	}
}

func TestRerunOldArmFromSealedConfig(t *testing.T) {
	r, root, l, id := w443Setup(t, w443NewDoc)
	code, body := w443Rerun(r, id)
	if code != fasthttp.StatusAccepted {
		t.Fatalf("status %d (%v)", code, body)
	}
	got := w443OldYAML(t, root, l.planned)
	if !strings.Contains(got, "bench/bot:1") {
		t.Fatalf("old.yaml %q", got)
	}
	if body["note"] != "The old version was rebuilt from the settings saved with the first run; files it reads, such as skills, are today's." {
		t.Fatalf("note %v", body["note"])
	}
}

func TestFindingsRoutes(t *testing.T) {
	r, _, _, id := w443Setup(t, w443NewDoc)
	code, body := doAgentOps(r, "GET", APIPrefix+"/findings", "", nil)
	if code != fasthttp.StatusOK || len(body["findings"].([]any)) != 1 || body["counts"].(map[string]any)["open"] != 1.0 {
		t.Fatalf("list %d %v", code, body)
	}
	if code, _ := doAgentOps(r, "GET", APIPrefix+"/findings/"+id, "", nil); code != fasthttp.StatusOK {
		t.Fatalf("get %d", code)
	}
	if code, _ := doAgentOps(r, "GET", APIPrefix+"/findings/fnd-000000000000", "", nil); code != fasthttp.StatusNotFound {
		t.Fatalf("unknown get %d", code)
	}
	code, body = doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/expected", "application/json", []byte(`{"reason":"Known and accepted"}`))
	if code != fasthttp.StatusOK || body["state"] != "expected" {
		t.Fatalf("expected %d %v", code, body)
	}
	code, body = doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/reopen", "application/json", []byte(`{"reason":"It matters"}`))
	if code != fasthttp.StatusOK || body["state"] != "reopened" {
		t.Fatalf("reopen %d %v", code, body)
	}
	if code, _ := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"nope","ref":"x"}`)); code != fasthttp.StatusBadRequest {
		t.Fatalf("bad source %d", code)
	}
	if code, _ := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"investigation","ref":"../x:0"}`)); code != fasthttp.StatusBadRequest {
		t.Fatalf("bad investigation ref %d", code)
	}
}

func TestFindingsAddFromInvestigation(t *testing.T) {
	r, root, _, _ := w443Setup(t, w443NewDoc)
	rep := `{"id":"job1","exp_id":"e_test","items":[{"id":"i0","run_id":"r_t01_A","title":"t","hypothesis":"It never ran the tests","kind":"agent_mistake","quotes":[{"run_id":"r_t01_A","seq":4,"text":"skip tests"}],"origin":"model"}],
	"groups":[{"title":"Skips the tests","kind":"agent_mistake","items":["i0","i9"],"runs":1,"tasks":["coding-core/t01"]}]}`
	if err := os.MkdirAll(filepath.Join(root, "investigations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "investigations", "job1.json"), []byte(rep), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"investigation","ref":"job1:0"}`))
	if code != fasthttp.StatusCreated {
		t.Fatalf("status %d %v", code, body)
	}
	if body["origin"] != "model" || body["hypothesis"] != "It never ran the tests" || body["title"] != "Skips the tests" || len(body["quotes"].([]any)) != 1 {
		t.Fatalf("finding %v", body)
	}
	if code, _ := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"investigation","ref":"job1:0"}`)); code != fasthttp.StatusOK {
		t.Fatalf("second add %d", code)
	}
	if code, _ := doAgentOps(r, "POST", APIPrefix+"/findings", "application/json", []byte(`{"source":"investigation","ref":"job1:5"}`)); code != fasthttp.StatusNotFound {
		t.Fatalf("unknown group %d", code)
	}
}

func TestFindingsCheckJudgesNewestRerun(t *testing.T) {
	r, _, _, id := w443Setup(t, w443NewDoc)
	// No rerun yet: the check is refused.
	if code, _ := doAgentOps(r, "POST", APIPrefix+"/findings/"+id+"/check", "application/json", []byte(`{}`)); code != fasthttp.StatusConflict {
		t.Fatalf("check with no rerun %d", code)
	}
}

func TestFindingsRoutesPolicy(t *testing.T) {
	want := map[string]auth.Role{
		"GET " + APIPrefix + "/findings":                auth.RoleViewer,
		"GET " + APIPrefix + "/findings/{id}":           auth.RoleViewer,
		"POST " + APIPrefix + "/findings":               auth.RoleMember,
		"POST " + APIPrefix + "/findings/{id}/expected": auth.RoleMember,
		"POST " + APIPrefix + "/findings/{id}/reopen":   auth.RoleMember,
		"POST " + APIPrefix + "/findings/{id}/rerun":    auth.RoleMember,
		"POST " + APIPrefix + "/findings/{id}/check":    auth.RoleMember,
	}
	for k, role := range want {
		method, path, _ := strings.Cut(k, " ")
		p, ok := tenancy.OrgPolicy[method+" "+path]
		if !ok {
			t.Errorf("%s has no policy", k)
			continue
		}
		if p.Access != tenancy.AccessOrg || p.Min != role {
			t.Errorf("%s = %+v, want org(%s)", k, p, role)
		}
	}
}
