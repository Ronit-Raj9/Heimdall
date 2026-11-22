package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/utils"
)

type ibFakeDocker struct {
	mu    sync.Mutex
	calls [][]string
	cps   map[string]string // container path -> content of cp into the proxy
	ps    string
}

func (f *ibFakeDocker) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch args[0] {
	case "cp":
		b, _ := os.ReadFile(args[1])
		if f.cps == nil {
			f.cps = map[string]string{}
		}
		f.cps[args[2]] = string(b)
	case "ps", "network", "image":
		if args[0] == "ps" || (len(args) > 1 && args[1] == "ls") {
			return []byte(f.ps), nil
		}
	case "create":
		return []byte("id1\n"), nil
	}
	return nil, nil
}

func (f *ibFakeDocker) find(prefix ...string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return c
		}
	}
	return nil
}

func ibTestRequest() issuebench.WorkRequest {
	return issuebench.WorkRequest{Mode: issuebench.WorkModeBuild, OrgID: "org_a", JobID: "job_ABC/123456789xyz", Repo: "acme/tiny",
		EgressHosts: []string{"pypi.org", "example.org"}}
}

func TestIssuebenchEgressSandboxArgv(t *testing.T) {
	f := &ibFakeDocker{}
	ctx := context.Background()
	fac, cleanup, err := ibEgressSandboxWith(ctx, ibEgressOptions{Public: "pubnet", Image: "bench/egress:1", Runtime: "runsc", Runner: "r1"}, ibTestRequest(), f.run)
	if err != nil {
		t.Fatal(err)
	}
	if fac == nil {
		t.Fatal("no factory")
	}
	net := "heimdall-ib-job_abc-1234"
	nc := strings.Join(f.find("network", "create"), " ")
	for _, w := range []string{"--internal", "--label heimdall=1", "--label heimdall.org=org_a", "--label heimdall.issuebench=egress", "--label heimdall.issuebench.job=job_ABC/123456789xyz", "--label heimdall.runner=r1"} {
		if !strings.Contains(nc, w) {
			t.Fatalf("network create lacks %q: %s", w, nc)
		}
	}
	if !strings.HasSuffix(nc, " "+net) {
		t.Fatalf("network name: %s", nc)
	}
	pc := strings.Join(f.find("create"), " ")
	for _, w := range []string{"--name " + net + "-egress", "--network " + net, "--network-alias egress", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges", "--memory 256m", "--pids-limit 128",
		"-e HEIMDALL_EGRESS_MODE=live-allowlisted", "-e HEIMDALL_EGRESS_ALLOW=pypi.org,example.org", "--runtime runsc", "bench/egress:1 -ca /egress/ca.pem -log "} {
		if !strings.Contains(pc, w) {
			t.Fatalf("proxy create lacks %q: %s", w, pc)
		}
	}
	ca := f.cps[net+"-egress:"+egress.ServiceCAPath]
	if !strings.Contains(ca, "BEGIN CERTIFICATE") || !strings.Contains(ca, "PRIVATE KEY") {
		t.Fatalf("proxy CA not copied: %q", ca)
	}
	if c := f.find("network", "connect"); strings.Join(c, " ") != "network connect pubnet "+net+"-egress" {
		t.Fatalf("connect %v", c)
	}
	// order: network create, create, cp, connect, start
	var order []string
	for _, c := range f.calls {
		order = append(order, c[0])
	}
	if strings.Join(order, ",") != "network,create,cp,network,start" {
		t.Fatalf("order %v", order)
	}

	// The build sandbox: on the job network, proxy env, CA files, build label.
	f.calls = nil
	sb, err := fac(ctx, "python:3.12-slim", t.TempDir(), []string{"UV_EXCLUDE_NEWER=x"})
	if err != nil {
		t.Fatal(err)
	}
	var bc string
	for _, c := range f.calls {
		if c[0] == "create" && strings.Contains(strings.Join(c, " "), "sleep infinity") && !strings.Contains(strings.Join(c, " "), "--network none") {
			bc = strings.Join(c, " ")
		}
	}
	for _, w := range []string{"--network " + net, "--runtime runsc", "-e HTTPS_PROXY=http://egress:3128", "-e SSL_CERT_FILE=" + egress.BundlePath, "--label heimdall.issuebench=build", "--label heimdall.runner=r1", "--label heimdall.org=org_a"} {
		if !strings.Contains(bc, w) {
			t.Fatalf("build create lacks %q: %s", w, bc)
		}
	}
	if strings.Contains(bc, "PRIVATE KEY") {
		t.Fatal("key in argv")
	}
	var filesCp bool
	for _, c := range f.calls {
		if c[0] == "cp" && strings.HasSuffix(c[2], ":/") && filepath.Base(c[1]) == "etc" {
			filesCp = true
		}
	}
	if !filesCp {
		t.Fatalf("CA files not copied into the build container: %v", f.calls)
	}
	sb.Close(ctx)
	cleanup()
	if c := f.find("rm", "-f", "-v", net+"-egress"); c == nil {
		t.Fatal("cleanup did not remove the proxy")
	}
	if c := f.find("network", "rm", net); c == nil {
		t.Fatal("cleanup did not remove the network")
	}
}

