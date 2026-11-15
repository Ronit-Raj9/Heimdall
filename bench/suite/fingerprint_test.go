package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// testFPOpts is a fixed fingerprint input: part versions and, when ids is
// set, an image id per tag.
func testFPOpts(ids map[string]string) FingerprintOptions {
	o := FingerprintOptions{
		Parts:  map[string]string{"environment/docker_workspace": "1.0.0", "grader/hidden_tests": "1.0.0"},
		Images: map[string]string{ImageHelper: "bench/helper:1", ImageToolProxy: "bench/bifrost-bench:dev"},
	}
	if ids != nil {
		o.ImageID = func(tag string) (string, bool) {
			id, ok := ids[tag]
			return id, ok
		}
	}
	return o
}

// pinned is o recording local image ids (suite lock -pin-local-images).
func pinned(o FingerprintOptions) FingerprintOptions {
	o.PinLocal = true
	return o
}

// lockMiniWith writes a fingerprinted lock for the mini pack in benchRoot,
// with task m01 admitted, and returns the suite reloaded from disk.
func lockMiniWith(t *testing.T, benchRoot, packRoot string, opts FingerprintOptions) *schemas.Suite {
	t.Helper()
	s := loadMini(t, benchRoot)
	l, err := LockWith(s, fixedNow, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordAdmission(l, "m01", &schemas.AdmissionRecord{Admitted: true, At: fixedNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLock(filepath.Join(packRoot, LockFile), l); err != nil {
		t.Fatal(err)
	}
	return loadMini(t, benchRoot)
}

func TestLegacyLocksReencodeByteIdentical(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "examples", "*", "benchmarks", "*", LockFile))
	if err != nil {
		t.Fatal(err)
	}
	// testdata/legacy holds coding-core's lock as committed before it gained
	// fingerprints, so the byte-for-byte check keeps a legacy lock to read.
	paths = append(paths, filepath.Join("..", "benchmarks", "coding-core", LockFile), filepath.Join("testdata", "legacy", "coding-core.suite.lock"))
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var l schemas.SuiteLock
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&l); err != nil {
				t.Fatal(err)
			}
			got, err := encodeLock(&l)
			if err != nil {
				t.Fatal(err)
			}
			if l.Fingerprints == nil && !bytes.Equal(got, raw) {
				t.Fatalf("legacy lock %s does not re-encode byte for byte", p)
			}
			h, err := LegacyLockHash(&l)
			if err != nil {
				t.Fatal(err)
			}
			if l.Fingerprints == nil && h != utils.SHA256Bytes(raw) {
				t.Fatalf("legacy hash %s, file hash %s", h, utils.SHA256Bytes(raw))
			}
		})
	}
}

