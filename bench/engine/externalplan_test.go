package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestCheckExternal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.cassette.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	api := func(mode, cassette string) schemas.ExternalAPI {
		return schemas.ExternalAPI{ID: "acme", BaseURL: "https://api.acme.test/v1", Mode: mode, Cassette: cassette,
			Allow: []schemas.ExternalAllow{{PathPrefix: "/rates"}}}
	}
	suiteWith := func(task *schemas.Task) *schemas.Suite {
		task.ID, task.Dir = "t1", dir
		return &schemas.Suite{ID: "s", Root: dir, Tasks: []*schemas.Task{task}, TaskIDs: []string{"t1"}}
	}
	conds := []condPlan{{id: "clean", tasks: []string{"t1"}, trials: 1}}
	cases := []struct {
		name     string
		task     *schemas.Task
		external string
		want     string // "" means the plan passes
	}{
		{"no external apis", &schemas.Task{}, "", ""},
		{"replay from existing cassette", &schemas.Task{External: []schemas.ExternalAPI{api("", "acme.cassette.jsonl")}}, "", ""},
		{"explicit replay", &schemas.Task{External: []schemas.ExternalAPI{api("replay", "acme.cassette.jsonl")}}, "replay", ""},
		{"no cassette and no opt in", &schemas.Task{External: []schemas.ExternalAPI{api("", "missing.jsonl")}}, "", "has no cassette"},
		{"replay without cassette", &schemas.Task{External: []schemas.ExternalAPI{api("replay", "missing.jsonl")}}, "", "cassette \"missing.jsonl\" is missing"},
		{"live without opt in", &schemas.Task{External: []schemas.ExternalAPI{api("live", "")}}, "", "must opt in with external: live"},
		{"record with opt in", &schemas.Task{External: []schemas.ExternalAPI{api("", "missing.jsonl")}}, "record", ""},
		{"experiment replay overrides task live", &schemas.Task{External: []schemas.ExternalAPI{api("live", "acme.cassette.jsonl")}}, "replay", ""},
		{"bad experiment value", &schemas.Task{}, "sometimes", `experiment external "sometimes"`},
		{"invalid api", &schemas.Task{External: []schemas.ExternalAPI{{ID: "Bad_ID", BaseURL: "ftp://x"}}}, "", "host-name safe"},
		{"id collides with a service", &schemas.Task{External: []schemas.ExternalAPI{api("", "acme.cassette.jsonl")},
			Services: []schemas.ServiceSpec{{ID: "acme", Image: "x"}}}, "", "also a service id"},
		{"cassette outside the task folder", &schemas.Task{External: []schemas.ExternalAPI{api("", "../../etc/passwd")}}, "", "cassette"},
	}
	p := &Planner{logger: schemas.NoopLogger{}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := p.checkExternal(&schemas.Experiment{ID: "e", External: c.external}, suiteWith(c.task), conds)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("got %v, want the plan to pass", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// An experiment without external serialises, and so hashes, exactly as it
// did before the field existed; with it set, the value is part of the
// manifest hash.
func TestExperimentExternalHash(t *testing.T) {
	without := schemas.Manifest{ExpID: "e_x", Experiment: schemas.Experiment{ID: "e_x", Name: "x"}}
	b, err := json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"external"`) {
		t.Fatalf("manifest without external mentions it: %s", b)
	}
	with := without
	with.Experiment.External = schemas.ExternalLive
	h1, err := utils.HashJSON(without)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := utils.HashJSON(with)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("external: live does not change the manifest hash")
	}
}

func TestScenarioHashesSuiteExternal(t *testing.T) {
	p := &Planner{logger: schemas.NoopLogger{}}
	got, err := p.scenarioHashes(&schemas.Suite{})
	if err != nil || got != nil {
		t.Fatalf("suite without scenario inputs: got %v, %v; want nil", got, err)
	}
	got, err = p.scenarioHashes(&schemas.Suite{External: []schemas.ExternalAPI{{ID: "acme", BaseURL: "https://api.acme.test"}}})
	if err != nil || got["suite.external"] == "" {
		t.Fatalf("suite with external apis: got %v, %v; want suite.external hashed", got, err)
	}
}
