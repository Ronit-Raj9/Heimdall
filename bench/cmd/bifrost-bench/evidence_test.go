package main

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// evidenceSandbox is a bench root with one recorded experiment and a
// storage section pointing at a folder mirror.
func evidenceSandbox(t *testing.T, storage string) (root, config string) {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "recorded", "e_smoke_sdoj")
	if _, err := os.Stat(src); err != nil {
		t.Skip("recorded experiment not present")
	}
	root = t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(root, "runs", "e_smoke_sdoj", rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(root, "deploy", "bench.yaml")
	if err := os.MkdirAll(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "runs_root: runs\nserver:\n  listen: 127.0.0.1:0\n  mode: recorded\n" + storage
	if err := os.WriteFile(config, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, config
}

func TestEvidenceCommand(t *testing.T) {
	root, config := evidenceSandbox(t, "storage:\n  mirror_dir: mirror\n")
	run := func(args ...string) int {
		t.Helper()
		return runEvidence(append(args[:1:1], append([]string{"-config", config}, args[1:]...)...))
	}
	if code := run("mirror"); code != exitOK {
		t.Fatalf("mirror: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "mirror", "local", "e_smoke_sdoj", ".mirror.json")); err != nil {
		t.Fatal("no listing in the mirror folder")
	}
	if code := run("verify", "-org", "local", "-deep"); code != exitOK {
		t.Fatalf("verify: exit %d", code)
	}
	audit := filepath.Join(root, "audit")
	if code := run("restore", "-org", "local", "-exp", "e_smoke_sdoj", "-to", audit); code != exitOK {
		t.Fatalf("restore -to: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(audit, "e_smoke_sdoj", "seals.log")); err != nil {
		t.Fatal("restored copy missing")
	}
	if code := run("restore", "-org", "local", "-exp", "e_smoke_sdoj"); code != exitInfra {
		t.Fatalf("restore over the live folder: exit %d", code)
	}
	// A changed object makes verify fail with exit 1.
	if err := os.WriteFile(filepath.Join(root, "mirror", "local", "e_smoke_sdoj", "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run("verify", "-org", "local", "-exp", "e_smoke_sdoj"); code != 1 {
		t.Fatalf("verify of a changed mirror: exit %d", code)
	}
	// -mirror overrides the config.
	other := filepath.Join(root, "other")
	if code := run("mirror", "-mirror", "file://"+other, "-org", "local", "-exp", "e_smoke_sdoj"); code != exitOK {
		t.Fatalf("mirror -mirror: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(other, "local", "e_smoke_sdoj", ".mirror.json")); err != nil {
		t.Fatal("-mirror ignored")
	}
	if code := run("archive", "-mirror", other, "-org", "local", "-exp", "e_smoke_sdoj", "-deep"); code != exitOK {
		t.Fatalf("archive: exit %d", code)
	}
	if code := run("list", "-mirror", other, "-json"); code != exitOK {
		t.Fatalf("list: exit %d", code)
	}
	if code := run("restore", "-mirror", other, "-org", "local", "-all"); code != exitOK {
		t.Fatalf("restore -all: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "e_smoke_sdoj", "manifest.json")); err != nil {
		t.Fatal("restore -all did not bring the archived experiment back")
	}
	if code := run("backup", "-mirror", other, "-json"); code != exitOK {
		t.Fatalf("backup without a database: exit %d", code)
	}
	for _, bad := range [][]string{{"nope"}, {"mirror", "extra-arg"}, {"verify"}, {"restore", "-org", "local"}} {
		if code := run(bad...); code == exitOK {
			t.Errorf("%v: exit 0", bad)
		}
	}
	if code := runEvidence(nil); code != exitUsage {
		t.Fatalf("no args: exit %d", code)
	}
}

func TestOpenEvidenceMirror(t *testing.T) {
	cfg := &lib.Config{Root: t.TempDir()}
	if _, err := openEvidenceMirror(cfg, "", nil); err == nil || !strings.Contains(err.Error(), "no mirror configured") {
		t.Fatalf("no mirror: %v", err)
	}
	t.Setenv("HEIMDALL_S3_ACCESS_KEY", "")
	t.Setenv("HEIMDALL_S3_SECRET_KEY", "")
	if _, err := openEvidenceMirror(cfg, "s3://bucket/prefix", nil); err == nil || !strings.Contains(err.Error(), "HEIMDALL_S3_ACCESS_KEY") {
		t.Fatalf("s3 without keys: %v", err)
	}
	t.Setenv("HEIMDALL_S3_ACCESS_KEY", "AKIDEXAMPLE")
	t.Setenv("HEIMDALL_S3_SECRET_KEY", "wJalrXUtnFEMIEXAMPLEKEY")
	t.Setenv("HEIMDALL_S3_ENDPOINT", "http://127.0.0.1:9")
	m, err := openEvidenceMirror(cfg, "s3://bucket/prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := m.Blob.String(); !strings.HasPrefix(s, "s3://bucket/prefix") || strings.Contains(s, "EXAMPLE") {
		t.Fatalf("String: %q", s)
	}
	cfg.Storage.MirrorDir = "m"
	cfg.Storage.S3.Bucket = "b"
	if _, err := openEvidenceMirror(cfg, "", nil); err == nil {
		t.Fatal("both mirrors configured: want error")
	}
}

// TestEvidenceDisasterRecovery mirrors a local and a hosted organization,
// loses the whole runs folder, and brings both back with restore -all.
func TestEvidenceDisasterRecovery(t *testing.T) {
	root, config := evidenceSandbox(t, "storage:\n  mirror_dir: mirror\n")
	hosted := filepath.Join(root, "runs", "orgs", "org_x")
	if err := os.MkdirAll(hosted, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "runs", "e_smoke_sdoj"), filepath.Join(hosted, "e_smoke_sdoj")); err != nil {
		t.Fatal(err)
	}
	if code := runEvidence([]string{"mirror", "-config", config}); code != exitOK {
		t.Fatalf("mirror: exit %d", code)
	}
	runs := filepath.Join(root, "runs")
	_ = filepath.WalkDir(runs, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(runs); err != nil {
		t.Fatal(err)
	}
	if code := runEvidence([]string{"restore", "-config", config, "-all"}); code != exitOK {
		t.Fatalf("restore -all: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(hosted, "e_smoke_sdoj", "seals.log")); err != nil {
		t.Fatalf("hosted org not restored: %v", err)
	}
	if code := runEvidence([]string{"verify", "-config", config, "-org", "org_x", "-deep"}); code != exitOK {
		t.Fatalf("verify after recovery: exit %d", code)
	}
	if code := runEvidence([]string{"restore", "-config", config, "-exp", "e_smoke_sdoj"}); code != exitInfra {
		t.Fatalf("-exp without -org: exit %d", code)
	}
}

// TestEvidenceServerHook starts the server's background mirror and fires
// the seal hook, as serve and the job runner do.
func TestEvidenceServerHook(t *testing.T) {
	root, config := evidenceSandbox(t, "storage:\n  mirror_dir: mirror\n  sweep_interval_m: -1\n")
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	old := evidenceDebounce
	evidenceDebounce = 10 * time.Millisecond
	defer func() { evidenceDebounce = old }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evidenceOnSeal("local", "e_smoke_sdoj") // before start: a no-op
	startEvidenceMirror(ctx, &app{cfg: cfg, logger: utils.NewStdLogger(io.Discard, false)})
	defer func() {
		evidenceMu.Lock()
		svc := evidenceMirror
		evidenceMirror = nil
		evidenceMu.Unlock()
		svc.Close()
	}()
	evidenceOnSeal("local", "e_smoke_sdoj")
	listing := filepath.Join(root, "mirror", "local", "e_smoke_sdoj", ".mirror.json")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(listing); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("seal hook did not mirror the experiment")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// S3 named without credentials: the mirror stays off.
	cfg.Storage.MirrorDir = ""
	cfg.Storage.S3.Bucket = "heimdall-evidence"
	t.Setenv("HEIMDALL_S3_ACCESS_KEY", "")
	evidenceMu.Lock()
	prev := evidenceMirror
	evidenceMu.Unlock()
	startEvidenceMirror(ctx, &app{cfg: cfg, logger: utils.NewStdLogger(io.Discard, false)})
	evidenceMu.Lock()
	same := evidenceMirror == prev
	evidenceMu.Unlock()
	if !same {
		t.Fatal("mirror started without credentials")
	}
}

func TestEvidenceVerifyMirrorOnlyExitsOne(t *testing.T) {
	root, config := evidenceSandbox(t, "storage:\n  mirror_dir: mirror\n")
	if code := runEvidence([]string{"mirror", "-config", config}); code != exitOK {
		t.Fatalf("mirror: exit %d", code)
	}
	exp := filepath.Join(root, "runs", "e_smoke_sdoj")
	_ = filepath.WalkDir(exp, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(exp); err != nil {
		t.Fatal(err)
	}
	if code := runEvidence([]string{"verify", "-config", config, "-org", "local", "-exp", "all"}); code != 1 {
		t.Fatalf("verify of a mirror-only experiment: exit %d, want 1", code)
	}
	if code := runEvidence([]string{"mirror", "-config", config, "-repair"}); code != exitOK {
		t.Fatalf("mirror -repair with nothing local: exit %d", code)
	}
}

// A sweep that stops before it reaches every experiment fails the backup
// even when nothing it reached failed (audit 4-C-07).
func TestEvidenceBackupIncompleteSweepFails(t *testing.T) {
	root, config := evidenceSandbox(t, "storage:\n  mirror_dir: mirror\n")
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	env := &evidenceEnv{cfg: cfg, logger: schemas.NoopLogger{}, out: &out,
		f: evidenceFlags{noDB: true, mirror: filepath.Join(root, "mirror")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := env.backupCmd(ctx); code != exitInfra {
		t.Fatalf("interrupted sweep: exit %d, output %s", code, out.String())
	}
	if !strings.Contains(out.String(), "INCOMPLETE") {
		t.Fatalf("output %s", out.String())
	}
}
