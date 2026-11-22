package utils

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func publicOnly(t *testing.T) {
	t.Helper()
	EgressPublicOnly = true
	t.Cleanup(func() { EgressPublicOnly = false })
}

func TestRemoteEgressPublicOnly(t *testing.T) {
	ctx := context.Background()
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Dir: t.TempDir()}
	d, log := fakeDocker(t, "")
	// Off (an org's own worker): private destinations start, no guard env.
	if _, err := StartRemoteEgress(ctx, d, rc, &schemas.RunPlan{Image: "img", Egress: []string{"10.0.0.5:8080"}}, "eval"); err != nil {
		t.Fatalf("own worker private destination: %v", err)
	}
	if got := calls(t, log); strings.Contains(got[1], "-e BENCH_EGRESS_PUBLIC_ONLY=1") {
		t.Fatalf("guard env set with the guard off: %s", got[1])
	}
	publicOnly(t)
	for _, target := range []string{"10.0.0.5:8080", "127.0.0.1:80", "169.254.169.254:80", "bifrost:8080", "localhost:443", "[::1]:443", "100.64.1.1:443"} {
		if _, err := StartRemoteEgress(ctx, d, rc, &schemas.RunPlan{Image: "img", Egress: []string{target}}, "eval"); err == nil ||
			!strings.Contains(err.Error(), "shared worker") {
			t.Errorf("%s: %v", target, err)
		}
	}
	d, log = fakeDocker(t, "")
	if _, err := StartRemoteEgress(ctx, d, rc, &schemas.RunPlan{Image: "img", Egress: []string{"agent.example.com:443"}}, "eval"); err != nil {
		t.Fatal(err)
	}
	if got := calls(t, log); !strings.Contains(got[1], "-e BENCH_EGRESS_PUBLIC_ONLY=1") || !strings.Contains(got[1], "--entrypoint python3 img -I -c") {
		t.Fatalf("create call: %s", got[1])
	}
}

func TestExternalEgressPublicOnly(t *testing.T) {
	publicOnly(t)
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Dir: t.TempDir()}
	d, _ := fakeDocker(t, "")
	if _, err := StartExternalEgress(context.Background(), d, rc, "img", "eval", map[string]string{"crm": "registry:5000"}); err == nil ||
		!strings.Contains(err.Error(), "shared worker") {
		t.Fatalf("compose name accepted: %v", err)
	}
}

// The forwarder's address check, run in python3 when it is installed.
func TestForwarderPublicCheck(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	src := remoteForwarder[:strings.Index(remoteForwarder, "srv = socket.create_server")]
	src += `
import sys
bad = ["127.0.0.1", "10.1.2.3", "172.17.0.1", "192.168.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00:ec2::254", "fe80::1%eth0", "::ffff:10.0.0.1", "64:ff9b::a00:1", "0.0.0.0", "224.0.0.1"]
good = ["1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"]
wrong = [a for a in bad if public(a)] + [a for a in good if not public(a)]
if wrong:
    print(wrong); sys.exit(1)
import ast; ast.parse(open(sys.argv[1]).read()) if len(sys.argv) > 1 else None
`
	cmd := exec.Command(py, "-I", "-c", src)
	cmd.Env = []string{"BENCH_EGRESS_TARGET=example.com:443", "BENCH_EGRESS_PUBLIC_ONLY=1"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("public(): %v %s", err, out)
	}
	cmd = exec.Command(py, "-I", "-c", "import ast,sys; ast.parse(sys.stdin.read())")
	cmd.Stdin = strings.NewReader(remoteForwarder)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("forwarder does not parse: %v %s", err, out)
	}
}
