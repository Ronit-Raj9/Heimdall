package utils

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrgImagePrefix(t *testing.T) {
	if got := OrgImagePrefix("org_ab12"); got != "org-org-ab12/" {
		t.Fatalf("prefix %q", got)
	}
}

func TestPolicyNil(t *testing.T) {
	var p *DockerPolicy
	in := []string{"build", "-t", "x", "."}
	if out, err := p.Rewrite(in); err != nil || strings.Join(out, " ") != strings.Join(in, " ") {
		t.Fatalf("nil policy changed args: %v %v", out, err)
	}
}

func TestPolicyRuntime(t *testing.T) {
	p := &DockerPolicy{Runtime: "runsc", TagPrefix: "org-a/"}
	for _, in := range [][]string{{"run", "--rm", "img"}, {"create", "--name", "n", "img"}, {"container", "run", "img"}} {
		out, err := p.Rewrite(in)
		if err != nil || !hasFlag(out, "--runtime") || out[len(out)-1] != "img" {
			t.Fatalf("%v -> %v %v", in, out, err)
		}
	}
	out, _ := p.Rewrite([]string{"network", "create", "--internal", "n"})
	if hasFlag(out, "--runtime") {
		t.Fatal("runtime on network create")
	}
	out, _ = p.Rewrite([]string{"run", "--runtime", "runc", "img"})
	if strings.Count(strings.Join(out, " "), "--runtime") != 1 {
		t.Fatalf("runtime doubled: %v", out)
	}
}

