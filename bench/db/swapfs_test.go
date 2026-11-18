package db_test

import (
	"io/fs"
	"sync"
)

// swapFS is an fs.FS whose contents a test replaces between migrations.
type swapFS struct {
	mu sync.Mutex
	fs fs.FS
}

func (s *swapFS) Open(name string) (fs.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fs.Open(name)
}
