package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/bench/jobs"
)

// JobKind is the worker job that runs one saved run to the end.
const JobKind = "issuebench.run"

// JobParams are the job's params.
type JobParams struct {
	Run string `json:"run"`
}

// JobHandler runs a saved run as a job, so the existing job log stream
// carries its live log and a retried attempt resumes from the saved state.
// The org's manager is looked up per job by its org id.
func JobHandler(manager func(orgID string) (*Manager, error)) jobs.Handler {
	return func(ctx context.Context, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
		var p JobParams
		if err := json.Unmarshal(j.Params, &p); err != nil || p.Run == "" {
			return nil, fmt.Errorf("issuebench job: params need run")
		}
		m, err := manager(j.OrgID)
		if err != nil {
			return nil, err
		}
		if err := m.Run(ctx, p.Run, func(line string) { log.Printf("%s", line) }); err != nil {
			return nil, err
		}
		s, err := m.Load(p.Run)
		if err != nil {
			return nil, err
		}
		s.Log = nil
		return s, nil
	}
}