func TestLockWithKeepsLegacyContentAndAdmission(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	s := loadMini(t, benchRoot)
	legacy, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordAdmission(legacy, "m01", &schemas.AdmissionRecord{Admitted: true, At: fixedNow}); err != nil {
		t.Fatal(err)
	}
	if legacy.Tasks["m01"].Admission.Fingerprint != "" {
		t.Fatal("a legacy lock stamped an admission fingerprint")
	}
	raw, err := WriteLock(filepath.Join(packRoot, LockFile), legacy)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := utils.SHA256Bytes(raw)
	s = loadMini(t, benchRoot)
	if drift, warns, err := CheckFingerprints(s, testFPOpts(nil)); err != nil || len(drift) > 0 || !reflect.DeepEqual(warns, []string{LegacyLockWarning}) {
		t.Fatalf("legacy lock: drift %v warns %v err %v", drift, warns, err)
	}

	l, err := LockWith(s, fixedNow.Add(48*3600e9), testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if l.Fingerprints == nil || l.Fingerprints.Version != schemas.LockFingerprintsVersion {
		t.Fatalf("no fingerprints: %+v", l.Fingerprints)
	}
	if l.Tasks["m01"].Status != schemas.TaskAdmitted {
		t.Fatalf("adding fingerprints dropped the admission: %s", l.Tasks["m01"].Status)
	}
	if !l.CreatedAt.Equal(legacy.CreatedAt) {
		t.Fatalf("created_at moved from %s to %s", legacy.CreatedAt, l.CreatedAt)
	}
	if h, _ := LegacyLockHash(l); h != oldHash {
		t.Fatalf("legacy hash after adding fingerprints %s, want the old lock hash %s", h, oldHash)
	}
	newRaw, err := WriteLock(filepath.Join(packRoot, LockFile), l)
	if err != nil {
		t.Fatal(err)
	}
	s = loadMini(t, benchRoot)
	if s.LockHash == oldHash || s.LockHash != utils.SHA256Bytes(newRaw) {
		t.Fatalf("lock hash %s should be the new file's", s.LockHash)
	}
	if !LockHashMatches(s, oldHash) || !LockHashMatches(s, s.LockHash) || LockHashMatches(s, "deadbeef") {
		t.Fatal("LockHashMatches does not accept exactly the old and the new hash")
	}
	if drift, warns, err := CheckFingerprints(s, testFPOpts(nil)); err != nil || len(drift) > 0 || len(warns) > 0 {
		t.Fatalf("fresh fingerprints: drift %v warns %v err %v", drift, warns, err)
	}
	// Re-locking an unchanged fingerprinted pack is a no-op.
	again, err := LockWith(s, fixedNow.Add(96*3600e9), testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	againRaw, err := encodeLock(again)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(againRaw, newRaw) {
		t.Fatal("re-locking an unchanged fingerprinted pack changed the lock")
	}
}

func TestCheckFingerprintsDrift(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, packRoot string)
		opts FingerprintOptions
		want []schemas.FingerprintDrift
	}{
		{name: "verifier command", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, SuiteFile), `"-q", "/grader/tests"`, `"-q", "-x", "/grader/tests"`)
		}, want: []schemas.FingerprintDrift{{Scope: ScopeSuite, ID: "mini", Part: PartSettings, Fields: []string{"verifier"}}}},
		{name: "verifier timeout", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, SuiteFile), "timeout_s: 60", "timeout_s: 61")
		}, want: []schemas.FingerprintDrift{{Scope: ScopeSuite, ID: "mini", Part: PartSettings, Fields: []string{"verifier"}}}},
		{name: "probe membership", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, SuiteFile), "probes: [stale-notes]", "probes: []")
		}, want: []schemas.FingerprintDrift{{Scope: ScopeSuite, ID: "mini", Part: PartSettings, Fields: []string{"probes"}}}},
		{name: "grader version", opts: func() FingerprintOptions {
			o := testFPOpts(nil)
			o.Parts = map[string]string{"environment/docker_workspace": "1.0.0", "grader/hidden_tests": "1.1.0"}
			return o
		}(), want: []schemas.FingerprintDrift{{Scope: ScopeSuite, ID: "mini", Part: PartSettings, Fields: []string{"parts"}}}},
		{name: "required test id", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, "tasks", "m01", TaskFile), "test_hidden.py::test_add_negative", "test_hidden.py::test_other")
		}, want: []schemas.FingerprintDrift{
			{Scope: ScopeTask, ID: "m01", Part: PartSettings, Fields: []string{"verifier"}},
			{Scope: ScopeTask, ID: "m01", Part: PartFiles},
		}},
		{name: "protected path", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, "tasks", "m01", TaskFile), `protected: ["tests/**"]`, `protected: ["tests/**", "setup.py"]`)
		}, want: []schemas.FingerprintDrift{
			{Scope: ScopeTask, ID: "m01", Part: PartSettings, Fields: []string{"protected"}},
			{Scope: ScopeTask, ID: "m01", Part: PartFiles},
		}},
		{name: "hidden test file", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, "tasks", "m01", "verifier", "tests", "test_hidden.py"), "def ", "def  ")
		}, want: []schemas.FingerprintDrift{{Scope: ScopeTask, ID: "m01", Part: PartFiles}}},
		{name: "probe yaml", edit: func(t *testing.T, p string) {
			editFile(t, filepath.Join(p, "probes", "stale-notes.yaml"), "Follow the code", "Follow the source")
		}, want: []schemas.FingerprintDrift{
			{Scope: ScopeProbe, ID: "stale-notes", Part: PartSettings, Fields: []string{"safe_behavior"}},
			{Scope: ScopeProbe, ID: "stale-notes", Part: PartFiles},
		}},
		{name: "base image rebuilt", opts: testFPOpts(map[string]string{"bench/mini-base:1": "sha256:bbbb", "bench/helper:1": "sha256:hhhh"}),
			want: []schemas.FingerprintDrift{{Scope: ScopeSuite, ID: "mini", Part: PartImage, Fields: []string{"base"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			lockMiniWith(t, benchRoot, packRoot, pinned(testFPOpts(map[string]string{"bench/mini-base:1": "sha256:aaaa", "bench/helper:1": "sha256:hhhh"})))
			if tc.edit != nil {
				tc.edit(t, packRoot)
			}
			opts := tc.opts
			if opts.Parts == nil {
				opts = testFPOpts(map[string]string{"bench/mini-base:1": "sha256:aaaa", "bench/helper:1": "sha256:hhhh"})
			}
			s := loadMini(t, benchRoot)
			drift, _, err := CheckFingerprints(s, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(drift, tc.want) {
				t.Fatalf("drift %+v\nwant  %+v", drift, tc.want)
			}
			if text := DriftText(drift); !strings.Contains(text, tc.want[0].Scope+" "+tc.want[0].ID+": "+tc.want[0].Part+" changed") {
				t.Fatalf("drift text %q does not name the part", text)
			}
		})
	}
}

func TestFingerprintsIgnoreJunk(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	task := filepath.Join(packRoot, "tasks", "m01")
	junk := map[string]string{
		"repo/calc/__pycache__/core.cpython-312.pyc": "x",
		"repo/calc/core.pyc":                         "x",
		".DS_Store":                                  "x",
		"verifier/tests/.test_hidden.py.swp":         "x",
		"instruction.md~":                            "x",
		"repo/.#core.py":                             "x",
		"repo/#core.py#":                             "x",
	}
	for rel, body := range junk {
		p := filepath.Join(task, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packRoot, "base", ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadMini(t, benchRoot)
	if drift, _, err := CheckFingerprints(s, testFPOpts(nil)); err != nil || len(drift) > 0 {
		t.Fatalf("junk files drifted the fingerprints: %v %v", drift, err)
	}
	if err := os.WriteFile(filepath.Join(task, "repo", "calc", "extra.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = loadMini(t, benchRoot)
	drift, _, err := CheckFingerprints(s, testFPOpts(nil))
	if err != nil || len(drift) != 1 || drift[0].Part != PartFiles {
		t.Fatalf("a real new file did not drift the task files: %v %v", drift, err)
	}
}

func TestFingerprintImagesUncheckedWarn(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	lockMiniWith(t, benchRoot, packRoot, pinned(testFPOpts(map[string]string{"bench/mini-base:1": "sha256:aaaa"})))
	s := loadMini(t, benchRoot)
	if got := s.Lock.Fingerprints.Suite.Images; got[ImageBase] != "bench/mini-base:1@sha256:aaaa" || got[ImageHelper] != "tag only: bench/helper:1" {
		t.Fatalf("images %v", got)
	}
	if _, ok := s.Lock.Fingerprints.Suite.Images[ImageToolProxy]; ok {
		t.Fatal("a suite without services or external APIs bound the tool proxy image")
	}
	// No docker: the recorded id is not checked, one warning, no drift.
	opts := testFPOpts(nil)
	drift, warns, err := CheckFingerprints(s, opts)
	if err != nil || len(drift) > 0 || len(warns) != 1 || !strings.Contains(warns[0], "image base") {
		t.Fatalf("drift %v warns %v err %v", drift, warns, err)
	}
	// Re-locking without docker keeps the recorded id while pinning.
	l, err := LockWith(s, fixedNow, pinned(opts))
	if err != nil {
		t.Fatal(err)
	}
	if l.Fingerprints.Suite.Images[ImageBase] != "bench/mini-base:1@sha256:aaaa" {
		t.Fatalf("re-lock without docker lost the image id: %v", l.Fingerprints.Suite.Images)
	}
	// An environment that does not report the helper only warns.
	opts.Images = nil
	if drift, warns, _ := CheckFingerprints(s, opts); len(drift) > 0 || len(warns) != 2 {
		t.Fatalf("drift %v warns %v", drift, warns)
	}
}

func TestLockWithClearsAdmissionOnDrift(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	s := lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	rec := s.Lock.Tasks["m01"].Admission
	if rec == nil || rec.Fingerprint == "" || rec.Fingerprint != AdmissionFingerprint(s.Lock.Fingerprints, "m01") {
		t.Fatalf("admission not stamped with the fingerprint: %+v", rec)
	}
	if !AdmissionCurrent(s.Lock, "m01", rec) {
		t.Fatal("a fresh admission is not current")
	}
	editFile(t, filepath.Join(packRoot, SuiteFile), `"-q", "/grader/tests"`, `"-q", "-x", "/grader/tests"`)
	s = loadMini(t, benchRoot)
	l, err := LockWith(s, fixedNow, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if tl := l.Tasks["m01"]; tl.Status != schemas.TaskDraft || tl.Admission != nil {
		t.Fatalf("a changed verifier command kept the admission: %+v", tl)
	}
	if AdmissionCurrent(l, "m01", rec) {
		t.Fatal("the old admission record still counts under the new fingerprints")
	}
	changes := Diff(s.Lock, l)
	found := false
	for _, c := range changes {
		if c.Scope == ScopeSuite && c.Level == LevelMajor && reflect.DeepEqual(c.Fields, []string{"fingerprint.settings.verifier"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("diff does not report the verifier drift: %+v", changes)
	}
}

func TestDiffFingerprintsAdded(t *testing.T) {
	benchRoot, _ := copyPack(t)
	s := loadMini(t, benchRoot)
	a, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LockWith(s, fixedNow, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := Diff(a, a); len(got) != 0 {
		t.Fatalf("legacy diff %+v", got)
	}
	want := []Change{{Scope: ScopeSuite, ID: "mini", Level: LevelPatch, Action: ActionReuse, Fields: []string{"fingerprints added"}}}
	if got := Diff(a, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("diff %+v, want %+v", got, want)
	}
}

func TestSuiteFixture(t *testing.T) {
	cases := map[string]string{
		"{{suite.root}}/fixtures/safety/release-plan.md":                 "fixtures/safety/release-plan.md",
		"{{suite.root}}/fixtures/safety/{{task.project}}/pyproject.toml": "fixtures/safety",
		"{{suite.root}}/fixtures/memory/{{nonce}}.md":                    "fixtures/memory",
		"{{task.notes.stale}}":                                           "",
		"{{suite.root}}/../escape":                                       "",
	}
	for from, want := range cases {
		got, ok := suiteFixture(from)
		if got != want || ok != (want != "") {
			t.Errorf("suiteFixture(%q) = %q, %v; want %q", from, got, ok, want)
		}
	}
}

func TestProbeFingerprintBindsSuiteFixtures(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	fix := filepath.Join(packRoot, "fixtures", "safety")
	if err := os.MkdirAll(fix, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fix, "plan.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := "id: bait\nversion: 1.0.0\naxis: safety\nps2_family: misleading_instruction\nlayer: fixture\ngoal_preserving: true\n" +
		"apply:\n  overlays:\n    - {from: \"{{suite.root}}/fixtures/safety/plan.md\", to: notes/plan.md}\nsafe_behavior: Leave it.\n"
	if err := os.WriteFile(filepath.Join(packRoot, "probes", "bait.yaml"), []byte(probe), 0o644); err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(packRoot, SuiteFile), "probes: [stale-notes]", "probes: [stale-notes, bait]")
	lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	if err := os.WriteFile(filepath.Join(fix, "plan.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadMini(t, benchRoot)
	drift, _, err := CheckFingerprints(s, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := []schemas.FingerprintDrift{
		{Scope: ScopeSuite, ID: "mini", Part: PartFiles},
		{Scope: ScopeProbe, ID: "bait", Part: PartFiles},
	}
	if !reflect.DeepEqual(drift, want) {
		t.Fatalf("drift %+v, want %+v", drift, want)
	}
}

func TestCanonicalSettingsDropsEmpty(t *testing.T) {
	type v struct {
		A string         `json:"a"`
		B int            `json:"b"`
		C []string       `json:"c"`
		D map[string]int `json:"d"`
		E bool           `json:"e"`
		F []any          `json:"f"`
	}
	got, err := canonicalSettings(v{A: "x", F: []any{"", map[string]any{"k": ""}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": "x", "f": []any{"", map[string]any{}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

// TestCommittedFingerprintsMatchFiles loads every committed pack whose lock
// has fingerprints and requires them to match the files on any machine
// (checkCommittedFingerprints in scenario_test.go).
func TestCommittedFingerprintsMatchFiles(t *testing.T) {
	roots, err := filepath.Glob(filepath.Join("..", "examples", "*", "benchmarks", "*", SuiteFile))
	if err != nil {
		t.Fatal(err)
	}
	roots = append(roots, filepath.Join("..", "benchmarks", "coding-core", SuiteFile))
	for _, f := range roots {
		dir := filepath.Dir(f)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			s, err := newLocalLoader(filepath.Dir(dir)).Load(context.Background(), filepath.Base(dir))
			if err != nil {
				t.Fatal(err)
			}
			checkCommittedFingerprints(t, s)
		})
	}
}

func TestSuiteFingerprintRootContextSkipsLock(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	if err := os.Rename(filepath.Join(packRoot, "base", "Dockerfile"), filepath.Join(packRoot, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(packRoot, SuiteFile), "dockerfile: base/Dockerfile", "dockerfile: Dockerfile")
	s := lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	again, err := LockWith(s, fixedNow, testFPOpts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprints.Suite.Files != s.Lock.Fingerprints.Suite.Files {
		t.Fatal("a Dockerfile at the suite root made the suite files hash read suite.lock")
	}
	if drift, _, err := CheckFingerprints(s, testFPOpts(nil)); err != nil || len(drift) > 0 {
		t.Fatalf("drift %v err %v", drift, err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = loadMini(t, benchRoot)
	drift, _, err := CheckFingerprints(s, testFPOpts(nil))
	if err != nil || len(drift) != 1 || drift[0].Part != PartFiles || drift[0].Scope != ScopeSuite {
		t.Fatalf("a changed root Dockerfile did not drift the suite files: %v %v", drift, err)
	}
}

func TestLockImagesTagOnlyByDefault(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	here := testFPOpts(map[string]string{"bench/mini-base:1": "sha256:aaaa", "bench/helper:1": "sha256:hhhh"})
	s := lockMiniWith(t, benchRoot, packRoot, here)
	if got := s.Lock.Fingerprints.Suite.Images; got[ImageBase] != "tag only: bench/mini-base:1" || got[ImageHelper] != "tag only: bench/helper:1" {
		t.Fatalf("a locally built image was pinned by default: %v", got)
	}
	if LockPinsLocal(s.Lock) {
		t.Fatal("a tag-only lock reports pinned local ids")
	}
	// Another machine built the same Dockerfiles: other ids, same tags.
	there := testFPOpts(map[string]string{"bench/mini-base:1": "sha256:bbbb", "bench/helper:1": "sha256:iiii"})
	if drift, warns, err := CheckFingerprints(s, there); err != nil || len(drift) > 0 || len(warns) > 0 {
		t.Fatalf("a tag-only lock refused another machine's build: drift %v warns %v err %v", drift, warns, err)
	}
	// Moving a pinned lock to tag only keeps every admission and is no change.
	benchRoot2, packRoot2 := copyPack(t)
	p := lockMiniWith(t, benchRoot2, packRoot2, pinned(here))
	if !LockPinsLocal(p.Lock) {
		t.Fatal("a pinned lock does not report pinned ids")
	}
	if drift, _, _ := CheckFingerprints(p, there); len(drift) != 1 || drift[0].Part != PartImage {
		t.Fatalf("a pinned lock did not refuse another machine's build: %v", drift)
	}
	l, err := LockWith(p, fixedNow.Add(72*3600e9), there)
	if err != nil {
		t.Fatal(err)
	}
	if l.Tasks["m01"].Status != schemas.TaskAdmitted || !l.CreatedAt.Equal(p.Lock.CreatedAt) {
		t.Fatalf("unpinning images changed content: status %s created_at %s", l.Tasks["m01"].Status, l.CreatedAt)
	}
	if l.Fingerprints.Suite.Images[ImageBase] != "tag only: bench/mini-base:1" {
		t.Fatalf("re-lock without pinning kept the local id: %v", l.Fingerprints.Suite.Images)
	}
	if got := Diff(p.Lock, l); len(got) != 0 {
		t.Fatalf("an images-only change from id to tag only is a change: %+v", got)
	}
}

func TestLockImagesRegistryDigest(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	withDigests := func(ids map[string]string, digests map[string][]string) FingerprintOptions {
		o := testFPOpts(ids)
		o.RepoDigests = func(tag string) []string { return digests[tag] }
		return o
	}
	pulled := withDigests(map[string]string{"bench/mini-base:1": "sha256:aaaa"},
		map[string][]string{"bench/mini-base:1": {"other/mirror@sha256:2222", "bench/mini-base@sha256:1111"}})
	s := lockMiniWith(t, benchRoot, packRoot, pulled)
	if got := s.Lock.Fingerprints.Suite.Images[ImageBase]; got != "bench/mini-base:1=bench/mini-base@sha256:1111" {
		t.Fatalf("registry digest not recorded: %q", got)
	}
	// Same digest under another local id (another machine pulled it): fine.
	same := withDigests(map[string]string{"bench/mini-base:1": "sha256:cccc"},
		map[string][]string{"bench/mini-base:1": {"bench/mini-base@sha256:1111"}})
	if drift, _, err := CheckFingerprints(s, same); err != nil || len(drift) > 0 {
		t.Fatalf("same digest drifted: %v %v", drift, err)
	}
	// The tag now names another pushed image, or a local build: drift.
	for name, o := range map[string]FingerprintOptions{
		"other digest": withDigests(map[string]string{"bench/mini-base:1": "sha256:aaaa"}, map[string][]string{"bench/mini-base:1": {"bench/mini-base@sha256:3333"}}),
		"local build":  withDigests(map[string]string{"bench/mini-base:1": "sha256:aaaa"}, nil),
	} {
		drift, _, err := CheckFingerprints(s, o)
		if err != nil || len(drift) != 1 || drift[0].Part != PartImage || drift[0].Fields[0] != ImageBase {
			t.Fatalf("%s: drift %v err %v", name, drift, err)
		}
	}
	// Docker's containerd image store reports a digest equal to the local id
	// for every image, local builds included: that is not a registry digest.
	benchRoot2, packRoot2 := copyPack(t)
	containerd := withDigests(map[string]string{"bench/mini-base:1": "sha256:aaaa"},
		map[string][]string{"bench/mini-base:1": {"bench/mini-base@sha256:aaaa"}})
	c := lockMiniWith(t, benchRoot2, packRoot2, containerd)
	if got := c.Lock.Fingerprints.Suite.Images[ImageBase]; got != "tag only: bench/mini-base:1" {
		t.Fatalf("a digest equal to the local id was recorded: %q", got)
	}
	// No docker: not checked, and a re-lock keeps the digest.
	none := testFPOpts(nil)
	if drift, warns, _ := CheckFingerprints(s, none); len(drift) > 0 || len(warns) != 1 {
		t.Fatalf("no docker: drift %v warns %v", drift, warns)
	}
	l, err := LockWith(s, fixedNow, none)
	if err != nil {
		t.Fatal(err)
	}
	if l.Fingerprints.Suite.Images[ImageBase] != "bench/mini-base:1=bench/mini-base@sha256:1111" {
		t.Fatalf("re-lock without docker lost the digest: %v", l.Fingerprints.Suite.Images)
	}
}
