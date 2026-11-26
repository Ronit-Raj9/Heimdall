package imagepub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// TestQualifyRefusesLocalImageWithoutPlatformMark: an image with no
// RepoDigests qualifies as a local platform build only with the platform
// build mark; an org build (mark empty) or an unlabelled image retagged to a
// platform name is refused, so it can never reach the shared registry.
func TestQualifyRefusesLocalImageWithoutPlatformMark(t *testing.T) {
	set := map[string]string{"bench/task-a:1": "benchmark x"}
	for _, label := range []string{"", "<no value>", "0"} {
		bin := filepath.Join(t.TempDir(), "docker")
		script := "#!/bin/sh\ncase \"$*\" in\n  *Labels*) echo '" + label + "' ;;\n  *) echo '[]' ;;\nesac\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		err := Qualify(context.Background(), utils.Docker{Binary: bin}, "bench/task-a:1", set, nil)
		if !errors.Is(err, ErrNotPlatform) {
			t.Errorf("label %q: err = %v, want ErrNotPlatform", label, err)
		}
	}
}
