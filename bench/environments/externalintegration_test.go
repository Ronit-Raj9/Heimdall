//go:build integration

package environments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// extUpstreamScript stands in for the internet host of an external API: GET
// /v1/forecast answers JSON and echoes the Authorization header it got (so
// the test can prove the proxy injected the credential and kept it out of
// the ledger); every other path is a 404. Each request path is printed.
const extUpstreamScript = `import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        print("PATH " + self.path, flush=True)
        if self.path.startswith("/v1/forecast"):
            body = json.dumps({"city": "oslo", "temp_c": 4, "auth_seen": self.headers.get("Authorization", "")}).encode()
            self.send_response(200)
        else:
            body = b'{"error":"not found"}'
            self.send_response(404)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass
http.server.ThreadingHTTPServer(("0.0.0.0", 8080), H).serve_forever()
`

// extAgentScript runs on the agent's side of a run (the service network only)
// and prints one JSON object: whether direct connections out worked, and the
// status and body of each call through the tool proxy.
const extAgentScript = `import json, socket, sys, urllib.request, urllib.error
out = {}
def tcp(name, host, port):
    try:
        socket.create_connection((host, port), timeout=3).close()
        out[name] = "connected"
    except Exception as e:
        out[name] = "blocked: %s" % e
tcp("direct_upstream", "api.hd-ext.test", 8080)
tcp("direct_internet", "1.1.1.1", 443)
for a in sys.argv[1:]:
    if a.startswith("tcp:"):
        h, p = a[4:].rsplit(":", 1)
        tcp(a, h, int(p))
        continue
    try:
        r = urllib.request.urlopen("http://toolproxy:9200/x/weather" + a, timeout=20)
        out[a] = [r.status, r.read().decode()]
    except urllib.error.HTTPError as e:
        out[a] = [e.code, e.read().decode()]
    except Exception as e:
        out[a] = [0, str(e)]
print(json.dumps(out))
`

const hdExtSecret = "hd-ext-test-secret-4242"

func TestExternalAPIIntegration(t *testing.T) {
	ctx := t.Context()
	suffix := extRandHex(t)
	tp := extProxyImage(t, suffix)

	upNet, upName := "hd-ext-up-"+suffix, "hd-ext-up-"+suffix
	extDocker(t, "network", "create", upNet)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", upNet).Run() })
	extDocker(t, "run", "-d", "--name", upName, "--network", upNet, "--network-alias", "api.hd-ext.test",
		testImage, "python3", "-c", extUpstreamScript)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", upName).Run() })

	t.Setenv("HD_EXT_TEST_KEY", hdExtSecret)
	api := schemas.ExternalAPI{
		ID: "weather", BaseURL: "http://api.hd-ext.test:8080/v1", Cassette: "weather.cassette.jsonl",
		Allow:   []schemas.ExternalAllow{{Methods: []string{"GET"}, PathPrefix: "/forecast"}},
		AuthEnv: "HD_EXT_TEST_KEY", AuthScheme: "Bearer",
	}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{}, HelperImage: testImage, ToolProxyImage: tp})

	// Run 1, record mode: live calls through the forwarder, cassette written.
	rec := extRun(t, e, "hd-ext-rec-"+suffix, api, schemas.ExternalRecord, nil)
	handles, _, err := e.StartServices(ctx, rec)
	if err != nil {
		t.Fatalf("start record run: %v", err)
	}
	if len(handles) != 1 || handles[0].URL != "http://toolproxy:9200/x/weather" {
		t.Fatalf("handles %+v", handles)
	}
	// The stand-in internet host joins the forwarder's outbound network,
	// where a real host would be reached through the host's route out.
	extDocker(t, "network", "connect", "--alias", "api.hd-ext.test", utils.ExternalEgressName(rec.Run.RunID, "weather"), upName)
	got := extAgentCalls(t, rec.Run.RunID, "/forecast?city=oslo", "/admin", "tcp:"+utils.ExternalEgressAddr(rec.Run.RunID, "weather"))
	for _, k := range []string{"direct_upstream", "direct_internet", "tcp:" + utils.ExternalEgressAddr(rec.Run.RunID, "weather")} {
		if !strings.HasPrefix(got[k].(string), "blocked") {
			t.Fatalf("agent side reached %s directly: %v", k, got[k])
		}
	}
	ok := got["/forecast?city=oslo"].([]any)
	if ok[0].(float64) != 200 || !strings.Contains(ok[1].(string), `"temp_c": 4`) {
		t.Fatalf("allowed call: %v", ok)
	}
	if denied := got["/admin"].([]any); denied[0].(float64) == 200 || denied[0].(float64) == 404 {
		t.Fatalf("call outside the allowlist was not blocked by the proxy: %v", denied)
	}
	if _, err := e.ExportState(ctx, rec); err != nil {
		t.Fatal(err)
	}
	logs, _ := exec.Command("docker", "logs", upName).CombinedOutput()
	if !strings.Contains(string(logs), "PATH /v1/forecast?city=oslo") || strings.Contains(string(logs), "/v1/admin") {
		t.Fatalf("upstream saw:\n%s", logs)
	}
	ledger := extRunFile(t, rec.Dir, ToolsFile)
	cassette := extRunFile(t, rec.Dir, filepath.Join(utils.ExternalFolder, "weather"+utils.ExternalCassetteSuffix))
	for name, b := range map[string]string{"ledger": ledger, "cassette": cassette} {
		if strings.Contains(b, hdExtSecret) {
			t.Fatalf("%s holds the credential:\n%s", name, b)
		}
	}
	if !strings.Contains(ledger, `"protocol":"external"`) || !strings.Contains(ledger, `"tool":"GET /forecast"`) || !strings.Contains(ledger, `"blocked":true`) {
		t.Fatalf("ledger:\n%s", ledger)
	}
	if !strings.Contains(cassette, `"path":"/forecast"`) {
		t.Fatalf("cassette:\n%s", cassette)
	}
	extDocker(t, "network", "disconnect", utils.ExternalEgressName(rec.Run.RunID, "weather"), upName)
	if err := e.Destroy(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Run 2, replay mode from the recorded cassette, with the internet host
	// gone and no forwarder at all.
	extDocker(t, "rm", "-f", upName)
	rep := extRun(t, e, "hd-ext-rep-"+suffix, api, "", []byte(cassette))
	if _, _, err := e.StartServices(ctx, rep); err != nil {
		t.Fatalf("start replay run: %v", err)
	}
	if out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name=bench-eg-"+rep.Run.RunID).Output(); strings.TrimSpace(string(out)) != "" {
		t.Fatalf("replay run started a forwarder: %s", out)
	}
	got = extAgentCalls(t, rep.Run.RunID, "/forecast?city=oslo", "/forecast?city=bergen")
	if r := got["/forecast?city=oslo"].([]any); r[0].(float64) != 200 || !strings.Contains(r[1].(string), `"temp_c": 4`) {
		t.Fatalf("replayed call: %v", r)
	}
	if r := got["/forecast?city=bergen"].([]any); r[0].(float64) != 599 {
		t.Fatalf("unmatched call: %v", r)
	}
	if _, err := e.ExportState(ctx, rep); err != nil {
		t.Fatal(err)
	}
	ledger = extRunFile(t, rep.Dir, ToolsFile)
	if !strings.Contains(ledger, `"replayed":true`) || !strings.Contains(ledger, `"unmatched":true`) {
		t.Fatalf("replay ledger:\n%s", ledger)
	}
}

