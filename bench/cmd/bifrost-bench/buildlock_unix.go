//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockOrgBuild takes the exclusive build lock of one org (a file lock, so
// it holds across the runner's build session processes), waiting until it
// is free or ctx ends. The kernel drops it when the process dies.
func lockOrgBuild(ctx context.Context, path string) (func(), error) {
	unlock, _, err := lockOrgBuildSlot(ctx, []string{path})
	return unlock, err
}

// lockOrgBuildSlot takes the first free lock of paths (one per build slot
// of an org), waiting until one is free or ctx ends. It returns the index
// of the lock taken. With one path it is lockOrgBuild.
func lockOrgBuildSlot(ctx context.Context, paths []string) (func(), int, error) {
	files := make([]*os.File, len(paths))
	closeAll := func(keep int) {
		for i, f := range files {
			if f != nil && i != keep {
				_ = f.Close()
			}
		}
	}
	for i, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			closeAll(-1)
			return nil, 0, fmt.Errorf("build lock: %w", err)
		}
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			closeAll(-1)
			return nil, 0, fmt.Errorf("build lock: %w", err)
		}
		files[i] = f
	}
	for {
		for i, f := range files {
			err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				closeAll(i)
				return func() {
					_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
					_ = f.Close()
				}, i, nil
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				closeAll(-1)
				return nil, 0, fmt.Errorf("build lock %s: %w", paths[i], err)
			}
		}
		select {
		case <-ctx.Done():
			closeAll(-1)
			return nil, 0, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