func TestIssuebenchEgressDefaultHosts(t *testing.T) {
	f := &ibFakeDocker{}
	req := ibTestRequest()
	req.EgressHosts = nil
	_, cleanup, err := ibEgressSandboxWith(context.Background(), ibEgressOptions{Runner: "r1"}, req, f.run)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	pc := strings.Join(f.find("create"), " ")
	if !strings.Contains(pc, "-e HEIMDALL_EGRESS_ALLOW="+strings.Join(issuebench.DefaultEgressHosts, ",")) || !strings.Contains(pc, " "+egress.DefaultImage+" ") {
		t.Fatalf("defaults: %s", pc)
	}
	if c := f.find("network", "connect"); c[2] != "bridge" {
		t.Fatalf("public default: %v", c)
	}
}

func TestIssuebenchEgressCleanupAfterCancel(t *testing.T) {
	f := &ibFakeDocker{ps: "c1 c2\n"}
	ctx, cancel := context.WithCancel(context.Background())
	_, cleanup, err := ibEgressSandboxWith(ctx, ibEgressOptions{Runner: "r1"}, ibTestRequest(), f.run)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	f.calls = nil
	cleanup()
	got := map[string]bool{}
	for _, c := range f.calls {
		got[strings.Join(c, " ")] = true
	}
	for _, w := range []string{"ps -aq --filter label=heimdall.issuebench.job=job_ABC/123456789xyz", "rm -f -v c1 c2", "rm -f -v heimdall-ib-job_abc-1234-egress", "network rm heimdall-ib-job_abc-1234"} {
		if !got[w] {
			t.Fatalf("cleanup after cancel lacks %q: %v", w, f.calls)
		}
	}
}

func TestIssuebenchEgressFailureCleansUp(t *testing.T) {
	f := &ibFakeDocker{}
	run := func(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
		if args[0] == "start" {
			f.run(ctx, stdin, args...)
			return nil, &issuebench.ExitError{Code: 1, Output: []byte("boom")}
		}
		return f.run(ctx, stdin, args...)
	}
	if _, _, err := ibEgressSandboxWith(context.Background(), ibEgressOptions{Runner: "r1"}, ibTestRequest(), run); err == nil || !strings.Contains(err.Error(), "start the proxy") {
		t.Fatalf("err %v", err)
	}
	if f.find("network", "rm") == nil {
		t.Fatal("failed start left the network")
	}
}

func TestIssuebenchSweep(t *testing.T) {
	f := &ibFakeDocker{ps: "a b\n"}
	n, err := sweepIssuebenchWith(context.Background(), f.run, "r1")
	if err != nil || n != 6 {
		t.Fatalf("n %d err %v", n, err)
	}
	ls := strings.Join(f.find("ps"), " ")
	if !strings.Contains(ls, "--filter label=heimdall.issuebench --filter label=heimdall.runner=r1") {
		t.Fatalf("filters: %s", ls)
	}
	var order []string
	for _, c := range f.calls {
		order = append(order, strings.Join(c[:2], " "))
	}
	want := "ps -aq,rm -f,rm -f,network ls,network rm,network rm,image ls,rmi -f,rmi -f"
	if strings.Join(order, ",") != want {
		t.Fatalf("order %v", order)
	}
	if _, err := sweepIssuebenchWith(context.Background(), f.run, ""); err == nil {
		t.Fatal("empty runner must refuse")
	}
}

