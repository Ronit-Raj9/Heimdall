//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDocker writes a docker CLI stand-in that logs every call (one line
// of arguments) to <dir>/calls and answers what a build session asks.
// FAKE_GUARD=down makes the guard never get ready; the build exits 3 and
// records its environment and docker config.
func fakeDocker(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "docker")
	script := `#!/bin/sh
echo "$*" >> "` + dir + `/calls"
case "$1 $2" in
"logs "*) [ "${FAKE_GUARD:-}" = down ] || echo "heimdall build egress ready: registry 172.30.0.0/16:5000" ;;
"inspect "*) if [ "${FAKE_GUARD:-}" = down ]; then echo false; else echo true; fi ;;
"ps -aq") echo "c1"; echo "c2" ;;
"buildx build")
	echo "builder=$BUILDX_BUILDER" > "` + dir + `/build-env"
	cat "$DOCKER_CONFIG/config.json" > "` + dir + `/build-config"
	echo "building"
	exit 3 ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func calls(t *testing.T, dir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func callWith(cs []string, prefix string) string {
	for _, c := range cs {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

func TestBuildSession(t *testing.T) {
	bin, dir := fakeDocker(t)
	var out bytes.Buffer
	c := &buildSessionConfig{Org: "org_a", Runner: "shared-x", LockDir: filepath.Join(dir, "locks"), Docker: bin,
		Network: "heimdall-buildnet", BuildkitImage: "moby/buildkit@sha256:1", GuardImage: "heimdall/runner:local",
		AppArmor: "heimdall-buildkitd", BuildkitConfig: "/srv/bench/deploy/hosted/buildkitd.toml",
		Registry: "registry:5000", Credentials: &utils.RegistryCredentials{User: "heimdall-pull", Password: "pw"},
		Stdout: &out, Stderr: &out, newID: func() string { return "s1" }}
	code, err := c.run(context.Background(), []string{"buildx", "build", "--load", "-t", "org-org-a/x:1", "/ctx"})
	if err != nil || code != 3 {
		t.Fatalf("build exit code not passed through: %d %v", code, err)
	}
	cs := calls(t, dir)
	guard := callWith(cs, "run -d --rm --name heimdall-build-org_a-s1-guard")
	for _, want := range []string{"--network heimdall-buildnet", "--cap-drop ALL --cap-add NET_ADMIN", "--read-only",
		"--label heimdall.build-runner=shared-x", "--entrypoint /usr/local/bin/heimdall-build-egress heimdall/runner:local"} {
		if !strings.Contains(guard, want) {
			t.Errorf("guard %q lacks %q", guard, want)
		}
	}
	bk := callWith(cs, "run -d --rm --name heimdall-build-org_a-s1-buildkit")
	for _, want := range []string{"--network container:heimdall-build-org_a-s1-guard", "apparmor=heimdall-buildkitd",
		"-v heimdall-buildcache-org_a:/home/user/.local/share/buildkit", "/srv/bench/deploy/hosted/buildkitd.toml:/home/user/.config/buildkit/buildkitd.toml:ro",
		"moby/buildkit@sha256:1 --addr tcp://0.0.0.0:1234"} {
		if !strings.Contains(bk, want) {
			t.Errorf("buildkit %q lacks %q", bk, want)
		}
	}
	// The guard is ready before BuildKit starts, BuildKit before the build,
	// and both are removed afterwards (BuildKit first: it lives in the
	// guard's network namespace).
	order := []string{"volume create", "run -d --rm --name heimdall-build-org_a-s1-guard", "logs heimdall-build-org_a-s1-guard",
		"run -d --rm --name heimdall-build-org_a-s1-buildkit", "buildx create --name heimdall-build-org_a-s1 --driver remote tcp://heimdall-build-org_a-s1-guard:1234",
		"buildx inspect --bootstrap", "buildx build", "buildx rm -f heimdall-build-org_a-s1", "rm -f heimdall-build-org_a-s1-buildkit", "rm -f heimdall-build-org_a-s1-guard"}
	i := 0
	for _, c := range cs {
		if i < len(order) && strings.HasPrefix(c, order[i]) {
			i++
		}
	}
	if i != len(order) {
		t.Fatalf("session steps out of order at %q:\n%s", order[i], strings.Join(cs, "\n"))
	}
	env, _ := os.ReadFile(filepath.Join(dir, "build-env"))
	if strings.TrimSpace(string(env)) != "builder=heimdall-build-org_a-s1" {
		t.Errorf("build env %q", env)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "build-config"))
	if !strings.Contains(string(cfg), `"registry:5000"`) || !strings.Contains(string(cfg), base64.StdEncoding.EncodeToString([]byte("heimdall-pull:pw"))) {
		t.Errorf("session docker config lacks the pull credentials: %s", cfg)
	}
	if !strings.Contains(out.String(), "building") {
		t.Errorf("build output not passed through: %q", out.String())
	}
}

// A guard that does not get ready stops the session before BuildKit
// starts: no build runs without its egress rules. Cleanup still runs.
func TestBuildSessionGuardNotReady(t *testing.T) {
	bin, dir := fakeDocker(t)
	t.Setenv("FAKE_GUARD", "down")
	c := &buildSessionConfig{Org: "org_a", Runner: "r", LockDir: dir, Docker: bin, Network: "n", BuildkitImage: "bk", GuardImage: "g",
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, newID: func() string { return "s2" }}
	code, err := c.run(context.Background(), []string{"buildx", "build", "/ctx"})
	if err == nil || code == 0 || !strings.Contains(err.Error(), "egress rules") {
		t.Fatalf("session without egress rules: %d %v", code, err)
	}
	cs := calls(t, dir)
	if callWith(cs, "run -d --rm --name heimdall-build-org_a-s2-buildkit") != "" || callWith(cs, "buildx build") != "" {
		t.Fatalf("BuildKit or the build started without the guard:\n%s", strings.Join(cs, "\n"))
	}
	if callWith(cs, "rm -f heimdall-build-org_a-s2-guard") == "" {
		t.Fatal("guard not removed")
	}
}

// One build at a time per org: a second lock of the same org waits; another
// org's lock does not.
func TestLockOrgBuild(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockOrgBuild(context.Background(), filepath.Join(dir, "org_a.lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if _, err := lockOrgBuild(ctx, filepath.Join(dir, "org_a.lock")); err == nil {
		t.Fatal("second build of the same org did not wait")
	}
	other, err := lockOrgBuild(context.Background(), filepath.Join(dir, "org_b.lock"))
	if err != nil {
		t.Fatalf("another org waited: %v", err)
	}
	other()
	unlock()
	again, err := lockOrgBuild(context.Background(), filepath.Join(dir, "org_a.lock"))
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	again()
}

func TestSweepBuildSessions(t *testing.T) {
	bin, dir := fakeDocker(t)
	n, err := sweepBuildSessions(context.Background(), utils.Docker{Binary: bin}, "shared-x")
	if err != nil || n != 2 {
		t.Fatalf("sweep %d %v", n, err)
	}
	cs := calls(t, dir)
	if !strings.Contains(cs[0], "label=heimdall.build-runner=shared-x") || cs[1] != "rm -f c1 c2" {
		t.Fatalf("sweep calls %v", cs)
	}
}

func TestBuildSessionCommandLine(t *testing.T) {
	argv := buildSessionArgv("/usr/local/bin/bifrost-bench", "org_a", "shared-x", "/var/lib/r/build-locks")
	if strings.Join(argv, " ") != "/usr/local/bin/bifrost-bench runner build-session -org org_a -runner shared-x -lock-dir /var/lib/r/build-locks" {
		t.Fatalf("argv %v", argv)
	}
	for _, bad := range [][]string{
		{"-org", "org_a", "-runner", "r", "-lock-dir", "d", "--", "run", "img"},
		{"-org", "../x", "-runner", "r", "-lock-dir", "d", "--", "buildx", "build", "."},
		{"-org", "org_a", "--", "buildx", "build", "."},
	} {
		if code := runBuildSession(bad); code != exitUsage {
			t.Errorf("%v: exit %d", bad, code)
		}
	}
}
