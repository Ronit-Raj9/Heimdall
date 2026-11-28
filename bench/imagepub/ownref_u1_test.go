package imagepub

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestValidOwnRef(t *testing.T) {
	id := strings.Repeat("c", 64)
	for ref, want := range map[string]bool{
		"org-o1/bench/s-base:" + id:          true,
		"org-o1/bench/s-base:1":              false, // a tag an org could guess
		"bench/s-base:" + id:                 false, // no org namespace
		"registry:5000/org-o1/bench/s:" + id: false,
		"org-o1/../bench/s:" + id:            false,
		"org-O1/bench/s:" + id:               false,
	} {
		if got := ValidOwnRef(ref); got != want {
			t.Errorf("%s: %v, want %v", ref, got, want)
		}
	}
}

// An org's own image is pushed to its namespace and never pinned in the
// platform manifest.
func TestPublishOwnSkipsManifest(t *testing.T) {
	s, marker := testServer(t)
	ref := "org-o1/bench/s-base:" + strings.Repeat("c", 64)
	rec := publish(t, s, ref, "tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sha256:"+hex64) {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(s.Manifest); err == nil {
		t.Fatal("an org image was written to the platform manifest")
	}
	calls, err := os.ReadFile(marker)
	if err != nil || !strings.Contains(string(calls), "registry-writer:5000/"+ref) {
		t.Fatalf("calls = %s, %v", calls, err)
	}
}

func TestPublishOwnClientChecksNames(t *testing.T) {
	c := &Client{}
	id := strings.Repeat("c", 64)
	for _, tc := range [][2]string{
		{"org-o2/bench/s-base:1", "org-o1/bench/s-base:" + id}, // another org's repository
		{"org-o1/bench/s-base:1", "org-o1/bench/s-base:1"},     // not an ID tag
	} {
		if _, err := c.PublishOwn(t.Context(), tc[0], tc[1]); err == nil {
			t.Errorf("%v: no error", tc)
		}
	}
}