func TestPolicyBuilds(t *testing.T) {
	trusted, org := t.TempDir(), t.TempDir()
	for _, d := range []string{trusted, org} {
		if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &DockerPolicy{Runtime: "runsc", TagPrefix: "org-a/", Trusted: []string{trusted}}
	// Platform content builds on the host as before.
	out, err := p.Rewrite([]string{"build", "-t", "bench/base:1", "-f", filepath.Join(trusted, "Dockerfile"), trusted})
	if err != nil || out[0] != "build" || out[2] != "bench/base:1" {
		t.Fatalf("trusted build: %v %v", out, err)
	}
	// An org Dockerfile without a builder is refused, never built on the host.
	orgBuild := []string{"build", "-t", "bench/base:1", "-f", filepath.Join(org, "Dockerfile"), "--network", "none", org}
	if _, err := p.Rewrite(orgBuild); !errors.Is(err, ErrPolicy) {
		t.Fatalf("org build without builder: %v", err)
	}
	// An org folder inside a trusted one is still org content.
	inner := filepath.Join(trusted, "projects", "org_a")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	p.Untrusted = []string{inner}
	if _, err := p.Rewrite([]string{"build", "-t", "x", inner}); !errors.Is(err, ErrPolicy) {
		t.Fatalf("untrusted folder inside trusted built on host: %v", err)
	}
	p.Builder = "heimdall-org-builder"
	out, err = p.Rewrite(orgBuild)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(out, " ")
	if !strings.HasPrefix(got, "buildx build --builder heimdall-org-builder --load ") || !strings.Contains(got, "-t org-a/bench/base:1") || strings.Contains(got, " -t bench/base:1") {
		t.Fatalf("org build: %s", got)
	}
	// Later references use the namespaced tag.
	out, _ = p.Rewrite([]string{"image", "inspect", "--format", "{{.Id}}", "bench/base:1"})
	if out[len(out)-1] != "org-a/bench/base:1" {
		t.Fatalf("inspect not namespaced: %v", out)
	}
	out, _ = p.Rewrite([]string{"run", "--rm", "bench/base:1", "sh"})
	if out[len(out)-2] != "org-a/bench/base:1" {
		t.Fatalf("run not namespaced: %v", out)
	}
	// Dangerous build flags and host network are refused for org builds.
	for _, bad := range [][]string{
		{"build", "--network", "host", "-t", "x", org},
		{"build", "--secret", "id=a,src=/etc/shadow", "-t", "x", org},
		{"build", "--build-context", "h=/", "-t", "x", org},
		{"build", "-t", "x", "https://example.com/repo.git"},
		{"build", "-t", "org-b/x", org},
	} {
		if _, err := p.Rewrite(bad); !errors.Is(err, ErrPolicy) {
			t.Errorf("%v accepted: %v", bad, err)
		}
	}
}

func TestPolicyForeignAndPull(t *testing.T) {
	digest := "python@sha256:" + strings.Repeat("a", 64)
	p := &DockerPolicy{TagPrefix: "org-a/", Platform: []string{"ghcr.io/heimdall/", "redis:7"}}
	if _, err := p.Rewrite([]string{"run", "org-b/secret:1"}); !errors.Is(err, ErrPolicy) {
		t.Fatalf("other org's image ran: %v", err)
	}
	if _, err := p.Rewrite([]string{"run", "-e", "X=org-b/y", "org-a/mine"}); err != nil {
		t.Fatalf("own image or env value refused: %v", err)
	}
	if _, err := p.Rewrite([]string{"pull", digest}); !errors.Is(err, ErrPolicy) {
		t.Fatal("unapproved pull allowed")
	}
	p.SetApproved([]string{digest})
	for _, ok := range []string{digest, "redis:7", "ghcr.io/heimdall/helper:2"} {
		if _, err := p.Rewrite([]string{"pull", ok}); err != nil {
			t.Errorf("pull %s: %v", ok, err)
		}
	}
	for _, bad := range []string{"redis:8", "bench/coding-core-base:1", "python:3.12"} {
		if _, err := p.Rewrite([]string{"pull", bad}); !errors.Is(err, ErrPolicy) {
			t.Errorf("pull %s allowed", bad)
		}
	}
}

// A refused call never reaches the docker CLI.
func TestPolicyRefusedCommand(t *testing.T) {
	d := Docker{Binary: "/nonexistent/docker", Policy: &DockerPolicy{TagPrefix: "org-a/"}}
	if _, err := d.Run(context.Background(), "pull", "anything:1"); !errors.Is(err, ErrPolicy) {
		t.Fatalf("refusal not returned: %v", err)
	}
	if err := d.Command(context.Background(), "pull", "x:1").Run(); !errors.Is(err, ErrPolicy) {
		t.Fatalf("Command refusal: %v", err)
	}
}

// G3 B3: with a Session, an org build runs under the session command (a
// fresh builder per build), never straight on a shared builder; other
// docker calls and platform builds stay plain docker calls.
func TestPolicyBuildSession(t *testing.T) {
	trusted, org := t.TempDir(), t.TempDir()
	for _, d := range []string{trusted, org} {
		if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := &DockerPolicy{Runtime: "runsc", TagPrefix: "org-a/", Trusted: []string{trusted},
		Session: []string{"/bin/session", "runner", "build-session", "-org", "a"}}
	d := Docker{Binary: "/usr/bin/docker", Policy: p}
	cmd := d.Command(context.Background(), "build", "-t", "x:1", "--network", "none", org)
	got := strings.Join(cmd.Args, " ")
	want := "/bin/session runner build-session -org a -docker /usr/bin/docker -- buildx build --load -t org-a/x:1 --network none " + org
	if cmd.Path != "/bin/session" || got != want || cmd.Err != nil {
		t.Fatalf("org build under a session:\n got %s (%s, %v)\nwant %s", got, cmd.Path, cmd.Err, want)
	}
	if strings.Contains(got, "--builder") {
		t.Fatalf("a session build named a shared builder: %s", got)
	}
	if cmd.Cancel == nil || cmd.WaitDelay != sessionStopGrace {
		t.Fatal("a cancelled session build is killed without its cleanup")
	}
	for _, args := range [][]string{
		{"build", "-t", "bench/base:1", trusted},
		{"run", "--rm", "img"},
		{"image", "inspect", "x"},
	} {
		c := d.Command(context.Background(), args...)
		if c.Path != "/usr/bin/docker" {
			t.Errorf("%v ran under the session: %v", args, c.Args)
		}
	}
	// A refused build never starts a session.
	c := d.Command(context.Background(), "build", "--secret", "id=x", "-t", "x", org)
	if !errors.Is(c.Err, ErrPolicy) {
		t.Fatalf("refused build: %v", c.Err)
	}
}