// extRun is a run context for one external API in mode (empty: the
// default), with cassette written into the task folder when given. Destroy
// runs at cleanup.
func extRun(t *testing.T, e *dockerWorkspace, runID string, api schemas.ExternalAPI, mode string, cassette []byte) *schemas.RunContext {
	t.Helper()
	dir := t.TempDir()
	if cassette != nil {
		if err := os.WriteFile(filepath.Join(dir, api.Cassette), cassette, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rc := &schemas.RunContext{
		Run:      &schemas.Run{RunID: runID, ExpID: "hd-ext-exp"},
		Suite:    &schemas.Suite{ID: "hd-ext", BaseImage: schemas.ImageSpec{Tag: testImage}},
		Task:     &schemas.Task{ID: "t", Dir: dir, External: []schemas.ExternalAPI{api}},
		Manifest: &schemas.Manifest{Experiment: schemas.Experiment{External: mode}},
		Dir:      t.TempDir(),
	}
	// t.Context is already cancelled when cleanups run.
	t.Cleanup(func() { _ = e.Destroy(context.Background(), rc) })
	return rc
}

// extAgentCalls runs extAgentScript in a container on the run's service network
// only, as an agent would sit, and returns what it printed.
func extAgentCalls(t *testing.T, runID string, args ...string) map[string]any {
	t.Helper()
	name := "hd-ext-agent-" + runID
	cmd := append([]string{"run", "--rm", "--name", name, "--network", serviceNetwork(runID), testImage, "python3", "-c", extAgentScript}, args...)
	out, err := exec.Command("docker", cmd...).Output()
	if err != nil {
		t.Fatalf("agent container: %v: %s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("agent output %s: %v", out, err)
	}
	return got
}

// extProxyImage builds the current bifrost-bench for linux and a throwaway
// image hd-ext-tp:<suffix> holding it over the stock tool proxy image's
// binary. It is removed at cleanup.
func extProxyImage(t *testing.T, suffix string) string {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "bifrost-bench"), "./cmd/bifrost-bench")
	build.Dir = ".."
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bifrost-bench: %v\n%s", err, out)
	}
	df := "FROM " + DefaultToolProxyImage + "\nCOPY bifrost-bench /usr/local/bin/bifrost-bench\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(df), 0o644); err != nil {
		t.Fatal(err)
	}
	tag := "hd-ext-tp:" + suffix
	extDocker(t, "build", "-q", "-t", tag, dir)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", tag).Run() })
	return tag
}

func extDocker(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func extRunFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func extRandHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
