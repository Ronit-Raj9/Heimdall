package lib

import (
	"os"
	"reflect"
	"testing"
)

func TestJobsConfig(t *testing.T) {
	d := JobsConfig{}.Resolved()
	if d.ExperimentLimit != DefaultExperimentLimit || d.ExperimentPerOrg != DefaultExperimentPerOrg || d.Workers != DefaultExperimentLimit+jobsOpsHeadroom {
		t.Fatalf("defaults %+v", d)
	}
	if r := (JobsConfig{ExperimentLimit: 4}).Resolved(); r.ExperimentPerOrg != 4 || r.Workers != 12 {
		t.Fatalf("small limit %+v", r)
	}
	for _, bad := range []JobsConfig{
		{ExperimentLimit: -1},
		{ExperimentLimit: 4, ExperimentPerOrg: 5},
		{ExperimentLimit: 10, Workers: 10},
		{Workers: 5000},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v: want error", bad)
		}
	}
	if err := (JobsConfig{ExperimentLimit: 200, ExperimentPerOrg: 20}).Validate(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BENCH_JOBS_EXPERIMENT_LIMIT", "100")
	cfg := &Config{}
	if err := applyEnv(reflect.ValueOf(cfg).Elem(), EnvPrefix, os.LookupEnv); err != nil || cfg.Jobs.ExperimentLimit != 100 {
		t.Fatalf("env: %v %+v", err, cfg.Jobs)
	}
}
