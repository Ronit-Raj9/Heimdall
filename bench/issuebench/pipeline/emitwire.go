package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/jobs"
)

// What the emit lane (G05) needs from the pipeline, as small interfaces so
// this package compiles without it; the integrator wires the real ones.

// Checkout gives emit a working copy of repo at sha in dir; emit scrubs the
// history in place, so each candidate gets its own copy.
type Checkout interface {
	Checkout(ctx context.Context, repo, sha, dir string) error
}

// LayerBuilder builds the three image layers emit wrote into dir
// (Dockerfile.base, Dockerfile.env, Dockerfile, layers.json) before admit.
type LayerBuilder interface {
	BuildLayers(ctx context.Context, dir string) error
}

// Queuer queues one job of kind with params and returns its id.
type Queuer interface {
	Queue(ctx context.Context, kind string, params any) (string, error)
}

// AdmitKinds are the jobs queued, in order, for an emitted benchmark.
var AdmitKinds = []string{"suite.lint", "suite.lock", "suite.admit"}

// QueueAdmit queues lint, lock and admit for suite with params {"suite": id}.
func QueueAdmit(ctx context.Context, q Queuer, suite string) ([]string, error) {
	var ids []string
	for _, k := range AdmitKinds {
		id, err := q.Queue(ctx, k, map[string]string{"suite": suite})
		if err != nil {
			return ids, fmt.Errorf("queue %s: %w", k, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// JobCreator is the part of the job registry the adapter uses
// (handlers.JobsAPI and *jobs.Runner satisfy it).
type JobCreator interface {
	Create(ctx context.Context, n jobs.NewJob) (*jobs.Job, error)
}

// JobQueuer adapts the job registry to Queuer for one org.
type JobQueuer struct {
	Jobs      JobCreator
	OrgID     string
	CreatedBy string
}

// Queue implements Queuer.
func (q JobQueuer) Queue(ctx context.Context, kind string, params any) (string, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	by := q.CreatedBy
	if by == "" {
		by = "system"
	}
	j, err := q.Jobs.Create(ctx, jobs.NewJob{OrgID: q.OrgID, Kind: kind, Params: b, CreatedBy: by})
	if err != nil {
		return "", err
	}
	return j.ID, nil
}
