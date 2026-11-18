package suite

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/compose"
)

// copyStack copies the acme-stack example suite (three compose tasks) into
// a temp benchmarks folder, without its lock.
func copyStack(t *testing.T) (benchRoot, packRoot string) {
	t.Helper()
	benchRoot = t.TempDir()
	packRoot = filepath.Join(benchRoot, "acme-stack")
	src := filepath.Join("..", "examples", "acme-stack", "benchmarks", "acme-stack")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(packRoot, r)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if r == LockFile {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy acme-stack: %v", err)
	}
	return benchRoot, packRoot
}

func TestLoadComposeTask(t *testing.T) {
	benchRoot, _ := copyStack(t)
	s, err := newLocalLoader(benchRoot).Load(context.Background(), "acme-stack")
	if err != nil {
		t.Fatal(err)
	}
	task := s.TaskByID("health-db")
	if task == nil || task.Environment == nil {
		t.Fatalf("task %+v", task)
	}
	env := task.Environment
	if env.Dockerfile != "stack/app/Dockerfile" || env.Context != "stack/app" || env.WorkspacePath != "/app" || !env.BuildNetwork ||
		env.Env["DATABASE_URL"] != "postgresql://acme:acme@db:5432/acme" {
		t.Fatalf("environment %+v", env)
	}
	var ids []string
	for _, sv := range task.Services {
		ids = append(ids, sv.ID)
	}
	if strings.Join(ids, ",") != "db,cache,seed" {
		t.Fatalf("services %v", ids)
	}
	seed := task.Services[2]
	if !seed.Compose.RunOnce || !seed.Compose.Verifier || seed.Files["/seed/seed.sql"] != "stack/db/seed/seed.sql" {
		t.Fatalf("seed %+v %+v", seed, seed.Compose)
	}
	// Lint reports what was changed, here the published port of fix-migration.
	var found bool
	for _, p := range Lint(s) {
		if strings.Contains(p.Path, "fix-migration/stack/compose.yaml") && strings.Contains(p.Message, "host port 5432 is not published") && p.Severity == SeverityWarning {
			found = true
		}
		if p.Severity == SeverityError {
			t.Errorf("lint error: %s %s", p.Path, p.Message)
		}
	}
	if !found {
		t.Fatalf("lint did not report the published port: %v", Lint(s))
	}
}

func TestLoadComposeRefused(t *testing.T) {
	benchRoot, packRoot := copyStack(t)
	compose := filepath.Join(packRoot, "tasks", "cache-invalidation", "stack", "compose.yaml")
	editFile(t, compose, "  cache:\n    image: redis:7-alpine\n", "  cache:\n    image: redis:7-alpine\n    privileged: true\n    network_mode: host\n")
	_, err := newLocalLoader(benchRoot).Load(context.Background(), "acme-stack")
	if err == nil {
		t.Fatal("a privileged compose service loaded")
	}
	for _, want := range []string{
		"tasks/cache-invalidation/task.yaml: compose stack/compose.yaml: services.cache.privileged: refused: privileged mode",
		"services.cache.network_mode: refused: network_mode \"host\" is not allowed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
	// LoadTask (task validate) refuses it the same way.
	if _, _, err := LoadTask(benchRoot, "acme-stack", "cache-invalidation"); err == nil || !strings.Contains(err.Error(), "privileged mode") {
		t.Fatalf("LoadTask: %v", err)
	}
}

// TestLockComposeHash: the compose file and the service images are bound by
// the lock, so editing the stack changes the task's hash and fingerprint.
func TestLockComposeHash(t *testing.T) {
	benchRoot, packRoot := copyStack(t)
	load := func() map[string]string {
		s, err := newLocalLoader(benchRoot).Load(context.Background(), "acme-stack")
		if err != nil {
			t.Fatal(err)
		}
		l, err := LockWith(s, time.Unix(0, 0), FingerprintOptions{})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for id, tl := range l.Tasks {
			out[id] = tl.FixturesSHA256 + tl.RepoTreeSHA256
		}
		fp := l.Fingerprints.Tasks["health-db"]
		if fp.Images["service/db"] == "" || fp.Images["service/seed"] == "" || fp.Images["task"] == "" {
			t.Fatalf("health-db images %v", fp.Images)
		}
		return out
	}
	before := load()
	editFile(t, filepath.Join(packRoot, "tasks", "health-db", "stack", "compose.yaml"), "retries: 60", "retries: 61")
	after := load()
	if before["health-db"] == after["health-db"] {
		t.Fatal("editing the compose file kept the task hash")
	}
	if before["fix-migration"] != after["fix-migration"] {
		t.Fatal("another task's hash moved")
	}
}

// TestNewTaskFromCompose: task init -from-compose copies a team's project,
// leaves out .git, node_modules and symlinks, writes a task that loads, and
// lint lists the drafts still to edit and what the mapping changed.
func TestNewTaskFromCompose(t *testing.T) {
	if compose.EditMeMarker() != EditMeMarker {
		t.Fatalf("compose's EDIT ME marker %q differs from the suite's %q", compose.EditMeMarker(), EditMeMarker)
	}
	benchRoot, packRoot := copyStack(t)
	team := t.TempDir()
	for rel, body := range map[string]string{
		"docker-compose.yml":      "services:\n  web:\n    build: .\n    working_dir: /srv\n    ports: [\"8000:8000\"]\n    depends_on: [redis]\n    x-heimdall: {workspace: true}\n  redis:\n    image: redis:7-alpine\n    restart: always\n",
		"Dockerfile":              "FROM python:3.12-slim\nWORKDIR /srv\nCOPY . .\n",
		"app.py":                  "print('hi')\n",
		".git/HEAD":               "ref: refs/heads/main\n",
		"node_modules/x/index.js": "x\n",
	} {
		p := filepath.Join(team, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(team, "passwd")); err != nil {
		t.Fatal(err)
	}
	res, err := compose.NewTask(compose.NewTaskOptions{SuiteDir: packRoot, ID: "team-web", ComposeFile: filepath.Join(team, "docker-compose.yml")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Workspace != "web" || strings.Join(res.Services, ",") != "redis" || res.Copied != 3 || len(res.Skipped) != 3 {
		t.Fatalf("result %+v", res)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "stack", ".git")); err == nil {
		t.Fatal(".git was copied")
	}
	_, task, err := LoadTask(benchRoot, "acme-stack", "team-web")
	if err != nil {
		t.Fatal(err)
	}
	if task.Compose != "stack/docker-compose.yml" || task.Environment.WorkspacePath != "/srv" || len(task.Services) != 1 {
		t.Fatalf("task %+v", task)
	}
	s, _, err := LoadTask(benchRoot, "acme-stack", "team-web")
	if err != nil {
		t.Fatal(err)
	}
	var editMe, port, restart bool
	for _, p := range LintTask(s, task) {
		editMe = editMe || strings.Contains(p.Message, "EDIT ME")
		port = port || strings.Contains(p.Message, "host port 8000 is not published")
		restart = restart || strings.Contains(p.Message, "services.redis.restart: ignored")
	}
	if !editMe || !port || !restart {
		t.Fatalf("lint: edit me %t, port %t, restart %t: %v", editMe, port, restart, LintTask(s, task))
	}
}