// A real utils.Docker with a policy: every call reaches the docker binary
// through the policy (the fake binary logs what it really ran), and a
// build, which the policy refuses without a builder, never runs.
func TestIssuebenchSandboxCallsGoThroughPolicy(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\necho \"$*\" >> " + logf + "\ncase \"$1\" in create) echo cid;; commit) echo sha256:x;; run) echo heimdall-snapshot-ok;; esac\nexit 0\n"
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := utils.Docker{Binary: bin, Policy: &utils.DockerPolicy{OrgID: "org_a", Runtime: "runsc-test", TagPrefix: utils.OrgImagePrefix("org_a")}}
	ctx := context.Background()
	fac, cleanup, err := ibEgressSandbox(ctx, ibEgressOptions{Docker: d, Runner: "r1"}, ibTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	sb, err := fac(ctx, "python:3.12-slim", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sb.Exec(ctx, "true", time.Minute)
	sb.Snapshot(ctx)
	sb.Close(ctx)
	cleanup()
	b, _ := os.ReadFile(logf)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	creates := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "create ") || strings.HasPrefix(l, "run ") {
			creates++
			if !strings.Contains(l, "--runtime runsc-test") {
				t.Fatalf("container started outside the policy: %s", l)
			}
		}
	}
	if creates < 3 {
		t.Fatalf("expected proxy, check and build containers, got %d: %v", creates, lines)
	}
	// The same DockerFunc refuses a build the policy cannot place.
	out, err := issuebench.ExecDocker(d.Command)(ctx, nil, "build", "-t", "x", dir)
	if err == nil {
		t.Fatalf("policy let an org build run: %s", out)
	}
	b2, _ := os.ReadFile(logf)
	if strings.Contains("\n"+string(b2), "\nbuild ") {
		t.Fatal("refused build reached the docker binary")
	}
}

// TestIssuebenchEgressRealDocker runs the proxy and a build sandbox on the
// local daemon: the sandbox reaches an allowed host only through the proxy
// and nothing else. It needs the egress image (`egress image`) and network
// access; it removes everything it creates.
func TestIssuebenchEgressRealDocker(t *testing.T) {
	if os.Getenv("HEIMDALL_TEST_REAL_DOCKER") == "" {
		t.Skip("set HEIMDALL_TEST_REAL_DOCKER=1 for the real Docker egress test")
	}
	img := os.Getenv("HEIMDALL_TEST_IMAGE")
	if img == "" {
		img = "python:3.12-slim"
	}
	d := utils.Docker{Binary: "docker"}
	ctx := context.Background()
	if _, ok, _ := d.ImageID(ctx, egress.DefaultImage); !ok {
		t.Skip("no " + egress.DefaultImage + " image; run `bifrost-bench egress image` with a static binary")
	}
	req := ibTestRequest()
	req.JobID = fmt.Sprintf("w04real%d", os.Getpid())
	req.EgressHosts = []string{"pypi.org"}
	fac, cleanup, err := ibEgressSandbox(ctx, ibEgressOptions{Docker: d, Runner: "w04-real-test"}, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup()
		if n, err := sweepIssuebench(ctx, d, "w04-real-test"); err != nil || n != 0 {
			t.Errorf("sweep after cleanup found %d objects (%v)", n, err)
		}
	}()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hi\n"), 0o644)
	sb, err := fac(ctx, img, repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(ctx)
	py := `import urllib.request,sys
def get(u):
    try:
        return urllib.request.urlopen(u, timeout=20).status
    except Exception as e:
        return type(e).__name__+":"+str(e)[:80]
print("allowed", get("https://pypi.org/simple/pip/"))
print("denied", get("https://example.com/"))
`
	code, out, err := sb.Exec(ctx, "cat hello.txt; python3 -c '"+py+"'; env -u HTTPS_PROXY -u https_proxy python3 -c 'import socket; socket.setdefaulttimeout(5)\ntry:\n  socket.create_connection((\"151.101.0.223\",443)); print(\"direct open\")\nexcept Exception as e: print(\"direct blocked\")'", 2*time.Minute)
	t.Logf("exit %d err %v\n%s", code, err, out)
	if err != nil || !strings.Contains(out, "hi\n") || !strings.Contains(out, "allowed 200") || strings.Contains(out, "denied 200") || !strings.Contains(out, "direct blocked") {
		t.Fatalf("egress confinement wrong:\n%s", out)
	}
}
