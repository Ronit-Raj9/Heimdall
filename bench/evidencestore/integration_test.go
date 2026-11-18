//go:build integration

package evidencestore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/evidencestore"
	"github.com/maximhq/bifrost/bench/evidencestore/internal/miniotest"
	"github.com/maximhq/bifrost/bench/evidencestore/s3blob"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestMinIOMirrorRestoreArchive runs the whole evidence path against a real
// MinIO container: mirror (with a multipart object), idempotent second pass,
// deep verify, restore into a separate folder, archive and restore in place,
// a tampered object refused, and virtual-host addressing.
func TestMinIOMirrorRestoreArchive(t *testing.T) {
	srv := miniotest.Start(t)
	ctx := context.Background()
	newClient := func(bucket string, virtual bool) *s3blob.Client {
		cfg := s3blob.Config{Endpoint: srv.Endpoint, Region: srv.Region, Bucket: bucket, Prefix: "heimdall",
			AccessKey: srv.AccessKey, SecretKey: srv.SecretKey, Addressing: s3blob.AddressingPath, ChecksumSHA256: true,
			PartSize: s3blob.MinPartSize, MultipartThreshold: s3blob.MinPartSize}
		if virtual {
			cfg.Endpoint, cfg.Addressing, cfg.HTTPClient = srv.VirtualEndpoint(), s3blob.AddressingVirtual, srv.HTTPClient()
		}
		c, err := s3blob.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.MakeBucket(ctx); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, virtual := range []bool{false, true} {
		t.Run(fmt.Sprintf("virtual=%v", virtual), func(t *testing.T) {
			client := newClient(fmt.Sprintf("hd-ev-%v", virtual), virtual)
			org := newOrg(t, "org_minio")
			f := newExperiment(t, org, "e_minio")
			big := strings.Repeat("evidence-", 700_000) // 6.3 MB: multipart with 5 MiB parts
			f.sealed("s_1", "r_1", "agent.stream.jsonl", big)
			f.sealed("s_2", "r_2")
			f.run("s_3", "r_3")
			mir := evidencestore.NewMirror(client, nil)

			res, err := mir.Experiment(ctx, org, "e_minio")
			if err != nil {
				t.Fatal(err)
			}
			if res.Seals != 2 || len(res.Pending) != 1 || res.Uploaded != res.Files {
				t.Fatalf("mirror: %+v", res)
			}
			again, err := mir.Experiment(ctx, org, "e_minio")
			if err != nil || !again.Unchanged || again.Uploaded != 0 {
				t.Fatalf("second pass: %+v %v", again, err)
			}
			rep, err := mir.Verify(ctx, org, "e_minio", true)
			if err != nil || !rep.OK() {
				t.Fatalf("deep verify: %+v %v", rep, err)
			}
			info, err := client.Head(ctx, "org_minio/e_minio/slots/s_1/runs/r_1/agent.stream.jsonl")
			if err != nil || info.SHA256 != utils.SHA256Bytes([]byte(big)) {
				t.Fatalf("multipart object: %+v %v", info, err)
			}

			target := t.TempDir()
			t.Cleanup(func() { makeWritable(target) })
			if _, err := mir.Restore(ctx, org, "e_minio", evidencestore.RestoreOptions{Target: target}); err != nil {
				t.Fatal(err)
			}
			st, _ := store.NewFSStore(target, nil)
			if err := st.VerifyChain("e_minio"); err != nil {
				t.Fatal(err)
			}

			// Archive: the run in flight blocks it until it seals.
			if _, err := mir.Archive(ctx, org, "e_minio"); !errors.Is(err, evidencestore.ErrRefused) {
				t.Fatalf("archive with an unsealed run: %v", err)
			}
			f.seal("r_3")
			stub, err := mir.Archive(ctx, org, "e_minio")
			if err != nil {
				t.Fatal(err)
			}
			if stub.Runs != 3 || !strings.HasPrefix(stub.Mirror, "s3://hd-ev-") {
				t.Fatalf("stub: %+v", stub)
			}
			if _, err := mir.Restore(ctx, org, "e_minio", evidencestore.RestoreOptions{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(org.Store.Root(), "e_minio", "manifest.json")); err != nil {
				t.Fatal("not restored in place")
			}

			// Someone with bucket access changes one object: restore refuses.
			forged := []byte("--- a\n+++ b\n+forged\n")
			if err := client.Put(ctx, "org_minio/e_minio/slots/s_2/runs/r_2/workspace.diff", evidencestore.PutInput{
				Body: bytes.NewReader(forged), Size: int64(len(forged)), SHA256: utils.SHA256Bytes(forged)}); err != nil {
				t.Fatal(err)
			}
			other := t.TempDir()
			t.Cleanup(func() { makeWritable(other) })
			if _, err := mir.Restore(ctx, org, "e_minio", evidencestore.RestoreOptions{Target: other}); !errors.Is(err, evidencestore.ErrRefused) {
				t.Fatalf("tampered restore: %v", err)
			}
			if _, err := os.Stat(filepath.Join(other, "e_minio")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("tampered restore left files in place")
			}
			rep, err = mir.Verify(ctx, org, "e_minio", false)
			if err != nil || rep.OK() || !strings.Contains(strings.Join(rep.Mismatched, " "), "workspace.diff") {
				t.Fatalf("verify after tamper: %+v %v", rep, err)
			}
		})
	}
}

// ---- fixtures (the package's internal fixtures are not visible here) ----

type fixture struct {
	t    *testing.T
	org  evidencestore.Org
	m    *schemas.Manifest
	runs map[string]*schemas.Run
}

func newOrg(t *testing.T, id string) evidencestore.Org {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return evidencestore.Org{ID: id, Store: st}
}

func makeWritable(root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
}

func newExperiment(t *testing.T, org evidencestore.Org, expID string) *fixture {
	t.Helper()
	m := &schemas.Manifest{ExpID: expID, CreatedAt: time.Now().UTC(), SuiteID: "coding_core",
		Experiment: schemas.Experiment{ID: expID, Name: "minio fixture"}, Arms: map[string]schemas.ArmManifest{"A": {}}}
	for i := 1; i <= 3; i++ {
		m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%d", i), ExpID: expID, PairID: fmt.Sprintf("p_%d", i),
			Task: "t01", Arm: "A", Condition: "clean", Trial: i, OrderIndex: i, Counted: true})
	}
	h, err := engine.ManifestHash(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Hash = h
	if err := org.Store.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, org: org, m: m, runs: map[string]*schemas.Run{}}
}

