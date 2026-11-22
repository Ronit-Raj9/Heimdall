package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
)

// runProblemTTL is how long a read run problem is reused: the list page asks
// for every experiment at once.
const runProblemTTL = time.Second

// runProblemCache holds the newest run problem read per experiment.
type runProblemCache struct {
	mu   sync.Mutex
	byID map[string]runProblemEntry
}

type runProblemEntry struct {
	problem, jobID string
	at             time.Time
}

// RunProblem implements handlers.RunProblemReporter (N151): when the
// experiment's newest experiment.run job logged errors and sealed no run, the
// newest error message (a worker refusal, a slot that could not start) and
// that job's id; else "" and the job id when there is a job. The comparison
// page shows it in place of "Waiting for the first run to start".
func (l *launcher) RunProblem(expID string) (problem, jobID string) {
	if l.jobs == nil {
		return "", ""
	}
	l.problems.mu.Lock()
	defer l.problems.mu.Unlock()
	if e, ok := l.problems.byID[expID]; ok && time.Since(e.at) < runProblemTTL {
		return e.problem, e.jobID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var e runProblemEntry
	if j, err := l.jobs.Latest(ctx, l.org, kindExperimentRun, expTarget(expID)); err == nil && j != nil {
		e.problem, e.jobID = runProblemOf(j)
	}
	e.at = time.Now()
	if l.problems.byID == nil {
		l.problems.byID = map[string]runProblemEntry{}
	}
	l.problems.byID[expID] = e
	return e.problem, e.jobID
}

// runProblemOf reads a run job's progress: its last error when it logged
// errors and sealed nothing. The job id is returned whenever there is a job.
func runProblemOf(j *jobs.Job) (problem, jobID string) {
	var p expRunProgress
	if len(j.Progress) > 0 && json.Unmarshal(j.Progress, &p) == nil && p.Errors > 0 && p.Sealed == 0 {
		problem = p.LastError
	}
	return problem, j.ID
}
