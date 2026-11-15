//go:build !unix

package schedule

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockNoop(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "s.lock"))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := tryLock(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}
