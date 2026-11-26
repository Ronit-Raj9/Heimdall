package main

// Wave 4 lane I35 (W4-48): twins.make writes impossible twins of a
// benchmark's tasks into <benchmark>-twins and queues suite.admit for it, so
// admission proves each twin (the honest reference passes the original
// tests and fails the changed ones). No model is called.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/twins"
)

const kindTwinsMake = "twins.make"

func init() {
	registerJobKind(jobKindSpec{Kind: kindTwinsMake, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithLimit(2, 1)},
		Validate: validateTwinsMake, Run: runTwinsMake})
}

type twinsParams struct {
	Suite string   `json:"suite"`
	Tasks []string `json:"tasks,omitempty"`
	Max   int      `json:"max,omitempty"`
}

type twinsJobResult struct {
	*twins.Result
	AdmitJob string `json:"admit_job,omitempty"`
}

func validateTwinsMake(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p twinsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, "", err
	}
	if err := ops.ValidID("suite", p.Suite); err != nil {
		return nil, "", invalid(err)
	}
	for _, t := range p.Tasks {
		if err := ops.ValidID("task", t); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.Max <= 0 || p.Max > twins.MaxTwins {
		p.Max = twins.MaxTwins
	}
	return p, "suite:" + p.Suite, nil
}

func runTwinsMake(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p twinsParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	benchDir := filepath.Join(env.ProjectRoot, string(projectfs.AreaBenchmarks))
	src, err := suite.Builtin(benchDir)[0].Load(ctx, p.Suite)
	if err != nil {
		return nil, fmt.Errorf("load benchmark %s: %w", p.Suite, err)
	}
	res, err := twins.MakeSuite(src, benchDir, p.Tasks, p.Max)
	if err != nil {
		return nil, err
	}
	log.Printf("made %d impossible twins in %s, skipped %d tasks", len(res.Made), res.Suite, len(res.Skipped))
	out := twinsJobResult{Result: res}
	if len(res.Made) == 0 {
		return out, nil
	}
	if env.Services.Jobs == nil {
		return out, errors.New("the twins were written, but this server cannot queue their readiness check")
	}
	params, _ := json.Marshal(map[string]string{"suite": res.Suite})
	admit, err := env.Services.Jobs.Create(ctx, jobs.NewJob{OrgID: env.OrgID, Kind: kindSuiteAdmit, Params: params, CreatedBy: "system"})
	if err != nil {
		return out, fmt.Errorf("queue the readiness check of %s: %w", res.Suite, err)
	}
	out.AdmitJob = admit.ID
	log.Printf("queued the readiness check of %s as job %s", res.Suite, admit.ID)
	return out, nil
}
