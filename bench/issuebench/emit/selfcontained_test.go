package emit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func TestEmitSelfContainedDockerfile(t *testing.T) {
	env := issuebench.EnvSpec{BaseImage: "python:3.11-slim", Commands: []string{"  pip install -e .  ", "", "apt-get update\napt-get install -y make"}}
	want := "# Written by Heimdall's issue emitter. The worker builds this image at admission.\n" +
		"FROM python:3.11-slim\n" +
		"SHELL [\"/bin/bash\", \"-lc\"]\n" +
		"RUN (command -v git >/dev/null || (apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*))\n" +
		"WORKDIR /testbed\n" +
		"ADD repo.tar.gz /testbed/\n" +
		"RUN pip install -e .\n" +
		"RUN <<'HEIMDALL_STEP'\nset -e\napt-get update\napt-get install -y make\nHEIMDALL_STEP\n" +
		"RUN rm -rf /testbed\n" +
		"ADD repo.tar.gz /testbed/\n" +
		"WORKDIR /testbed\n"
	if got := SelfContainedDockerfile(env); got != want {
		t.Fatalf("dockerfile:\n%s\nwant:\n%s", got, want)
	}
}

func readArchive(t *testing.T, p string) map[string]*tar.Header {
	t.Helper()
	f, err := os.Open(p)
	must(t, err)
	defer f.Close()
	zr, err := gzip.NewReader(f)
	must(t, err)
	tr := tar.NewReader(zr)
	out := map[string]*tar.Header{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		must(t, err)
		out[h.Name] = h
	}
}

