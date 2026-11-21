package evidencestore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEraseExperimentAndOrg(t *testing.T) {
	ctx := context.Background()
	d, err := NewDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	put := func(key string) {
		body := []byte(key)
		sum := sha256.Sum256(body)
		if err := d.Put(ctx, key, PutInput{Body: bytes.NewReader(body), Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"org_a/exp_1/a.json", "org_a/exp_1/r/b.json", "org_a/exp_2/c.json", "org_b/exp_1/d.json"} {
		put(k)
	}
	m := NewMirror(d, schemas.NoopLogger{})
	if n, err := m.EraseExperiment(ctx, "org_a", "exp_1"); err != nil || n != 2 {
		t.Fatalf("erase experiment %d %v", n, err)
	}
	if _, err := m.EraseExperiment(ctx, "org_a", "../x"); err == nil {
		t.Fatal("bad id accepted")
	}
	if n, err := m.EraseOrg(ctx, "org_a"); err != nil || n != 1 {
		t.Fatalf("erase org %d %v", n, err)
	}
	left, _ := d.List(ctx, "")
	if len(left) != 1 || left[0].Key != "org_b/exp_1/d.json" {
		t.Fatalf("left %+v", left)
	}
}

func TestRemoveFolderReadOnlyRuns(t *testing.T) {
	root := filepath.Join(t.TempDir(), "exp_1")
	run := filepath.Join(root, "runs", "r1")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "seal.json"), []byte("{}"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(run, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFolder(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("folder kept: %v", err)
	}
	if err := RemoveFolder(root); err != nil {
		t.Fatalf("missing folder: %v", err)
	}
}
