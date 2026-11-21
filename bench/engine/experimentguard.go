package engine

import "github.com/maximhq/bifrost/bench/schemas"

// WithExperimentGuard checks every experiment before the planner reads any
// file it names (Tenancy area: a hosted org's planner refuses suite refs and
// agent, policy and matrix paths that leave the org's project folder,
// tenancy.CheckExperimentPaths). Nil checks nothing.
func WithExperimentGuard(guard func(*schemas.Experiment) error) PlannerOption {
	return func(p *Planner) { p.guard = guard }
}

// WithGatewayDeferredToRunner plans without a gateway preflight and records
// the manifest's gateway as deferred to the runners (Tenancy area: a hosted
// server runs no agent and opens no gateway). Every runner preflights its own
// gateway when it re-plans the experiment and again before each slot,
// refuses the lease on a failure, and uploads the record with each run; the
// gateway_hygiene instrument reads those records. Only the hosted server's
// planner sets it: a runner's planner never does.
func WithGatewayDeferredToRunner() PlannerOption {
	return func(p *Planner) { p.deferGateway = true }
}

// deferredGateway is the manifest's gateway record when the preflight is the
// runners'. It carries no time or identity, so every plan of the same
// experiment records the same value.
func deferredGateway() *schemas.GatewayManifest {
	return &schemas.GatewayManifest{Name: "deferred", DeferredToRunner: true}
}

// WithFilesConfinedTo makes the planner open the files an agent config
// names (notes.user_file) only when they resolve, symlinks included, inside
// root (Tenancy area: the experiment guard sees only top-level refs; an
// arm's resolved config and its overrides name more files, audit 4-B-14).
// Empty confines nothing.
func WithFilesConfinedTo(root string) PlannerOption {
	return func(p *Planner) { p.confine = root }
}

// WithAgentLibrary layers the built-in library root lib (suite.LibraryDir)
// under the project: an agent config path agents/<file> that the project
// does not hold resolves to lib/agents/<file>, read-only, and the files a
// library config names resolve inside lib. Empty layers nothing.
func WithAgentLibrary(lib string) PlannerOption {
	return func(p *Planner) { p.library = lib }
}
