package evidencestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// corruptingBlob flips one byte of every Get of keys with the suffix.
type corruptingBlob struct {
	Blob
	suffix string
}

func (b *corruptingBlob) Get(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	rc, info, err := b.Blob.Get(ctx, key)
	if err != nil || !strings.HasSuffix(key, b.suffix) {
		return rc, info, err
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	data[len(data)/2] ^= 0x20
	return io.NopCloser(bytes.NewReader(data)), info, nil
}

func TestRestoreRefusesTampering(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(t *testing.T, d *Dir, f *fixture) Blob
		want   string
	}{
		{"object bytes changed", func(t *testing.T, d *Dir, f *fixture) Blob {
			p := filepath.Join(d.Root(), "local", "e_rt", "slots", "s_2", "runs", "r_2", "workspace.diff")
			if err := os.WriteFile(p, []byte("--- a\n+++ b\n+x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return d
		}, "checksum"},
		{"corrupted in transit", func(t *testing.T, d *Dir, f *fixture) Blob {
			return &corruptingBlob{Blob: d, suffix: "big.bin"}
		}, "checksum"},
		{"object and listing rewritten to match", func(t *testing.T, d *Dir, f *fixture) Blob {
			rewriteMirrored(t, d, "local", "e_rt", "slots/s_2/runs/r_2/workspace.diff", []byte("--- a\n+++ b\n+x\n"))
			return d
		}, "workspace.diff"},
		{"seals.log line removed, listing rewritten", func(t *testing.T, d *Dir, f *fixture) Blob {
			b, _ := os.ReadFile(filepath.Join(d.Root(), "local", "e_rt", sealsLogFile))
			lines := strings.SplitAfter(string(b), "\n")
			rewriteMirrored(t, d, "local", "e_rt", sealsLogFile, []byte(lines[0]+lines[2]))
			return d
		}, "seal"},
		{"seal.json and its log line rewritten", func(t *testing.T, d *Dir, f *fixture) Blob {
			// The attacker edits a file, rewrites its seal.json to match and
			// the run's seals.log line: the next run's prev_seal breaks.
			base := "slots/s_1/runs/r_1/"
			rewriteMirrored(t, d, "local", "e_rt", base+"workspace.diff", []byte("forged\n"))
			sealPath := filepath.Join(d.Root(), "local", "e_rt", filepath.FromSlash(base+"seal.json"))
			sb, _ := os.ReadFile(sealPath)
			old := utils.SHA256Bytes([]byte("--- a\n+++ b\n"))
			forged := bytes.Replace(sb, []byte(old), []byte(utils.SHA256Bytes([]byte("forged\n"))), 1)
			rewriteMirrored(t, d, "local", "e_rt", base+"seal.json", forged)
			lb, _ := os.ReadFile(filepath.Join(d.Root(), "local", "e_rt", sealsLogFile))
			nl := bytes.Replace(lb, []byte(utils.SHA256Bytes(sb)), []byte(utils.SHA256Bytes(forged)), 1)
			rewriteMirrored(t, d, "local", "e_rt", sealsLogFile, nl)
			return d
		}, "prev_seal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			org := newOrg(t, "local")
			f := standard(t, org, "e_rt")
			mir, d := newDirMirror(t)
			if _, err := mir.Experiment(ctx, org, "e_rt"); err != nil {
				t.Fatal(err)
			}
			blob := tc.mutate(t, d, f)
			target := t.TempDir()
			t.Cleanup(func() { makeWritable(target) })
			_, err := NewMirror(blob, nil).Restore(ctx, org, "e_rt", RestoreOptions{Target: target})
			if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("restore: %v (want ErrRefused mentioning %q)", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(target, "e_rt")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("refused restore left the experiment in place")
			}
			if entries, _ := os.ReadDir(filepath.Join(target, stateDir, "staging")); len(entries) != 0 {
				t.Fatalf("staging left: %v", entries)
			}
		})
	}
}

// rewriteMirrored replaces a mirrored object and updates the listing to
// match, as someone with write access to the bucket could.
func rewriteMirrored(t *testing.T, d *Dir, orgID, expID, rel string, data []byte) {
	t.Helper()
	ctx := context.Background()
	key := expPrefix(orgID, expID) + rel
	if err := d.Put(ctx, key, PutInput{Body: bytes.NewReader(data), Size: int64(len(data)), SHA256: utils.SHA256Bytes(data)}); err != nil {
		t.Fatal(err)
	}
	l, _, err := readListing(ctx, d, orgID, expID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range l.Files {
		if l.Files[i].Path == rel {
			l.Files[i].Size, l.Files[i].SHA256 = int64(len(data)), utils.SHA256Bytes(data)
		}
	}
	if rel == sealsLogFile {
		entries, _ := parseSealsLog(data)
		l.Seals = len(entries)
		l.LastSeal = entries[len(entries)-1].Hash
	}
	b, _ := encodeListing(l)
	if err := d.Put(ctx, listingKey(orgID, expID), PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: utils.SHA256Bytes(b)}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesMirrorChangedAfterArchive(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := newExperiment(t, org, "e_anchor")
	f.sealed("s_1", "r_1")
	mir, d := newDirMirror(t)
	if _, err := mir.Archive(ctx, org, "e_anchor"); err != nil {
		t.Fatal(err)
	}
	// Someone with bucket access rewrites the listing (same content, new
	// generation): the stub no longer matches.
	l, _, _ := readListing(ctx, d, "local", "e_anchor")
	l.Generation++
	b, _ := encodeListing(l)
	if err := d.Put(ctx, listingKey("local", "e_anchor"), PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: utils.SHA256Bytes(b)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mir.Restore(ctx, org, "e_anchor", RestoreOptions{}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "changed after archiving") {
		t.Fatalf("restore: %v", err)
	}
	if _, err := ReadStub(org.root(), "e_anchor"); err != nil {
		t.Fatal("stub lost after a refused restore")
	}
}

func TestRecoverInterruptedArchive(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := newExperiment(t, org, "e_crash")
	f.sealed("s_1", "r_1")
	mir, _ := newDirMirror(t)
	// Crash after the folder moved to the trash, before the stub: the
	// experiment is put back.
	trashed, err := trashPath(org.root(), "e_crash")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.dir(), trashed); err != nil {
		t.Fatal(err)
	}
	if _, err := mir.Sweep(ctx, []Org{org}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir(), manifestFile)); err != nil {
		t.Fatal("interrupted archive not undone")
	}
	// Crash after the stub was written: the trash is deleted.
	if _, err := mir.Archive(ctx, org, "e_crash"); err != nil {
		t.Fatal(err)
	}
	leftover, _ := trashPath(org.root(), "e_crash")
	if err := os.MkdirAll(filepath.Join(leftover, "slots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mir.recover(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("finished archive's trash not deleted")
	}
}

func TestRestoreToIsAnchoredByStub(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := newExperiment(t, org, "e_anchor2")
	f.sealed("s_1", "r_1")
	mir, d := newDirMirror(t)
	if _, err := mir.Archive(ctx, org, "e_anchor2"); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	t.Cleanup(func() { makeWritable(target) })
	if _, err := mir.Restore(ctx, org, "e_anchor2", RestoreOptions{Target: target}); err != nil {
		t.Fatalf("auditor copy of an archived experiment: %v", err)
	}
	l, _, _ := readListing(ctx, d, "local", "e_anchor2")
	l.Generation += 5
	b, _ := encodeListing(l)
	if err := d.Put(ctx, listingKey("local", "e_anchor2"), PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: utils.SHA256Bytes(b)}); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	t.Cleanup(func() { makeWritable(other) })
	if _, err := mir.Restore(ctx, org, "e_anchor2", RestoreOptions{Target: other}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "changed after archiving") {
		t.Fatalf("auditor copy from a rewritten mirror: %v", err)
	}
}

func TestRecoverKeepsBothWhenUnclear(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := newExperiment(t, org, "e_unclear")
	f.sealed("s_1", "r_1")
	mir, _ := newDirMirror(t)
	trashed, err := trashPath(org.root(), "e_unclear")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.dir(), trashed); err != nil {
		t.Fatal(err)
	}
	// Something recreated an empty folder (or a half-written stub) in place.
	if err := os.Mkdir(f.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := mir.Sweep(ctx, []Org{org}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(trashed, manifestFile)); err != nil {
		t.Fatal("recovery deleted the only full copy")
	}
	if err := os.WriteFile(filepath.Join(f.dir(), stubFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mir.recover(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(trashed, manifestFile)); err != nil {
		t.Fatal("recovery trusted a broken stub")
	}
}
