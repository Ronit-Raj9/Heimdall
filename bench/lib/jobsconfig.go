package lib

import "fmt"

// JobsConfig sizes the hosted job runner (INFRA_I4 item 2, docs/hosting.md).
// A hosted experiment.run job holds a job worker for the whole experiment,
// but only waits in its scheduler while runners work its slots, so a
// waiting experiment costs a goroutine and a heartbeat, not a CPU. Zero
// means the default; each key can be set with BENCH_JOBS_<KEY>.
type JobsConfig struct {
	// ExperimentLimit is how many experiments run at once on the whole
	// platform (default 64).
	ExperimentLimit int `yaml:"experiment_limit" json:"experiment_limit,omitempty"`
	// ExperimentPerOrg is how many of them one organization may hold
	// (default 8): the rest of its queue waits while other organizations'
	// experiments start (the claim is round robin across organizations).
	ExperimentPerOrg int `yaml:"experiment_per_org" json:"experiment_per_org,omitempty"`
	// Workers is the hosted runner's job workers, experiments included
	// (default ExperimentLimit + 8, so the other kinds always have room).
	Workers int `yaml:"workers" json:"workers,omitempty"`
}

// Defaults of JobsConfig.
const (
	DefaultExperimentLimit  = 64
	DefaultExperimentPerOrg = 8
	// jobsOpsHeadroom is how many workers the other job kinds keep.
	jobsOpsHeadroom = 8
)

// Resolved returns the sizes with defaults filled in.
func (c JobsConfig) Resolved() JobsConfig {
	if c.ExperimentLimit <= 0 {
		c.ExperimentLimit = DefaultExperimentLimit
	}
	if c.ExperimentPerOrg <= 0 {
		c.ExperimentPerOrg = min(DefaultExperimentPerOrg, c.ExperimentLimit)
	}
	if c.Workers <= 0 {
		c.Workers = c.ExperimentLimit + jobsOpsHeadroom
	}
	return c
}

// Validate refuses sizes that would starve the other job kinds or that
// contradict each other.
func (c JobsConfig) Validate() error {
	if c.ExperimentLimit < 0 || c.ExperimentPerOrg < 0 || c.Workers < 0 {
		return fmt.Errorf("jobs: experiment_limit, experiment_per_org and workers must not be negative")
	}
	r := c.Resolved()
	if r.ExperimentPerOrg > r.ExperimentLimit {
		return fmt.Errorf("jobs.experiment_per_org (%d) is above jobs.experiment_limit (%d)", r.ExperimentPerOrg, r.ExperimentLimit)
	}
	if r.Workers <= r.ExperimentLimit {
		return fmt.Errorf("jobs.workers (%d) must be above jobs.experiment_limit (%d): waiting experiments would leave no worker for other jobs", r.Workers, r.ExperimentLimit)
	}
	if r.Workers > 4096 {
		return fmt.Errorf("jobs.workers (%d) is above 4096", r.Workers)
	}
	return nil
}
