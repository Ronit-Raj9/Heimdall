package imagepub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

func TestSetPinReplacesAndKeepsOthers(t *testing.T) {
	m := filepath.Join(t.TempDir(), "manifest")
	old := "sha256:" + hex64
	writeFile(t, m, "# pins\nbench/a:1 "+old+"\nbench/b:1 "+old+"\n")
	nd := "sha256:" + hex64[1:] + "f"
	if err := SetPin(m, "bench/a:1", nd); err != nil {
		t.Fatal(err)
	}
	if err := SetPin(m, "bench/c:1", nd); err != nil {
		t.Fatal(err)
	}
	got, err := utils.ReadRegistryManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if got["bench/a:1"] != nd || got["bench/b:1"] != old || got["bench/c:1"] != nd || len(got) != 3 {
		t.Fatalf("manifest = %v", got)
	}
	fi, err := os.Stat(m)
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v", fi.Mode(), err)
	}
	if err := SetPin(filepath.Join(t.TempDir(), "new"), "bench/a:1", nd); err != nil {
		t.Fatalf("missing manifest: %v", err)
	}
	if err := SetPin(m, "bench/a:1", "sha256:bad"); err == nil {
		t.Fatal("bad digest accepted")
	}
}
