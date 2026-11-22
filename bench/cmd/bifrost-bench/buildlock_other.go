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
