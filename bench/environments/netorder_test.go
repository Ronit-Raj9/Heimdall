package environments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Under gVisor (runsc) a container only sees the networks it had when it
// started, so every `network connect` must come between `create` and
// `start`. These tests read the fake docker call log for that order.

// connectAfterStart returns the first `network connect` in all that names a
// container already started (by `start` or `run -d`), or "".
func connectAfterStart(all []string) string {
	started := map[string]bool{}
	for _, c := range all {
		f := strings.Fields(c)
		switch {
		case len(f) >= 2 && f[0] == "start":
			started[f[len(f)-1]] = true
		case len(f) >= 4 && f[0] == "run" && f[1] == "-d" && f[2] == "--name":
			started[f[3]] = true
		case len(f) >= 4 && f[0] == "network" && f[1] == "connect":
			if started[f[len(f)-1]] {
				return c
			}
		}
	}
	return ""
}

// indexWith returns the index of the first call that starts with prefix and
// contains every part, or -1.
func indexWith(all []string, prefix string, parts ...string) int {
	c := callWith(all, prefix, parts...)
	if c == "" {
		return -1
	}
	for i, x := range all {
		if x == c {
			return i
		}
	}
	return -1
}

// mixedServices is a plain, a proxied MCP and a hidden service.
func mixedServices() []schemas.ServiceSpec {
	return []schemas.ServiceSpec{
		{ID: "kv", Role: schemas.ServiceRoleData, Image: "kv:1", Port: 8080, Ready: &schemas.ReadyCheck{HTTP: "/healthz"}},
		{ID: "mcp1", Role: schemas.ServiceRoleMCP, Image: "mcp:1", Port: 9000, MCP: &schemas.MCPEndpoint{Path: "/rpc"}},
		{ID: "audit", Image: "audit:1", Hidden: true, Ready: &schemas.ReadyCheck{Argv: []string{"true"}}},
	}
}

func TestNoNetworkConnectAfterStart(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := serviceRC(t, mixedServices()...)
	if _, _, err := e.StartServices(t.Context(), rc); err != nil {
		t.Fatalf("start services: %v", err)
	}
	all := calls(t, logPath)
	if c := connectAfterStart(all); c != "" {
		t.Fatalf("network joined after start (gVisor never sees it): %s\nin:\n%s", c, strings.Join(all, "\n"))
	}
}

func TestProbeCreatedOnBothNetworks(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	rc := serviceRC(t, mixedServices()...)
	if _, _, err := e.StartServices(t.Context(), rc); err != nil {
		t.Fatalf("start services: %v", err)
	}
	all := calls(t, logPath)
	cr := indexWith(all, "create --name bench-r_1-svcprobe-", "--network bench-net-r_1", "--user 65534:65534", "--read-only", "--entrypoint sleep helper:1 infinity")
	if cr < 0 {
		t.Fatalf("probe not created on the service network:\n%s", strings.Join(all, "\n"))
	}
	probe := strings.Fields(all[cr])[2]
	conn := indexWith(all, "network connect bench-hnet-r_1 "+probe)
	start := indexWith(all, "start "+probe)
	if conn < 0 || start < 0 || cr >= conn || conn >= start {
		t.Fatalf("probe order create %d, connect %d, start %d:\n%s", cr, conn, start, strings.Join(all, "\n"))
	}
	if callWith(all, "run -d --name "+probe) != "" {
		t.Fatalf("probe still started with run -d")
	}
	if callWith(all, "exec "+probe) == "" {
		t.Fatalf("no ready check ran in the probe")
	}
}

func TestProbeFailureKeepsErrorText(t *testing.T) {
	docker, _ := fakeDocker(t, "create --name bench-r_1-svcprobe")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	_, _, err := e.StartServices(t.Context(), serviceRC(t, mixedServices()...))
	if err == nil || !strings.Contains(err.Error(), "start probe container") {
		t.Fatalf("error %v", err)
	}
	docker, logPath := fakeDocker(t, "network connect bench-hnet-r_1 bench-r_1-svcprobe")
	e = newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1"})
	_, _, err = e.StartServices(t.Context(), serviceRC(t, mixedServices()...))
	if err == nil || !strings.Contains(err.Error(), "connect probe container") {
		t.Fatalf("error %v", err)
	}
	if callWith(calls(t, logPath), "rm -f", "bench-r_1-svcprobe") == "" {
		t.Fatalf("probe not removed after a failed connect")
	}
}

// fakeMCPGateway serves MCP for the ServeMCP tests; only the MCP methods are
// used.
type fakeMCPGateway struct {
	schemas.Gateway
	ups []schemas.MCPUpstream
}

func (g *fakeMCPGateway) ServeMCP(_ context.Context, _ *schemas.RunContext, ups []schemas.MCPUpstream, _ bool) ([]schemas.BifrostMCPClient, *schemas.RunKey, error) {
	g.ups = ups
	out := make([]schemas.BifrostMCPClient, len(ups))
	for i, u := range ups {
		out[i] = schemas.BifrostMCPClient{Service: u.Service, Slug: "s" + u.Service, Upstream: u.URL}
	}
	return out, &schemas.RunKey{ID: "mk", Value: "mcp-key"}, nil
}

