package history

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"testing"

	"github.com/maximhq/bifrost/bench/agents"
	"github.com/maximhq/bifrost/bench/examples/frameworks"
)

// Every shipped built-in file's current content is a known version, so
// history_gen.go was regenerated (go generate ./agents/history) with it.
func TestCurrentBuiltinFilesAreKnown(t *testing.T) {
	for _, src := range []struct {
		fsys   fs.FS
		prefix string
	}{{agents.Files, "agents"}, {frameworks.Files, "examples/frameworks"}} {
		err := fs.WalkDir(src.fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(src.fsys, p)
			if err != nil {
				return err
			}
			s := sha256.Sum256(b)
			want := path.Join(src.prefix, p)
			for _, got := range Paths(hex.EncodeToString(s[:])) {
				if got == want {
					return nil
				}
			}
			t.Errorf("%s: current content not in history_gen.go; run go generate ./agents/history", want)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
