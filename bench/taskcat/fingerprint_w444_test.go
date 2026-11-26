package taskcat

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/suite"
)

// TestLoaderReadsCategoriesWithoutFingerprintChange: setting a label writes
// only the dot-named sidecar, so the suite and task fingerprints (what an
// admission record is bound to) stay the same and an admitted task stays
// admitted.
func TestLoaderReadsCategoriesWithoutFingerprintChange(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("cp", "-r", filepath.Join("..", "suite", "testdata", "valid", "mini"), root).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	load := func() string {
		t.Helper()
		s, err := suite.Builtin(root)[0].Load(context.Background(), "mini")
		if err != nil {
			t.Fatal(err)
		}
		fp, err := suite.Fingerprints(s, suite.FingerprintOptions{})
		if err != nil {
			t.Fatal(err)
		}
		h, err := suite.FingerprintHash(fp)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	before := load()
	if err := Set(filepath.Join(root, "mini"), "m01", Label{Category: BugFix, Source: SourcePerson, Why: "Ana"}); err != nil {
		t.Fatal(err)
	}
	if after := load(); after != before {
		t.Fatalf("fingerprint changed after a label: %s -> %s", before, after)
	}
	if m := mustRead(t, filepath.Join(root, "mini")); m["m01"].Category != BugFix {
		t.Fatalf("labels = %+v", m)
	}
}
