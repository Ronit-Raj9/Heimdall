package main

import (
	"context"
	"errors"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// newGateway builds the Bifrost gateway client. It is filled in when the
// gateways/bifrost package lands.
func newGateway(ctx context.Context, cfg *lib.Config, docker utils.Docker, logger schemas.Logger) (schemas.Gateway, error) {
	return nil, errors.New("the bifrost gateway client is not wired yet")
}
