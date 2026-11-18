package suite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// W8 judge calibration: the calibration labels are hashed into the lock
// fingerprints only when present; records never make a lock drift, and a
// suite-root build context never reads the calibration folder.
func TestFingerprintCalibrationOnlyWhenPresent(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	if err := os.Rename(filepath.Join(packRoot, "base", "Dockerfile"), filepath.Join(packRoot, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(packRoot, SuiteFile), "dockerfile: base/Dockerfile", "dockerfile: Dockerfile")
	s := lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	if s.Lock.Fingerprints.Calibration != "" {
		t.Fatalf("a suite without labels has calibration %q", s.Lock.Fingerprints.Calibration)
	}
	raw, err := os.ReadFile(filepath.Join(packRoot, LockFile))
	if err != nil || strings.Contains(string(raw), `"calibration"`) {
		t.Fatalf("a lock without labels must encode as before: %v", err)
	}
	files, adm := s.Lock.Fingerprints.Suite.Files, AdmissionFingerprint(s.Lock.Fingerprints, "m01")

	dir := filepath.Join(packRoot, "calibration")
	if err := os.MkdirAll(filepath.Join(dir, "records", "cr_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	labels := filepath.Join(dir, "labels.jsonl")
	if err := os.WriteFile(labels, []byte(`{"id":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = loadMini(t, benchRoot)
	drift, warns, err := CheckFingerprints(s, testFPOpts(nil))
	if err != nil || len(drift) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "calibration labels changed") {
		t.Fatalf("labels after the lock: drift %v warns %v err %v", drift, warns, err)
	}
	s = lockMiniWith(t, benchRoot, packRoot, testFPOpts(nil))
	want, _ := utils.SHA256File(labels)
	if s.Lock.Fingerprints.Calibration != want || s.Lock.Fingerprints.Suite.Files != files {
		t.Fatalf("calibration %q (want %q), suite files changed: %v", s.Lock.Fingerprints.Calibration, want, s.Lock.Fingerprints.Suite.Files != files)
	}
	if got := AdmissionFingerprint(s.Lock.Fingerprints, "m01"); got == "" || got != adm {
		t.Fatal("the admission fingerprint must not depend on the labels, so labels never send a task back to draft")
	}
	// A record written beside the labels changes nothing.
	if err := os.WriteFile(filepath.Join(dir, "records", "cr_x", "record.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "records.log"), []byte("cr_x abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = loadMini(t, benchRoot)
	if drift, warns, err := CheckFingerprints(s, testFPOpts(nil)); err != nil || len(drift) != 0 || len(warns) != 0 {
		t.Fatalf("a record made the lock drift: %v %v %v", drift, warns, err)
	}
}
