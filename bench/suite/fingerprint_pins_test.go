package suite

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/imagepin"
)

// Library registry (Z13): a pins file in the suite folder makes the lock
// record the pushed digest, also on Docker's containerd store where the
// pushed digest equals the local id; a local image rebuilt after the push
// is never recorded under the pin; the pins file is outside the files hash.
func TestLockImagesLibraryPins(t *testing.T) {
	const pin = "reg.local:5001/library/mini/bench/mini-base@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const local = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const rebuilt = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	withID := func(id string, digests []string) FingerprintOptions {
		o := testFPOpts(map[string]string{"bench/mini-base:1": id})
		o.RepoDigests = func(tag string) []string {
			if tag == "bench/mini-base:1" {
				return digests
			}
			return nil
		}
		return o
	}
	withDigests := func(digests []string) FingerprintOptions { return withID(local, digests) }
	benchRoot, packRoot := copyPack(t)
	before := lockMiniWith(t, benchRoot, packRoot, withDigests(nil))
	files := before.Lock.Fingerprints.Suite.Files
	if err := imagepin.WritePins(filepath.Join(packRoot, imagepin.PinsFile), imagepin.Pins{"bench/mini-base:1": pin}); err != nil {
		t.Fatal(err)
	}
	pushed := withDigests([]string{"bench/mini-base@" + local, pin})
	s := lockMiniWith(t, benchRoot, packRoot, pushed)
	if got := s.Lock.Fingerprints.Suite.Images[ImageBase]; got != "bench/mini-base:1="+pin {
		t.Fatalf("pin not recorded: %q", got)
	}
	if s.Lock.Fingerprints.Suite.Files != files {
		t.Fatal("the pins file changed the suite's files fingerprint")
	}
	if drift, _, err := CheckFingerprints(s, pushed); err != nil || len(drift) > 0 {
		t.Fatalf("pushed image drifted: %v %v", drift, err)
	}
	// A worker that pulled the pin has it among its digests: no drift. One
	// that built its own copy does not: drift, refused.
	if drift, _, _ := CheckFingerprints(s, withDigests([]string{pin})); len(drift) > 0 {
		t.Fatalf("pulled pin drifted: %v", drift)
	}
	if drift, _, _ := CheckFingerprints(s, withID(rebuilt, []string{"bench/mini-base@" + rebuilt})); len(drift) != 1 {
		t.Fatalf("a local build passed a pinned lock: %v", drift)
	}
	// containerd store: the registry tag was removed, the id is the pin.
	if drift, _, _ := CheckFingerprints(s, withDigests(nil)); len(drift) > 0 {
		t.Fatalf("the pushed image without its registry tag drifted: %v", drift)
	}
	// Rebuilt after the push: the pin is not this image, recorded as it is.
	benchRoot2, packRoot2 := copyPack(t)
	os.WriteFile(filepath.Join(packRoot2, imagepin.PinsFile), []byte("bench/mini-base:1 "+pin+"\n"), 0o644)
	r := lockMiniWith(t, benchRoot2, packRoot2, withID(rebuilt, []string{"bench/mini-base@" + rebuilt}))
	if got := r.Lock.Fingerprints.Suite.Images[ImageBase]; got != "tag only: bench/mini-base:1" {
		t.Fatalf("a rebuilt image was recorded by a pin it does not match: %q", got)
	}
}
