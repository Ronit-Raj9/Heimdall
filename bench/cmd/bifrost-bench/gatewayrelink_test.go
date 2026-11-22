package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// relinkDocker is a docker stand-in whose `inspect` prints nets (the
// gateway's networks as JSON) and that logs every call to <dir>/calls.
func relinkDocker(t *testing.T, nets string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + dir + "/calls\ncase \"$1\" in\ninspect) echo '" + nets + "' ;;\nesac\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, dir
}

func TestRelinkGateway(t *testing.T) {
	ctx := context.Background()
	const network = "heimdall-evalnet-org_1"

	d, dir := relinkDocker(t, `{"heimdall-evalnet":{},"heimdall_upstream":{}}`)
	linked, err := relinkGateway(ctx, d, network, "gw1", "bifrost")
	if err != nil || !linked {
		t.Fatalf("gateway off the org network: linked %v, err %v; want a new link", linked, err)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(calls), "network connect --alias bifrost "+network+" gw1") {
		t.Fatalf("calls = %q; want a connect with the alias", calls)
	}

	d, dir = relinkDocker(t, `{"heimdall-evalnet":{},"`+network+`":{}}`)
	linked, err = relinkGateway(ctx, d, network, "gw1", "bifrost")
	if err != nil || linked {
		t.Fatalf("gateway already on the org network: linked %v, err %v; want nothing done", linked, err)
	}
	calls, _ = os.ReadFile(filepath.Join(dir, "calls"))
	if strings.Contains(string(calls), "network connect") {
		t.Fatalf("calls = %q; want no connect", calls)
	}
}
