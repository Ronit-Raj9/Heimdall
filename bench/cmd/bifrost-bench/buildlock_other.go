//go:build !unix

package main

import (
	"context"
	"errors"
)

// lockOrgBuild: build sessions run on the Linux shared worker only.
func lockOrgBuild(ctx context.Context, path string) (func(), error) {
	return nil, errors.New("image build sessions need a Unix host")
}

// lockOrgBuildSlot: build sessions run on the Linux shared worker only.
func lockOrgBuildSlot(ctx context.Context, paths []string) (func(), int, error) {
	return nil, 0, errors.New("image build sessions need a Unix host")
}
