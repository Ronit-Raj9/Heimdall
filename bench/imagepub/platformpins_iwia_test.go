package imagepub

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlatformPinsFromPlatformLocksOnly: the pins come from the locks of the
// checkout's and the library's own benchmarks (task, base and service
// images alike); every platform image without a lock pin is listed with no
// pin; an organization's folder contributes nothing.
func TestPlatformPinsFromPlatformLocksOnly(t *testing.T) {
	checkout, library, org := t.TempDir(), t.TempDir(), t.TempDir()
	s := filepath.Join(library, "benchmarks", "mini")
	if err := os.CopyFS(s, os.DirFS(filepath.Join("..", "suite", "testdata", "valid", "mini"))); err != nil {
		t.Fatal(err)
	}
	base := "registry.example/library/mini-base@sha256:" + hex64
	svc := "postgres@sha256:" + hex64
	writeFile(t, filepath.Join(s, "suite.lock"), `{"suite":"mini","fingerprints":{"version":1,"suite":{"settings":"x","images":{"base":"bench/mini-base:1=`+base+`","svc":"postgres:16=`+svc+`"}}}}`)
	writeFile(t, filepath.Join(org, "benchmarks", "orgsuite", "suite.yaml"), "id: orgsuite\nbase_image:\n  tag: bench/orgsuite-base:1\n")

	pins, err := PlatformPins(checkout, library)
	if err != nil {
		t.Fatal(err)
	}
	if pins["bench/mini-base:1"] != base || pins["postgres:16"] != svc {
		t.Fatalf("pins = %v", pins)
	}
	if pin, ok := pins["bench/coding-core-base:1"]; !ok || pin != "" {
		t.Fatalf("core base = %q %v", pin, ok)
	}
	if _, ok := pins["bench/orgsuite-base:1"]; ok {
		t.Fatal("an organization's benchmark gave a platform pin")
	}
}
