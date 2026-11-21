package environments

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Kubernetes area (X3): the environment driver seam (docs/kubernetes.md).
//
// A suite names its environment type (docker_workspace): a hardened
// workspace seeded from the task, one agent process, a trusted diff export.
// The driver is where that environment's containers live: on the local
// Docker daemon, or as one pod per run in a Kubernetes cluster. The engine
// drives a run's lifecycle through schemas.Environment exactly as before:
//
//	prepare      Create   workspace seeded, baseline commit, protected hashes
//	start agent  Exec     agent started with its observers, output captured
//	exec, stop   Exec     waited for under the wall budget, killed on expiry
//	collect      Exec     observer records written to the run folder
//	copy out     ExportDiff (and the optional scenario exports)
//	cleanup      Destroy  every object of the run removed, idempotent
//
// Driver adds what the planner needs to refuse, with a reason, what a driver
// cannot run, before anything is frozen. The Docker driver is today's
// docker_workspace environment, unchanged; it refuses nothing.

// Driver names: bench.yaml environment.driver and experiment
// environment.driver.
const (
	DriverDocker     = "docker"
	DriverKubernetes = "kubernetes"
)

// Driver is an environment that runs on one backend.
type Driver interface {
	schemas.Environment
	// Driver names the backend: DriverDocker or DriverKubernetes.
	Driver() string
	// Refusals lists, for one planned slot, every reason this driver cannot
	// run it, each one plain sentence; nil when it can.
	Refusals(s DriverSlot) []string
}

// DriverSlot is what a driver is asked about at plan time: one task under
// one condition (Probe nil for clean) for one agent config.
type DriverSlot struct {
	Experiment *schemas.Experiment
	Suite      *schemas.Suite
	Task       *schemas.Task
	Probe      *schemas.ProbeSpec
	Agent      *schemas.AgentConfig
}

// dockerDriver is the Docker driver: the docker_workspace environment with
// the driver methods added. Every method of the environment, optional
// interfaces included, is promoted unchanged, so Docker runs record exactly
// what they recorded before the seam.
type dockerDriver struct {
	*dockerWorkspace
}

// NewDockerDriver returns the Docker driver.
func NewDockerDriver(cfg DockerWorkspaceConfig) Driver {
	return &dockerDriver{dockerWorkspace: newDockerWorkspace(cfg)}
}

// Driver implements Driver.
func (d *dockerDriver) Driver() string { return DriverDocker }

// Refusals implements Driver. The Docker driver runs every slot the
// environment itself accepts.
func (d *dockerDriver) Refusals(DriverSlot) []string { return nil }

// Create implements schemas.Environment: a run whose experiment was frozen
// for another driver is refused before anything is created.
func (d *dockerDriver) Create(ctx context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	if err := CheckFrozenDriver(rc, DriverDocker); err != nil {
		return nil, err
	}
	return d.dockerWorkspace.Create(ctx, rc)
}

// DriverOf returns the driver name of env: its Driver() when it is a
// Driver, DriverDocker for any other environment (the docker_workspace
// environment registered directly, as tests and older callers do).
func DriverOf(env schemas.Environment) string {
	if d, ok := env.(Driver); ok {
		return d.Driver()
	}
	return DriverDocker
}

// Placement is a Driver that runs another driver's plan on other
// infrastructure, unchanged: the cloud sandbox drivers (bench/sandbox) run
// the docker_workspace plan (same images, same workspace seed, same diff
// export) in an organization's Modal, Daytona or E2B account. The manifest
// freezes the plan identity, so a server that planned an experiment for
// Docker and a worker that runs it in a sandbox agree on its hash; where it
// actually ran is recorded in the run folder (sandbox.json). Its refusals
// still apply when the experiment is planned.
type Placement interface {
	Driver
	// PlansAs is the driver whose plan this one runs (DriverDocker).
	PlansAs() string
}

// PlanIdentity is the driver name an experiment planned for env freezes:
// PlansAs for a Placement, DriverOf otherwise.
func PlanIdentity(env schemas.Environment) string {
	if p, ok := env.(Placement); ok {
		return p.PlansAs()
	}
	return DriverOf(env)
}

// FrozenDriver is the driver an experiment's manifest froze: its
// environment.driver, or docker when unset (every manifest recorded before
// the seam, and every Docker experiment since, which keeps their hashes).
func FrozenDriver(m *schemas.Manifest) string {
	if m == nil || m.Experiment.Environment == nil || m.Experiment.Environment.Driver == "" {
		return DriverDocker
	}
	return m.Experiment.Environment.Driver
}

// CheckFrozenDriver refuses a run of an experiment frozen for another
// driver: a resumed or remote run must run where the experiment was planned.
func CheckFrozenDriver(rc *schemas.RunContext, driver string) error {
	if rc == nil || rc.Manifest == nil {
		return nil
	}
	if want := FrozenDriver(rc.Manifest); want != driver {
		return fmt.Errorf("experiment %s was planned for the %s driver, but this bench runs the %s driver (environment.driver in bench.yaml); run it where it was planned or plan a new experiment",
			rc.Manifest.ExpID, want, driver)
	}
	return nil
}

// PlanDriver checks an experiment against the driver of the suite's
// environment and returns the choice to freeze into the manifest: nil for
// the Docker driver when the experiment names none (so the manifest hashes
// as before), else the driver that will run it. An experiment that asks for
// another driver than the configured one is refused, and so is every slot
// the driver cannot run, with all reasons at once, deduplicated.
func PlanDriver(envs []schemas.Environment, suite *schemas.Suite, exp *schemas.Experiment, slots []DriverSlot) (*schemas.EnvironmentChoice, error) {
	var env schemas.Environment
	for _, e := range envs {
		if suite != nil && e.GetName() == suite.Environment {
			env = e
		}
	}
	if env == nil {
		return exp.Environment, nil // checkParts reports the missing environment
	}
	driver := PlanIdentity(env)
	if exp.Environment != nil && exp.Environment.Driver != "" && exp.Environment.Driver != driver {
		return nil, fmt.Errorf("experiment asks for the %s driver, but this bench is configured for the %s driver (environment.driver in bench.yaml)", exp.Environment.Driver, driver)
	}
	if d, ok := env.(Driver); ok {
		seen := map[string]bool{}
		var reasons []string
		for _, s := range slots {
			s.Experiment = exp
			for _, r := range d.Refusals(s) {
				if !seen[r] {
					seen[r] = true
					reasons = append(reasons, r)
				}
			}
		}
		if len(reasons) > 0 {
			sort.Strings(reasons)
			return nil, fmt.Errorf("the %s driver cannot run this experiment: %s", driver, strings.Join(reasons, "; "))
		}
	}
	if exp.Environment != nil {
		return exp.Environment, nil
	}
	if driver == DriverDocker {
		return nil, nil
	}
	return &schemas.EnvironmentChoice{Driver: driver}, nil
}
