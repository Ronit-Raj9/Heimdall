package utils

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestImageName(t *testing.T) {
	for in, want := range map[string]string{"task-tb-fix_git": "task-tb-fix_git", "Task Tb/Fix": "task-tb-fix", "--x--": "x", "": "x", "svc-s-DB.1": "svc-s-db.1"} {
		if got := ImageName(in); got != want {
			t.Errorf("ImageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeTaskFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, c := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTaskImage(t *testing.T) {
	dir := writeTaskFiles(t, map[string]string{"environment/Dockerfile": "FROM x\n", "environment/app.py": "print(1)\n", "other/Dockerfile": "FROM y\n"})
	task := &schemas.Task{ID: "Fix Git", Dir: dir, Environment: &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile"}}
	img, ok, err := TaskImage("tb", task)
	if err != nil || !ok || !strings.HasPrefix(img.Tag, "bench/task-tb-fix-git:") || len(img.Tag) != len("bench/task-tb-fix-git:")+12 {
		t.Fatalf("image %+v %v %v", img, ok, err)
	}
	if img.Context != filepath.Dir(img.Dockerfile) || img.Network {
		t.Fatalf("context %+v", img)
	}
	again, _, _ := TaskImage("tb", task)
	if again.Tag != img.Tag {
		t.Fatal("tag is not stable")
	}
	task.Environment.BuildNetwork = true
	if net, _, _ := TaskImage("tb", task); net.Tag == img.Tag || !net.Network {
		t.Fatal("build network does not change the tag")
	}
	task.Environment.BuildNetwork = false
	if err := os.WriteFile(filepath.Join(dir, "environment/app.py"), []byte("print(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, _, _ := TaskImage("tb", task); changed.Tag == img.Tag {
		t.Fatal("a context change does not change the tag")
	}
	if pre, ok, err := TaskImage("tb", &schemas.Task{Environment: &schemas.TaskEnvironment{Image: "tb/x:1"}}); err != nil || !ok || pre.Tag != "tb/x:1" || pre.Dockerfile != "" {
		t.Fatalf("prebuilt %+v %v %v", pre, ok, err)
	}
	for name, tk := range map[string]*schemas.Task{
		"no environment": {ID: "a"},
		"no image":       {ID: "a", Environment: &schemas.TaskEnvironment{WorkspacePath: "/app"}},
	} {
		if _, ok, err := TaskImage("tb", tk); ok || err != nil {
			t.Errorf("%s: %v %v", name, ok, err)
		}
	}
	task.Environment.Context = "../.."
	if _, _, err := TaskImage("tb", task); err == nil {
		t.Fatal("context outside the task accepted")
	}
}

// fakeImageDocker stands in for docker: image inspect answers "No such
// image" until a build or pull ran, then an id.
func fakeImageDocker(t *testing.T) (Docker, string) {
	t.Helper()
	dir := t.TempDir()
	log, flag := filepath.Join(dir, "calls.log"), filepath.Join(dir, "built")
	script := `#!/bin/sh
echo "$*" >> ` + log + `
case "$1" in
  image) if [ -f ` + flag + ` ]; then echo sha256:abc; else echo "Error: No such image: x" >&2; exit 1; fi ;;
  build|pull) echo "BUILDX=$BUILDX_NO_DEFAULT_ATTESTATIONS"; touch ` + flag + ` ;;
esac
`
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Docker{Binary: bin}, log
}

func TestEnsureImage(t *testing.T) {
	d, log := fakeImageDocker(t)
	var out strings.Builder
	img := LocalImage{Tag: "bench/task-s-t:0123456789ab", Dockerfile: "/t/Dockerfile", Context: "/t"}
	id, err := d.EnsureImage(context.Background(), img, &out)
	if err != nil || id != "sha256:abc" || !strings.Contains(out.String(), "BUILDX=1") {
		t.Fatalf("build: %q %v %q", id, err, out.String())
	}
	if _, err := d.EnsureImage(context.Background(), img, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	builds := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "build ") {
			builds++
			if l != "build -f /t/Dockerfile -t bench/task-s-t:0123456789ab --label bifrost-bench=1 --label heimdall.platform-build=1 --network none /t" {
				t.Fatalf("build line %q", l)
			}
		}
	}
	if builds != 1 {
		t.Fatalf("%d builds:\n%s", builds, raw)
	}
	d2, log2 := fakeImageDocker(t)
	if _, err := d2.EnsureImage(context.Background(), LocalImage{Tag: "postgres:16"}, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(log2); !strings.Contains(string(raw), "pull postgres:16") {
		t.Fatalf("missing prebuilt image not pulled:\n%s", raw)
	}
}