func (g *fakeMCPGateway) RemoveMCP(context.Context, string) error { return nil }

func (g *fakeMCPGateway) MCPCalls(context.Context, string, []schemas.BifrostMCPClient) ([]schemas.BifrostMCPCall, bool, string) {
	return nil, true, ""
}

func TestToolProxyJoinsEvalNetworkBeforeStart(t *testing.T) {
	docker, logPath := fakeDocker(t)
	gw := &fakeMCPGateway{}
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "heimdall-org-x",
		ServiceGateway: ServiceGatewayConfig{Gateway: gw, AgentURL: "http://bifrost:8080"}})
	rc := serviceRC(t, mixedServices()...)
	rc.Agent = &schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Bifrost: &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{}}}}
	handles, _, err := e.StartServices(t.Context(), rc)
	if err != nil {
		t.Fatalf("start services: %v", err)
	}
	all := calls(t, logPath)
	cr := indexWith(all, "create --name bench-tp-r_1")
	conn := indexWith(all, "network connect --alias tp-r_1 heimdall-org-x bench-tp-r_1")
	start := indexWith(all, "start bench-tp-r_1")
	if cr < 0 || conn < 0 || start < 0 || cr >= conn || conn >= start {
		t.Fatalf("tool proxy order create %d, connect %d, start %d:\n%s", cr, conn, start, strings.Join(all, "\n"))
	}
	if c := connectAfterStart(all); c != "" {
		t.Fatalf("network joined after start: %s", c)
	}
	rc.Services, rc.Key = handles, &schemas.RunKey{ID: "k", Value: "v"}
	before := len(all)
	ev, err := e.ServeMCP(t.Context(), rc, false)
	if err != nil {
		t.Fatalf("serve mcp: %v", err)
	}
	if len(ev.Clients) != 1 || len(gw.ups) != 1 || gw.ups[0].URL != "http://tp-r_1:9200/s/mcp1/rpc" {
		t.Fatalf("clients %+v ups %+v", ev.Clients, gw.ups)
	}
	if after := calls(t, logPath)[before:]; callWith(after, "network connect") != "" {
		t.Fatalf("ServeMCP joined the network again: %s", strings.Join(after, "\n"))
	}
	if err := e.RemoveMCP(t.Context(), rc); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.gatewayJoined.Load("r_1"); ok {
		t.Fatalf("RemoveMCP kept the joined mark")
	}
}

func TestToolProxyWithoutGatewayMCPStaysOffEvalNetwork(t *testing.T) {
	docker, logPath := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "heimdall-org-x"})
	if _, _, err := e.StartServices(t.Context(), serviceRC(t, mixedServices()...)); err != nil {
		t.Fatalf("start services: %v", err)
	}
	if c := callWith(calls(t, logPath), "network connect", "heimdall-org-x"); c != "" {
		t.Fatalf("tool proxy joined the evaluation network without gateway MCP: %s", c)
	}
}

// netWatchDocker wraps fakeDocker: the network observer prints its ready
// line, everything else goes to the base fake.
func netWatchDocker(t *testing.T) (utils.Docker, string) {
	t.Helper()
	base, logPath := fakeDocker(t)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  "run --name bench-r_1-watch"*) %[1]q "$@"; echo '{"t":"ready"}'; sleep 0.3; exit 0 ;;
esac
exec %[1]q "$@"
`, base.Binary)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func TestNetWatchHolderJoinsServiceNetworkBeforeStart(t *testing.T) {
	docker, logPath := netWatchDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "evalnet", NetWatch: true})
	rc := &schemas.RunContext{
		Run:          &schemas.Run{RunID: "r_1"},
		Suite:        &schemas.Suite{},
		Task:         &schemas.Task{ID: "t"},
		Handle:       &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"},
		Dir:          t.TempDir(),
		Services:     []schemas.ServiceHandle{{Spec: schemas.ServiceSpec{ID: "kv"}}},
		FaultRuntime: &schemas.AdapterFaultRuntime{Network: "bench-fnet-r_1"},
	}
	if _, err := e.Exec(t.Context(), rc, &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	all := calls(t, logPath)
	cr := indexWith(all, "create --name bench-r_1-net-", "--network evalnet", "--entrypoint sleep helper:1 infinity")
	if cr < 0 {
		t.Fatalf("holder not created on the evaluation network:\n%s", strings.Join(all, "\n"))
	}
	holder := strings.Fields(all[cr])[2]
	svc := indexWith(all, "network connect bench-net-r_1 "+holder)
	fault := indexWith(all, "network connect bench-fnet-r_1 "+holder)
	start := indexWith(all, "start "+holder)
	if svc < 0 || fault < 0 || start < 0 || cr >= svc || svc >= start || fault >= start {
		t.Fatalf("holder order create %d, service %d, fault %d, start %d:\n%s", cr, svc, fault, start, strings.Join(all, "\n"))
	}
	if c := connectAfterStart(all); c != "" {
		t.Fatalf("network joined after start: %s\nin:\n%s", c, strings.Join(all, "\n"))
	}
	if callWith(all, "create --name bench-r_1 ", "--network container:"+holder) == "" {
		t.Fatalf("agent not in the holder's namespace:\n%s", strings.Join(all, "\n"))
	}
}