func TestEmitSelfContainArchive(t *testing.T) {
	repo := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(repo, "pkg"), 0o755))
	must(t, os.WriteFile(filepath.Join(repo, "pkg", "a.py"), []byte("x = 1\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "run.sh"), []byte("#!/bin/sh\n"), 0o755))
	must(t, os.Symlink("pkg/a.py", filepath.Join(repo, "link.py")))
	task := t.TempDir()
	must(t, os.WriteFile(filepath.Join(task, "task.yaml"), []byte("id: t\nreview: pending\nenvironment:\n  image: heimdall-issues/task-x:abc\n  workspace_path: /testbed\n"), 0o644))
	env := issuebench.EnvSpec{BaseImage: "python:3.11-slim"}
	must(t, SelfContain(task, env, repo))

	arc := filepath.Join(task, "environment", "repo.tar.gz")
	hs := readArchive(t, arc)
	for _, n := range []string{"pkg/", "pkg/a.py", "run.sh", "link.py"} {
		if hs[n] == nil {
			t.Fatalf("archive misses %s: %v", n, hs)
		}
		if !hs[n].ModTime.Equal(archiveTime) || hs[n].Uid != 0 {
			t.Fatalf("%s not normalised: %+v", n, hs[n])
		}
	}
	if hs["run.sh"].Mode != 0o755 || hs["pkg/a.py"].Mode != 0o644 || hs["link.py"].Linkname != "pkg/a.py" {
		t.Fatalf("modes or link: %+v %+v %+v", hs["run.sh"], hs["pkg/a.py"], hs["link.py"])
	}
	first, err := os.ReadFile(arc)
	must(t, err)
	// Same tree, same bytes, whatever the file times.
	must(t, os.Chtimes(filepath.Join(repo, "pkg", "a.py"), time.Now(), time.Now().Add(-time.Hour)))
	must(t, SelfContain(task, env, repo))
	again, err := os.ReadFile(arc)
	must(t, err)
	if !bytes.Equal(first, again) {
		t.Fatal("archive is not deterministic")
	}
	df, err := os.ReadFile(filepath.Join(task, "environment", "Dockerfile"))
	must(t, err)
	if string(df) != SelfContainedDockerfile(env) {
		t.Fatalf("dockerfile %q", df)
	}
	raw, err := os.ReadFile(filepath.Join(task, "task.yaml"))
	must(t, err)
	y := string(raw)
	for _, s := range []string{"dockerfile: environment/Dockerfile", "context: environment", "build_network: true", "workspace_path: /testbed", "review: pending"} {
		if !strings.Contains(y, s) {
			t.Fatalf("task.yaml misses %q:\n%s", s, y)
		}
	}
	if strings.Contains(y, "image:") {
		t.Fatalf("task.yaml keeps the image:\n%s", y)
	}
}

func TestEmitSelfContainRefuses(t *testing.T) {
	env := issuebench.EnvSpec{BaseImage: "python:3.11-slim"}
	mkTask := func() string {
		d := t.TempDir()
		must(t, os.WriteFile(filepath.Join(d, "task.yaml"), []byte("id: t\nenvironment:\n  image: x\n"), 0o644))
		return d
	}
	for _, target := range []string{"../outside", "/etc/passwd"} {
		repo := t.TempDir()
		must(t, os.Symlink(target, filepath.Join(repo, "l")))
		task := mkTask()
		if err := SelfContain(task, env, repo); err == nil || !strings.Contains(err.Error(), "outside the repository") {
			t.Fatalf("link %s: %v", target, err)
		}
		if _, err := os.Stat(filepath.Join(task, "environment", "repo.tar.gz")); !os.IsNotExist(err) {
			t.Fatalf("a refused archive was kept: %v", err)
		}
	}
	repo := t.TempDir()
	big := make([]byte, issuebench.MaxRepoArchiveBytes+(1<<20)-(64<<10)) // about 8.9 MiB compressed
	_, _ = rand.Read(big) // random bytes do not compress
	must(t, os.WriteFile(filepath.Join(repo, "blob.bin"), big, 0o644))
	task := mkTask()
	err := SelfContain(task, env, repo)
	if err == nil || !strings.Contains(err.Error(), "the repository is too large for a hosted task (9 MiB, the cap is 8 MiB)") {
		t.Fatalf("cap: %v", err)
	}
	if _, err := os.Stat(filepath.Join(task, "environment", "repo.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("an oversized archive was kept: %v", err)
	}
	if err := SelfContain(mkTask(), issuebench.EnvSpec{}, t.TempDir()); err == nil {
		t.Fatal("no base image accepted")
	}
}

func TestEmitSelfContainedSuite(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	env := issuebench.EnvSpec{Repo: "Acme/Calc", Version: "1.0", Language: "python", BaseImage: "python:3.11-slim", Commands: []string{"pip install pytest"}, TestCmd: "pytest -rA"}
	in := Input{
		Env:     env,
		RepoDir: repoWithHistory(t),
		Validation: issuebench.Validation{
			Candidate: issuebench.Candidate{
				Repo: "Acme/Calc", PR: 1, Issue: 101, IssueTitle: "add is wrong", IssueBody: "add(1, 2) returns -1",
				IssueCreated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), TestPatch: testPatch, FixPatch: fixPatch, Language: "python",
			},
			FailToPass: []string{"tests/test_calc.py::test_add"},
			Checks:     issuebench.LLMChecks{Clear: true},
		},
	}
	q := &fakeQueue{}
	rep, err := Emit(context.Background(), []Input{in}, Options{BenchRoot: root, WorkDir: filepath.Join(root, "work"), Readiness: q, SelfContained: true})
	must(t, err)
	s := rep.Suites[0]
	if len(s.Tasks) != 1 || len(q.kinds) != 3 {
		t.Fatalf("suite %+v jobs %v", s, q.kinds)
	}
	tid := s.Tasks[0]
	_, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), s.Suite, tid)
	must(t, err)
	e := task.Environment
	if task.Review != schemas.ReviewPending || e == nil || e.Image != "" || e.Dockerfile != "environment/Dockerfile" || e.Context != "environment" || !e.BuildNetwork || e.WorkspacePath != Workspace {
		t.Fatalf("task %s: review %q env %+v", tid, task.Review, e)
	}
	hs := readArchive(t, filepath.Join(root, "benchmarks", s.Suite, "tasks", tid, "environment", "repo.tar.gz"))
	if hs["calc.py"] == nil || hs[".git/HEAD"] == nil {
		t.Fatalf("archive misses the scrubbed checkout: %d entries", len(hs))
	}
}
