//go:build integration

package scaffold

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/utils"
)

// Images the browser integration test needs besides the two it builds: the
// helper (ready checks, the client standing in for the agent) and the bench
// image the tool proxy runs from.
const (
	browserTestHelper    = "bench/coding-core-base:1"
	browserTestToolProxy = "bench/bifrost-bench:dev"
)

// TestBrowserTemplateInDocker starts the browser template's sample task the
// way a run does, with the real environment kernel: the web application as a
// hidden service, Playwright MCP as a proxied service, and the tool proxy.
// A client container on the run's service network, where the agent sits,
// runs the sample's reference script: raw MCP JSON-RPC to the browser
// through the proxy, which navigates to the web application, fills the form
// and submits it. Then it checks that the agent's network cannot reach the
// web application, that the browser container has no route out, that the
// exported state holds the new record and passes the task's verifier, and
// that every browser action is a ledger row.
func TestBrowserTemplateInDocker(t *testing.T) {
	for _, img := range []string{browserTestHelper, browserTestToolProxy} {
		if exec.Command("docker", "image", "inspect", img).Run() != nil {
			t.Skipf("image %s is missing (built by bifrost-bench init)", img)
		}
	}
	ctx := context.Background()
	root, dir := newSuiteDir(t, "hd-web-it", TemplateBrowser)
	s, task, err := suite.LoadTask(root, "hd-web-it", "new-customer")
	if err != nil {
		t.Fatal(err)
	}
	for i := range task.Services {
		tag := "hd-web-it-" + task.Services[i].ID + ":test"
		buildTestImage(t, tag, filepath.Join(dir, "services", task.Services[i].ID))
		task.Services[i].Image = tag
	}

	env := environments.Builtin(environments.DockerWorkspaceConfig{
		HelperImage: browserTestHelper, ToolProxyImage: browserTestToolProxy, Docker: utils.Docker{},
	})[0]
	svc, ok := env.(schemas.ServiceEnvironment)
	if !ok {
		t.Fatal("docker workspace runs no services")
	}
	runID := strings.Replace(utils.NewID("r", 8), "r_", "hd-web-", 1)
	rc := &schemas.RunContext{
		Run: &schemas.Run{RunID: runID, ExpID: "e_hdweb"}, Slot: &schemas.Slot{Condition: schemas.ConditionClean},
		Suite: s, Task: task, Agent: &schemas.AgentConfig{}, Dir: t.TempDir(),
	}
	t.Cleanup(func() {
		if err := env.Destroy(context.Background(), rc); err != nil {
			t.Errorf("destroy: %v", err)
		}
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label="+utils.LabelRun+"="+runID).Output()
		if strings.TrimSpace(string(out)) != "" {
			t.Errorf("containers of %s left behind: %s", runID, out)
		}
	})

	handles, results, err := svc.StartServices(ctx, rc)
	if err != nil {
		t.Fatalf("start services: %v (%+v)", err, results)
	}
	byID := map[string]schemas.ServiceHandle{}
	for _, h := range handles {
		byID[h.Spec.ID] = h
	}
	browserURL := "http://toolproxy:9200/s/browser/mcp"
	if h := byID["browser"]; !strings.HasPrefix(browserURL, h.URL) || h.URL == "" {
		t.Fatalf("browser handle URL %q, want the tool proxy", h.URL)
	}
	if h := byID["webapp"]; h.URL != "" || h.Upstream == "" {
		t.Fatalf("webapp handle %+v: a hidden service has no agent URL", h)
	}

	// The agent's place: a container on the run's service network only.
	ref, err := os.ReadFile(filepath.Join(task.Dir, "solution", "reference.sh"))
	if err != nil {
		t.Fatal(err)
	}
	servers, err := json.Marshal(map[string]map[string]string{"browser": {"url": browserURL}})
	if err != nil {
		t.Fatal(err)
	}
	direct := `import urllib.request
try:
    urllib.request.urlopen("http://webapp:8000/healthz", timeout=5)
    print("DIRECT_REACHED")
except OSError as e:
    print("DIRECT_REFUSED", e)
`
	agent := exec.Command("docker", "run", "--rm", "-i", "--network", "bench-net-"+runID, "-e", "BENCH_MCP_SERVERS="+string(servers),
		"--entrypoint", "sh", browserTestHelper, "-c", "cat > /tmp/ref.sh && sh /tmp/ref.sh && python3 -I -c '"+direct+"'")
	agent.Stdin = strings.NewReader(string(ref))
	out, err := agent.CombinedOutput()
	if err != nil {
		t.Fatalf("reference script through the browser: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "created Mira Okafor") || !strings.Contains(string(out), "DIRECT_REFUSED") {
		t.Fatalf("agent container output:\n%s", out)
	}

	// The browser has no route out of the run's internal networks.
	egress := `fetch("http://1.1.1.1/",{signal:AbortSignal.timeout(5000)}).then(r=>console.log("EGRESS_OPEN",r.status)).catch(e=>console.log("EGRESS_CLOSED",e.cause?e.cause.code:e.name))`
	out, err = exec.Command("docker", "exec", byID["browser"].Container, "node", "-e", egress).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "EGRESS_CLOSED") {
		t.Fatalf("browser egress check: %v %s", err, out)
	}

	// Z03: the live screen shows the page the reference used, and a
	// person's input reaches it.
	ls, ok := env.(schemas.LiveScreen)
	if !ok {
		t.Fatal("docker workspace shows no live screen")
	}
	if got := ls.LiveServices(rc); len(got) != 1 || got[0].ID != "browser" || !got[0].Takeover {
		t.Fatalf("live services %+v", got)
	}
	img, mime, err := ls.LiveFrame(ctx, rc, "browser")
	if err != nil || mime != "image/jpeg" || len(img) < 100 {
		t.Fatalf("live frame: %v %s %d bytes", err, mime, len(img))
	}
	if err := ls.LiveInput(ctx, rc, "browser", schemas.HumanAction{Action: schemas.HumanScroll, Direction: "down"}); err != nil {
		t.Fatalf("live input: %v", err)
	}

	files, err := svc.ExportState(ctx, rc)
	if err != nil || len(files) != 1 || files[0].Error != "" {
		t.Fatalf("export state: %v %+v", err, files)
	}
	db := readFile(t, filepath.Join(rc.Dir, filepath.FromSlash(files[0].Path)))
	if !strings.Contains(db, `"email": "mira.okafor@lumen.example"`) || !strings.Contains(db, `"plan": "business"`) {
		t.Fatalf("exported state lacks the new customer:\n%s", db)
	}
	if py, err := lookPython(); err == nil {
		grade := exec.Command(py, "-I", filepath.Join(task.Dir, "verifier", "tests", "verify.py"))
		gout := t.TempDir()
		grade.Env = append(os.Environ(), "GRADER_STATE="+filepath.Join(rc.Dir, "state"), "GRADER_OUT="+gout)
		if b, err := grade.CombinedOutput(); err != nil {
			t.Fatalf("verify.py: %v\n%s", err, b)
		}
		if reward := readFile(t, filepath.Join(gout, "reward.json")); !strings.Contains(reward, `"reward": 1`) {
			t.Fatalf("reward %s", reward)
		}
	}

	// Z03: the browser's video, HAR and console are in recordings/, not in
	// state/ where the verifier reads.
	idx := readFile(t, filepath.Join(rc.Dir, schemas.RecordingsDir, schemas.RecordingsIndexFile))
	kinds := map[string]schemas.RecordingRecord{}
	for _, line := range strings.Split(strings.TrimSpace(idx), "\n") {
		var r schemas.RecordingRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("recordings index line %q: %v", line, err)
		}
		if r.Error != "" {
			t.Errorf("recording %s/%s: %s", r.Service, r.ID, r.Error)
		}
		kinds[r.Kind] = r
	}
	if v := kinds[schemas.RecordingVideo]; v.File == "" || v.Bytes < 1000 || v.StartedAt.IsZero() || v.Mime != "video/webm" {
		t.Errorf("video record %+v", v)
	}
	if h := kinds[schemas.RecordingHAR]; h.File == "" || !strings.Contains(readFile(t, filepath.Join(rc.Dir, schemas.RecordingsDir, "browser", h.File)), "webapp:8000") {
		t.Errorf("HAR record %+v", h)
	}
	if _, ok := kinds[schemas.RecordingConsole]; !ok {
		t.Error("no console record")
	}
	if entries, _ := os.ReadDir(filepath.Join(rc.Dir, "state", "browser")); len(entries) != 0 {
		t.Errorf("recordings leaked into the verifier's state: %v", entries)
	}

	f, err := os.Open(filepath.Join(rc.Dir, "tools.jsonl"))
	if err != nil {
		t.Fatalf("tool ledger: %v", err)
	}
	defer f.Close()
	calls, bad := toolproxy.ReadLedger(f)
	seen := map[string]bool{}
	for _, c := range calls {
		if c.Server != "browser" || c.Protocol != "mcp" || c.IsError {
			t.Errorf("ledger row %+v", c)
		}
		seen[c.Tool] = true
	}
	for _, tool := range []string{"browser_navigate", "browser_snapshot", "browser_fill_form", "browser_click"} {
		if !seen[tool] {
			t.Errorf("no %s row in the ledger (%d rows, %d bad)", tool, len(calls), bad)
		}
	}
}

// buildTestImage builds tag from dir and removes the tag after the test.
func buildTestImage(t *testing.T, tag, dir string) {
	t.Helper()
	if out, err := exec.Command("docker", "build", "-q", "-t", tag, dir).CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", tag, err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "image", "rm", tag).CombinedOutput(); err != nil {
			t.Logf("remove %s: %v %s", tag, err, out)
		}
	})
}
