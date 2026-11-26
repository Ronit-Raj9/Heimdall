package environments

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// iwiaDocker is a docker stand-in that logs every call; an image exists
// once something was pulled.
func iwiaDocker(t *testing.T) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	log, flag := filepath.Join(dir, "calls.log"), filepath.Join(dir, "pulled")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1\" in\n  image) if [ -f " + flag + " ]; then echo sha256:abc; else echo 'Error: No such image: x' >&2; exit 1; fi ;;\n  pull) touch " + flag + " ;;\nesac\n"
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, log
}

// pinnedSuite is a suite whose lock pins task t's image tag to pinned.
func pinnedSuite(id, tag, pinned string) *schemas.Suite {
	return &schemas.Suite{ID: id, Lock: &schemas.SuiteLock{Fingerprints: &schemas.LockFingerprints{
		Tasks: map[string]schemas.Fingerprint{"t": {Images: map[string]string{"task": tag + "=" + pinned}}}}}}
}

// SEC-C C2, rule 2: the task image of a benchmark the org owns is always
// the org's own tag, even when its task file names a platform image and its
// lock pins that image's real digest; a library benchmark's task image keeps
// its plain name from the platform's pin.
func TestOrgTaskImageIsAlwaysOrgTag(t *testing.T) {
	const tag = "bench/task-b:1"
	const pin = "registry.example/library/task-b@sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	library, project := t.TempDir(), t.TempDir()
	policy := func() *utils.DockerPolicy {
		return &utils.DockerPolicy{OrgID: "o1", TagPrefix: utils.OrgImagePrefix("o1"), Trusted: []string{library}, Untrusted: []string{project},
			Platform: []string{"registry.example/library/"},
			PlatformPin: func(ref string) (string, bool) {
				if ref == tag {
					return pin, true
				}
				return "", false
			}}
	}
	task := func(dir string) *schemas.Task {
		return &schemas.Task{ID: "t", Dir: dir, Environment: &schemas.TaskEnvironment{Image: tag}}
	}

	d, log := iwiaDocker(t)
	d.Policy = policy()
	if _, _, err := EnsureTaskImage(context.Background(), d, pinnedSuite("mine", tag, pin), task(filepath.Join(project, "benchmarks", "mine", "tasks", "t")), nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	if !strings.Contains(string(raw), "tag "+pin+" org-o1/"+tag+"\n") {
		t.Fatalf("org benchmark calls:\n%s", raw)
	}

	d2, log2 := iwiaDocker(t)
	d2.Policy = policy()
	if _, _, err := EnsureTaskImage(context.Background(), d2, pinnedSuite("lib", tag, pin), task(filepath.Join(library, "benchmarks", "lib", "tasks", "t")), nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(log2); !strings.Contains(string(raw), "tag "+pin+" "+tag+"\n") {
		t.Fatalf("library benchmark calls:\n%s", raw)
	}
}
