package main

import (
	"sync"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// noDockerOnce logs the no-Docker line once per process.
var noDockerOnce sync.Once

// serverJanitor is the janitor a scheduler of this process uses, or nil
// when the process cannot reach Docker (fix wave 3, N218): the hosted
// server image has no docker binary, so a sweep would only fail on
// "docker ps". Leftover containers and stale run keys are then cleaned by
// the workers' own hygiene (runnerhygiene.go). The scheduler skips a nil
// janitor.
func serverJanitor(gw schemas.Gateway, docker utils.Docker, logger schemas.Logger) *engine.Janitor {
	if !docker.Available() {
		if logger != nil {
			noDockerOnce.Do(func() { logger.Info("no Docker on this server: leftover containers are cleaned on the workers") })
		}
		return nil
	}
	return engine.NewJanitor(gw, docker, logger)
}
