package environments

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// gateDocker is a stand-in docker binary that logs each call and answers
// `docker port` with a loopback binding.
func gateDocker(t *testing.T) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
case "$1" in
  port) echo "127.0.0.1:41234"; echo "[::1]:41234" ;;
esac
exit 0
`, logPath)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func TestStartRemoteTools(t *testing.T) {
	docker, logPath := gateDocker(t)
	var probed string
	old := remoteGateReady
	remoteGateReady = func(_ context.Context, addr string, _ time.Duration) error { probed = addr; return nil }
	t.Cleanup(func() { remoteGateReady = old })

	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, RemoteToolsPublicURL: "https://tools.example.com:{port}"})
	rc := serviceRC(t)
	rc.Agent = &schemas.AgentConfig{Budgets: schemas.Budgets{WallS: 60}, Remote: &schemas.RemoteAgent{URL: "https://a", Tools: schemas.RemoteToolsExpose}}
	handles := []schemas.ServiceHandle{
		{Spec: schemas.ServiceSpec{ID: "notes", Role: schemas.ServiceRoleMCP}, URL: toolProxyURL("notes")},
		{Spec: schemas.ServiceSpec{ID: "secret", Hidden: true}},
	}
	if err := e.startRemoteTools(context.Background(), rc, "e_1", "r_1", handles); err != nil {
		t.Fatal(err)
	}
	a := rc.RemoteTools
	if a == nil || a.BaseURL != "https://tools.example.com:41234" || len(a.Token) != 64 || strings.Join(a.Services, ",") != "notes" || probed != "127.0.0.1:41234" {
		t.Fatalf("access %+v probed %q", a, probed)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	for _, want := range []string{
		"network create --label", "-p 127.0.0.1::9201", "--user 65534 --cap-drop ALL", "--read-only",
		"toolproxy gate -listen :9201 -upstream http://toolproxy:9200 -run r_1 -until",
		"network connect bench-net-r_1 bench-rt-r_1", "start bench-rt-r_1", "port bench-rt-r_1 9201/tcp",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("no docker call with %q in:\n%s", want, log)
		}
	}
	if strings.Contains(log, a.Token) {
		t.Fatal("the token reached a docker argument")
	}
	var rec schemas.RemoteToolsRecord
	b, err := os.ReadFile(filepath.Join(rc.Dir, schemas.RemoteToolsFile))
	if err != nil || json.Unmarshal(b, &rec) != nil || rec.BaseURL != a.BaseURL || rec.Listen != "127.0.0.1:41234" {
		t.Fatalf("record %s %v", b, err)
	}
	if strings.Contains(string(b), a.Token) {
		t.Fatal("the token is in the run folder")
	}

	// Not exposed, or nothing to publish: no gate.
	rc2 := serviceRC(t)
	rc2.Agent = &schemas.AgentConfig{Remote: &schemas.RemoteAgent{URL: "https://a"}}
	if err := e.startRemoteTools(context.Background(), rc2, "e_1", "r_2", handles); err != nil || rc2.RemoteTools != nil {
		t.Fatalf("not exposed: %v %+v", err, rc2.RemoteTools)
	}
	rc2.Agent.Remote.Tools = schemas.RemoteToolsExpose
	if err := e.startRemoteTools(context.Background(), rc2, "e_1", "r_2", handles[1:]); err != nil || rc2.RemoteTools != nil {
		t.Fatalf("nothing to publish: %v %+v", err, rc2.RemoteTools)
	}
}

func TestPublishedPort(t *testing.T) {
	if p, err := publishedPort("0.0.0.0:5000\n127.0.0.1:6000\n", "127.0.0.1"); err != nil || p != 6000 {
		t.Fatalf("%d %v", p, err)
	}
	if p, err := publishedPort("[::]:7000\n", "127.0.0.1"); err != nil || p != 7000 {
		t.Fatalf("%d %v", p, err)
	}
	if _, err := publishedPort("", "127.0.0.1"); err == nil {
		t.Fatal("empty output accepted")
	}
}
