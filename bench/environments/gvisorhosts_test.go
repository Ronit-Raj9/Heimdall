package environments

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
