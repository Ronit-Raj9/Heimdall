package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

// AdmitRetry is how often a kind that found its target busy is queued
// again, and AdmitWait how long that goes on (tests shorten them).
var (
	AdmitRetry = 5 * time.Second
	AdmitWait  = 6 * time.Hour
)

// QueueAdmit queues lint, lock and admit for suite with params {"suite": id}.
// lock and admit share the target suite:<id>, and only one job per target
// may be queued or running: admit always finds lock still there, and a
// second accept finds the first one's admit. So a kind whose target is busy
// (jobs.ErrConflict) is queued, in order with the kinds after it, in the
// background as soon as the target is free. The ids are those queued now.
func QueueAdmit(ctx context.Context, q Queuer, suite string) ([]string, error) {
	var ids []string
	params := map[string]string{"suite": suite}
	for i, k := range AdmitKinds {
		id, err := q.Queue(ctx, k, params)
		if errors.Is(err, jobs.ErrConflict) {
			go queueWhenFree(context.WithoutCancel(ctx), q, AdmitKinds[i:], params)
			return ids, nil
		}
		if err != nil {
			return ids, fmt.Errorf("queue %s: %w", k, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func queueWhenFree(ctx context.Context, q Queuer, kinds []string, params map[string]string) {
	deadline := time.Now().Add(AdmitWait)
	for _, k := range kinds {
		for {
			_, err := q.Queue(ctx, k, params)
			if err == nil {
				break
			}
			if !errors.Is(err, jobs.ErrConflict) || time.Now().After(deadline) {
				return
			}
			time.Sleep(AdmitRetry)
		}
	}
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
