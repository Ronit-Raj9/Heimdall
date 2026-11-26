package imagepub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeDocker is a docker stand-in whose "image inspect" prints out, or fails
// like a missing image when out is "missing".
func fakeDocker(t *testing.T, out string) utils.Docker {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\n"
	if out == "missing" {
		script += "echo 'Error response from daemon: No such image: x' >&2\nexit 1\n"
	} else {
		script += "case \"$1\" in\n  image) printf '%s\\n' '" + out + "' ;;\n  save) printf 'TARDATA' ;;\nesac\n"
	}
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}
}

func TestQualifyRefusesOutsideRepoDigest(t *testing.T) {
	set := map[string]string{"bench/task-a:1": "benchmark x"}
	d := fakeDocker(t, `["docker.io/evil/x@sha256:`+hex64+`"]`)
	err := Qualify(context.Background(), d, "bench/task-a:1", set, nil)
	if !errors.Is(err, ErrNotPlatform) {
		t.Fatalf("err = %v, want ErrNotPlatform", err)
	}
}

func TestQualifyAcceptsLocalBuild(t *testing.T) {
	set := map[string]string{"bench/task-a:1": "benchmark x"}
	for _, out := range []string{`[]`, `null`, `["bench/task-a@sha256:` + hex64 + `"]`, `["ghcr.io/acme/base@sha256:` + hex64 + `"]`} {
		d := fakeDocker(t, out)
		if err := Qualify(context.Background(), d, "bench/task-a:1", set, []string{"ghcr.io/acme/"}); err != nil {
			t.Errorf("%s: %v", out, err)
		}
	}
}

func TestQualifyRefusesOutsideSetAndMissing(t *testing.T) {
	set := map[string]string{"bench/task-a:1": "benchmark x"}
	d := fakeDocker(t, `[]`)
	if err := Qualify(context.Background(), d, "bench/task-b:1", set, nil); !errors.Is(err, ErrNotPlatform) {
		t.Errorf("outside the set: %v", err)
	}
	if err := Qualify(context.Background(), d, "org-o1/bench/task-a:1", set, nil); !errors.Is(err, ErrNotPlatform) {
		t.Errorf("org ref: %v", err)
	}
	if err := Qualify(context.Background(), fakeDocker(t, "missing"), "bench/task-a:1", set, nil); !errors.Is(err, ErrMissing) {
		t.Errorf("missing: %v", err)
	}
}