func (f *fixture) run(slotID, runID string, extra ...string) {
	f.t.Helper()
	var sl *schemas.Slot
	for i := range f.m.Slots {
		if f.m.Slots[i].SlotID == slotID {
			sl = &f.m.Slots[i]
		}
	}
	r := &schemas.Run{RunID: runID, SlotID: slotID, PairID: sl.PairID, ExpID: f.m.ExpID, Attempt: 1,
		Stage: schemas.StageCleanup, ManifestHash: f.m.Hash, Outcome: schemas.OutcomePass}
	ctx := context.Background()
	if _, err := f.org.Store.NewRun(ctx, sl, r); err != nil {
		f.t.Fatal(err)
	}
	files := map[string]string{"workspace.diff": "--- a\n+++ b\n", "evidence/shim.log": "shim\n"}
	for i := 0; i+1 < len(extra); i += 2 {
		files[extra[i]] = extra[i+1]
	}
	for name, data := range files {
		if err := f.org.Store.WriteRunFile(ctx, r, name, []byte(data)); err != nil {
			f.t.Fatal(err)
		}
	}
	f.runs[runID] = r
}

func (f *fixture) seal(runID string) {
	f.t.Helper()
	if _, err := f.org.Store.Seal(context.Background(), f.runs[runID]); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) sealed(slotID, runID string, extra ...string) {
	f.run(slotID, runID, extra...)
	f.seal(runID)
}
