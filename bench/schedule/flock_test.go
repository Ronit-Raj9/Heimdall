//go:build unix

package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.lock")
	open := func() *os.File {
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	unlock, err := tryLock(open())
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	second := open()
	if _, err := tryLock(second); !errors.Is(err, errBusy) {
		t.Fatalf("second lock = %v, want errBusy", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	again, err := tryLock(open())
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	if err := again(); err != nil {
		t.Fatal(err)
	}
}
