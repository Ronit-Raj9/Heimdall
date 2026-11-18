package traceinbox

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Planted values no file under the inbox or a draft may hold.
const (
	plantedKey   = "sk-live1234567890abcdefXYZ"
	plantedEmail = "jane.doe@example.com"
)

var testNow = time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)

// waits records the retry waits a client asked for, without sleeping.
type waits struct {
	mu sync.Mutex
	d  []time.Duration
}

func (w *waits) sleep(_ context.Context, d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.d = append(w.d, d)
	return nil
}

// newTestInbox returns an inbox in a temporary project with a scenario
// suite of each draft kind under benchmarks/.
func newTestInbox(t *testing.T, cfg *Config, env map[string]string) (*Inbox, *waits) {
	t.Helper()
	root := t.TempDir()
	bench := filepath.Join(root, "benchmarks")
	for id, tmpl := range map[string]string{"support": scaffold.TemplateMCP, "tools": scaffold.TemplateToolUse, "answers": scaffold.TemplateQA} {
		files, err := scaffold.Suite(scaffold.SuiteOptions{ID: id, Template: tmpl})
		if err != nil {
			t.Fatal(err)
		}
		if err := scaffold.WriteFiles(filepath.Join(bench, id), files, false); err != nil {
			t.Fatal(err)
		}
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	ib := New(filepath.Join(root, Dir), cfg, bench)
	ib.ProjectRoot = root
	w := &waits{}
	ib.Sleep = w.sleep
	ib.Now = func() time.Time { return testNow }
	ib.Getenv = func(k string) string { return env[k] }
	return ib, w
}

// checkNoLeak walks dirs and fails on any planted value.
func checkNoLeak(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, leaked := range []string{plantedKey, plantedEmail, "s3cr3t-langfuse", "ls-key-123", "bf-token-9"} {
				if bytes.Contains(b, []byte(leaked)) {
					t.Errorf("%s holds %q", p, leaked)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func mustItems(t *testing.T, ib *Inbox) []Item {
	t.Helper()
	items, err := ib.List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	return items
}
