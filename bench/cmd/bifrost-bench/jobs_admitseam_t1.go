package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// admitOnWorker proves one task of the org's project on a worker, the way
// hosted tasks.mine does: the readiness controls run as one bridge
// experiment on the worker's runner and readinessVerdict reads them. With
// record, an admitted verdict is written to the suite's lock; tracetask.build
// passes false, because its admission waits for a person's approval.
// Shared by repotask.build (lane T4) and tracetask.build (lane T1).
func admitOnWorker(ctx context.Context, env jobEnv, j *jobs.Job, suiteID, id string, log jobs.Logger, record bool) (hostedAdmitTask, string, error) {
	none := hostedAdmitTask{Task: id}
	l, err := bridgeLauncher(env)
	if err != nil {
		return none, "", err
	}
	if err := bridgeRequireRunner(l); err != nil {
		return none, "", err
	}
	st, err := snapshotSuite(ctx, env.App, suiteID)
	if err != nil {
		return none, "", err
	}
	if errs := staticErrors(ctx, st.suite, id); len(errs) > 0 {
		none.Reason = "static validation: " + errs[0]
		return none, "", nil
	}
	t := st.suite.TaskByID(id)
	if t == nil {
		return none, "", fmt.Errorf("task %s is not in suite %s", id, suiteID)
	}
	tasks := []*schemas.Task{t}
	m, err := l.Plan(ctx, readinessExperiment(suiteID, bridgeOracleAgent, bridgeNoopAgent, st.suite, tasks, schemas.SplitPublic, false))
	if err != nil {
		return none, "", fmt.Errorf("plan the admission run: %w", err)
	}
	if err := runExperiment(ctx, l, m, max(bridgeDefaultTimeout, readinessTimeout(len(m.Slots))), log); err != nil {
		return none, m.ExpID, err
	}
	runs, err := bridgeSealedRuns(ctx, env, m.ExpID)
	if err != nil {
		return none, m.ExpID, err
	}
	v := readinessVerdict(st.suite, tasks, runs, map[string]string{id: m.ExpID}, readinessFileChecks(env.App, st.suite))
	if len(v) != 1 {
		return none, m.ExpID, errors.New("admission gave no verdict")
	}
	if record && v[0].Admitted {
		if _, _, err := recordHostedAdmissions(ctx, env, st, v, m.ExpID, jobActor(j)); err != nil {
			return v[0], m.ExpID, err
		}
	}
	return v[0], m.ExpID, nil
}
