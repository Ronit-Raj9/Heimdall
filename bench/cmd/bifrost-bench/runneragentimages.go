package main

// Agent images on the shared worker (fix wave 3, SEC-A, N180, N181). Every
// agent image the shared worker builds for an org, per task or per
// benchmark, is built with the org's policy docker (utils/dockerpolicy.go):
// an org-written Dockerfile runs only on the isolated builder, under the
// org's image namespace, never on the host daemon.

import (
	"io"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// sharedPlannerOptions are the options of an org's planner on the shared
// worker. Per-task agent images build with docker (the org's policy docker)
// and a planner that would otherwise fall back to a docker CLI without a
// policy refuses the build instead. Build output goes to out (nil discards
// it); a failed build's error carries its last lines either way.
func sharedPlannerOptions(docker utils.Docker, out io.Writer) []engine.PlannerOption {
	return []engine.PlannerOption{
		engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck(), engine.WithAgentLibrary(suite.LibraryDir()),
		engine.WithAgentImageBuilder(engine.DockerAgentImages{Docker: docker, Out: out}), engine.WithoutHostAgentBuilds(),
	}
}
