package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// leakyPlatform is fakePlatform whose captured stdout prints an org secret
// and a member key, as an agent that dumps its environment would.
type leakyPlatform struct{ *fakePlatform }

func (f leakyPlatform) Download(ctx context.Context, b *Box, path string, max int64) ([]byte, error) {
	if path == stdoutFile+".cap" {
		return []byte("API_TOKEN=org-secret-value-7f3a\nMEMBER=sk-bf-member-9c2d\nKEY=sk-bf-runkey\n"), nil
	}
	return f.fakePlatform.Download(ctx, b, path, max)
}

// F5.2: copyStreams removes every runtime credential, not only the run key.
func TestCopyStreamsRedactsOrgSecrets(t *testing.T) {
	d, err := NewDriver(DriverConfig{Platform: leakyPlatform{newFakePlatform()}, Local: fakeLocal{}})
	if err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{
		Dir:     t.TempDir(),
		Key:     &schemas.RunKey{Value: "sk-bf-runkey", Members: []schemas.MemberKey{{ID: "m1", Value: "sk-bf-member-9c2d"}}},
		Secrets: map[string]string{"API_TOKEN": "org-secret-value-7f3a"},
	}
	if _, err := d.copyStreams(t.Context(), rc, &Box{ID: "box-1", Platform: "fake"}); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := environments.StreamPaths(rc)
	out, _ := os.ReadFile(stdout)
	for _, v := range []string{"org-secret-value-7f3a", "sk-bf-member-9c2d", "sk-bf-runkey"} {
		if strings.Contains(string(out), v) {
			t.Fatalf("stdout kept %q: %q", v, out)
		}
	}
}
