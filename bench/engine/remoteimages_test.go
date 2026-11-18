package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	suitepkg "github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/verdict"
)

const testBaseImage = "bench/base:1"

// imageSide is a runner side whose environment inspects images: the suite
// has a base image, so a local plan records suite_images, which a hosted
// plan (no Docker) can not.
func imageSide(t *testing.T, ids map[string]string, digests map[string][]string) *remoteSide {
	t.Helper()
	side := runnerSide(t, &schemas.GatewayHygiene{MCPAutoToolInjectOff: true})
	side.b.suite.BaseImage = schemas.ImageSpec{Tag: testBaseImage}
	side.b.reg.Environments = []schemas.Environment{digestEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, side.b.w}, ids}, digests}}
	return side
}

// newDeferredImageWorld is newDeferredWorld with a base image on the
// coordinator's suite too (same suite as the runner's), still planned
// without any image inspector, as on a hosted server.
func newDeferredImageWorld(t *testing.T, mutate func(*remoteSide)) *remoteWorld {
	t.Helper()
	cs := newRemoteSide(t)
	cs.b.reg.Gateway = nil
	cs.b.suite.BaseImage = schemas.ImageSpec{Tag: testBaseImage}
	if mutate != nil {
		mutate(cs)
	}
	m, err := NewPlanner(cs.b.reg, cs.b.root, nil, WithGatewayDeferredToRunner()).Plan(context.Background(), testExperiment())
	if err != nil {
		t.Fatalf("plan without a gateway or Docker: %v", err)
	}
	c, err := NewCoordinator(cs.b.reg, RemoteOptions{Token: remoteTestToken, StagingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return &remoteWorld{coord: cs, c: c, url: serveWorkers(t, c), m: m, run: RunOptions{Remote: c}}
}

// TestRemoteDeferredGatewayImages: a hosted plan freezes no image ids; a
// runner with Docker adopts the empty maps, so the lease is accepted, records
// the ids each slot ran in remote/worker.json, and instrument runner_images
// passes on one id per role, warns on mixed ids and is unknown without a
// record. A manifest that has suite_images keeps the exact comparison.
func TestRemoteDeferredGatewayImages(t *testing.T) {
	rw := newDeferredImageWorld(t, nil)
	if len(rw.m.SuiteImages) != 0 || len(rw.m.TaskImages) != 0 {
		t.Fatalf("hosted plan froze image ids: %v %v", rw.m.SuiteImages, rw.m.TaskImages)
	}
	done := rw.attach(t)
	side := imageSide(t, map[string]string{testBaseImage: "sha256:base1aaaaaaaaaaaa"}, nil)
	// The runner's own plan does record the id: that is what the adoption
	// has to bridge.
	local, err := NewPlanner(side.b.reg, side.b.root, nil).Plan(context.Background(), testExperiment())
	if err != nil || local.SuiteImages["base"] != "sha256:base1aaaaaaaaaaaa" {
		t.Fatalf("runner plan suite_images %v, %v", local.SuiteImages, err)
	}
	w := rw.worker(t, "w1", side)
	ctx := context.Background()
	for range rw.m.Slots {
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease while slots are pending")
		}
		if res := w.Work(ctx, l); res == nil || !res.Accepted {
			journal, _ := os.ReadFile(filepath.Join(rw.c.opts.StagingDir, rw.m.ExpID+".jsonl"))
			t.Fatalf("run not accepted: %+v; journal %s", res, journal)
		}
	}
	if sum := done(); sum.Sealed != len(rw.m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
	runs, err := rw.coord.st.SealedRuns(ctx, rw.m.ExpID, "")
	if err != nil || len(runs) != len(rw.m.Slots) {
		t.Fatalf("sealed runs %d, %v", len(runs), err)
	}
	for _, sr := range runs {
		if sr.RunnerImages == nil || sr.RunnerImages.IDs["base"] != "sha256:base1aaaaaaaaaaaa" {
			t.Fatalf("run %s runner images %+v", sr.Run.RunID, sr.RunnerImages)
		}
	}
	dirs, _ := filepath.Glob(filepath.Join(rw.coord.st.Root(), rw.m.ExpID, "slots", "*", "runs", runs[0].Run.RunID, "remote", "worker.json"))
	if len(dirs) != 1 {
		t.Fatalf("worker.json of %s: %v", runs[0].Run.RunID, dirs)
	}
	if b, _ := os.ReadFile(dirs[0]); !strings.Contains(string(b), `"images"`) || !strings.Contains(string(b), "sha256:base1") {
		t.Fatalf("worker.json without the runner's image ids: %s", b)
	}
	if c := instrument(t, rw.m, runs, verdict.InstrumentRunnerImages); c == nil || c.Status != schemas.InstrumentPass {
		t.Fatalf("runner_images %+v", c)
	}
	mixed := append([]schemas.SealedRun(nil), runs...)
	mixed[0].RunnerImages = &schemas.RunnerImages{IDs: map[string]string{"base": "sha256:base2bbbbbbbbbbbb"}}
	c := instrument(t, rw.m, mixed, verdict.InstrumentRunnerImages)
	if c == nil || c.Status != schemas.InstrumentWarn || !strings.Contains(c.Detail, "base -> ") ||
		!strings.Contains(c.Detail, "sha256:base1aaaaaaa") || !strings.Contains(c.Detail, "sha256:base2bbbbbbb") {
		t.Fatalf("mixed image ids: %+v", c)
	}
	missing := append([]schemas.SealedRun(nil), runs...)
	missing[0].RunnerImages = nil
	if c := instrument(t, rw.m, missing, verdict.InstrumentRunnerImages); c == nil || c.Status != schemas.InstrumentUnknown || !strings.Contains(c.Detail, "1 of") {
		t.Fatalf("missing record: %+v", c)
	}
	// Not a deferred manifest: no such instrument.
	plain := *rw.m
	plain.Gateway = &schemas.GatewayManifest{Name: "fake"}
	if c := instrument(t, &plain, runs, verdict.InstrumentRunnerImages); c != nil {
		t.Fatalf("non-deferred manifest got %+v", c)
	}
}

// TestRemoteDeferredGatewayImageRefusals: adopting the empty image maps does
// not weaken the lock. A lock that pins the base image by registry digest
// refuses a runner whose image carries another digest, and a deferred
// manifest that did freeze suite_images keeps the exact comparison.
func TestRemoteDeferredGatewayImageRefusals(t *testing.T) {
	t.Run("lock-pinned digest differs", func(t *testing.T) {
		pin := func(s *remoteSide, digest string) {
			env := digestEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, s.b.w}, map[string]string{testBaseImage: "sha256:base1aaaaaaaaaaaa"}},
				map[string][]string{testBaseImage: {"bench/base@" + digest}}}
			reg := *s.b.reg
			reg.Environments = []schemas.Environment{env}
			fps, err := suitepkg.Fingerprints(s.b.suite, FingerprintOptions(context.Background(), &reg, s.b.suite))
			if err != nil {
				t.Fatal(err)
			}
			s.b.suite.Lock.Fingerprints = fps
		}
		rw := newDeferredImageWorld(t, func(cs *remoteSide) { pin(cs, "sha256:d1") })
		done := rw.attach(t)
		side := imageSide(t, map[string]string{testBaseImage: "sha256:base1aaaaaaaaaaaa"},
			map[string][]string{testBaseImage: {"bench/base@sha256:d2"}})
		pin(side, "sha256:d1") // the same lock file on the runner
		side.b.reg.Environments = []schemas.Environment{digestEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, side.b.w},
			map[string]string{testBaseImage: "sha256:base1aaaaaaaaaaaa"}}, map[string][]string{testBaseImage: {"bench/base@sha256:d2"}}}}
		w := rw.worker(t, "w1", side)
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease")
		}
		if res := w.Work(context.Background(), l); res != nil {
			t.Fatalf("worker ran a lease whose lock pins another digest: %+v", res)
		}
		if side.b.w.count("exec") != 0 {
			t.Fatal("agent ran on a refused lease")
		}
		journal, err := os.ReadFile(filepath.Join(rw.c.opts.StagingDir, rw.m.ExpID+".jsonl"))
		if err != nil || !strings.Contains(string(journal), "image changed (base)") {
			t.Fatalf("journal %s %v", journal, err)
		}
		if sum := done(); sum.Sealed != 0 {
			t.Fatalf("summary %+v", sum)
		}
	})
	t.Run("manifest with suite_images keeps the exact check", func(t *testing.T) {
		m := &schemas.Manifest{Gateway: &schemas.GatewayManifest{Name: "deferred", DeferredToRunner: true},
			SuiteImages: map[string]string{"base": "sha256:server"}}
		local := &schemas.Manifest{SuiteImages: map[string]string{"base": "sha256:runner"}}
		adoptUnrepeatable(local, m)
		if local.SuiteImages["base"] != "sha256:runner" {
			t.Fatalf("a manifest with suite_images had them adopted: %v", local.SuiteImages)
		}
		if d := manifestDiff(local, m); !strings.Contains(d, "suite_images") {
			t.Fatalf("manifestDiff %q does not name suite_images", d)
		}
		hosted := &schemas.Manifest{Gateway: &schemas.GatewayManifest{Name: "deferred", DeferredToRunner: true}}
		local = &schemas.Manifest{SuiteImages: map[string]string{"base": "sha256:runner"}, TaskImages: map[string]string{"t01": "sha256:t"}}
		adoptUnrepeatable(local, hosted)
		if local.SuiteImages != nil || local.TaskImages != nil {
			t.Fatalf("hosted plan: image maps not adopted: %v %v", local.SuiteImages, local.TaskImages)
		}
	})
}
