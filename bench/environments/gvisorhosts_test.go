package environments

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestWithHosts(t *testing.T) {
	got := withHosts([]string{"create", "--name", "a", "img"}, []string{"bifrost:10.0.0.2", "toolproxy:10.0.1.3"})
	want := []string{"create", "--add-host", "bifrost:10.0.0.2", "--add-host", "toolproxy:10.0.1.3", "--name", "a", "img"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := withHosts([]string{"create", "img"}, nil); !reflect.DeepEqual(got, []string{"create", "img"}) {
		t.Fatalf("no hosts changed the command: %v", got)
	}
}

func TestSharedAddr(t *testing.T) {
	proxy := map[string]string{"bench-hnet-r_1": "10.9.0.2", "bench-net-r_1": "10.8.0.2"}
	probe := map[string]string{"bench-net-r_1": "10.8.0.5"}
	if got := sharedAddr(proxy, probe); got != "10.8.0.2" {
		t.Fatalf("got %q; want the address on the network the probe shares", got)
	}
	if got := sharedAddr(proxy, map[string]string{"other": "1.2.3.4"}); got != "" {
		t.Fatalf("got %q; want none", got)
	}
}

// TestSandboxHosts: under gVisor the agent's create line gets every DNS name
// of the containers on its networks; under runc, and on a container:
// network, it is unchanged.
func TestSandboxHosts(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/sh
case "$1" in
network) echo "id1 id2" ;;
inspect)
	echo '{"IPAddress":"10.0.0.2","Aliases":["bifrost"],"DNSNames":["heimdall-bifrost-1","bifrost","id1"]}'
	echo '{"IPAddress":"","DNSNames":["gone"]}' ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	args := []string{"create", "--name", "a", "img"}
	gv := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: bin, Policy: &utils.DockerPolicy{Runtime: "runsc"}}})
	got := gv.sandboxHosts(ctx, args, "heimdall-evalnet-org_1", "")
	want := []string{"create", "--add-host", "heimdall-bifrost-1:10.0.0.2", "--add-host", "bifrost:10.0.0.2", "--add-host", "id1:10.0.0.2", "--name", "a", "img"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gVisor: got %v, want %v", got, want)
	}
	if got := gv.sandboxHosts(ctx, args, "container:holder"); !reflect.DeepEqual(got, args) {
		t.Fatalf("container network: got %v, want unchanged", got)
	}
	runc := newDockerWorkspace(DockerWorkspaceConfig{Docker: utils.Docker{Binary: bin, Policy: &utils.DockerPolicy{Runtime: "runc"}}})
	if got := runc.sandboxHosts(ctx, args, "heimdall-evalnet-org_1"); !reflect.DeepEqual(got, args) {
		t.Fatalf("runc: got %v, want unchanged", got)
	}
}

// TestNetWatchHolderTakesHostsUnderGVisor: with the network observer on, the
// agent runs in the holder's namespace (container: network) and shares its
// /etc/hosts, so under gVisor the holder's create line carries the names on
// all its networks; the agent's own create line carries none.
func TestNetWatchHolderTakesHostsUnderGVisor(t *testing.T) {
	base, logPath := netWatchDocker(t)
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
case "$*" in
  "network inspect --format"*) %[1]q "$@" >/dev/null; echo "id1"; exit 0 ;;
  "inspect --format {{json (index"*) %[1]q "$@" >/dev/null; echo '{"IPAddress":"10.0.0.2","DNSNames":["bifrost"]}'; exit 0 ;;
  "run --runtime runsc --name bench-r_1-watch"*) %[1]q "$@"; echo '{"t":"ready"}'; sleep 0.3; exit 0 ;;
esac
exec %[1]q "$@"
`, base.Binary)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	docker := utils.Docker{Binary: bin, Policy: &utils.DockerPolicy{Runtime: "runsc"}}
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
	if callWith(all, "create --runtime runsc --add-host bifrost:10.0.0.2 --name bench-r_1-net-", "--network evalnet") == "" {
		t.Fatalf("holder created without the gVisor hosts:\n%s", strings.Join(all, "\n"))
	}
	agent := callWith(all, "create", "--name bench-r_1 ", "--network container:")
	if agent == "" || strings.Contains(agent, "--add-host") {
		t.Fatalf("agent create line %q; want the holder's namespace and no hosts of its own", agent)
	}
}
