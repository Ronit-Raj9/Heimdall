package suite

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func acmeAPI(cassette string) schemas.ExternalAPI {
	return schemas.ExternalAPI{ID: "acme", BaseURL: "https://api.acme.test/v1", Cassette: cassette,
		Allow: []schemas.ExternalAllow{{Methods: []string{"GET"}, PathPrefix: "/rates"}}}
}

// TestLockFoldsExternalAPIs: a task without external APIs keeps its lock;
// declaring one, and re-recording its cassette, change only the fixtures
// hash; a suite-level API counts for every task.
func TestLockFoldsExternalAPIs(t *testing.T) {
	benchRoot, pack := copyPack(t)
	lockOf := func(edit func(s *schemas.Suite)) schemas.TaskLock {
		t.Helper()
		s := loadMini(t, benchRoot)
		edit(s)
		l, err := Lock(s, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		return l.Tasks["m01"]
	}
	none := func(*schemas.Suite) {}
	base := lockOf(none)
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, map[string]string{"external/acme.cassette.jsonl": `{"method":"GET","path":"/rates"}` + "\n"})
	if got := lockOf(none); got != base {
		t.Fatal("a cassette file changed the lock of a task without external apis")
	}
	withAPI := func(s *schemas.Suite) {
		s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("external/acme.cassette.jsonl")}
	}
	v1 := lockOf(withAPI)
	if v1.FixturesSHA256 == base.FixturesSHA256 {
		t.Fatal("declaring an external api did not change the fixtures hash")
	}
	if v1.InstructionSHA256 != base.InstructionSHA256 || v1.RepoTreeSHA256 != base.RepoTreeSHA256 || v1.VerifierSHA256 != base.VerifierSHA256 || v1.ControlsSHA256 != base.ControlsSHA256 {
		t.Error("an external api changed a hash other than the fixtures hash")
	}
	writeFiles(t, task, map[string]string{"external/acme.cassette.jsonl": `{"method":"GET","path":"/rates","status":500}` + "\n"})
	if got := lockOf(withAPI); got.FixturesSHA256 == v1.FixturesSHA256 {
		t.Error("re-recording the cassette did not change the lock")
	}
	missing := lockOf(func(s *schemas.Suite) { s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("external/none.jsonl")} })
	if missing.FixturesSHA256 == base.FixturesSHA256 {
		t.Error("an external api without its cassette yet did not change the lock")
	}
	suiteLevel := lockOf(func(s *schemas.Suite) { s.External = []schemas.ExternalAPI{acmeAPI("")} })
	if suiteLevel.FixturesSHA256 == base.FixturesSHA256 {
		t.Error("a suite-level external api did not change the task's lock")
	}
}

func TestLintExternal(t *testing.T) {
	benchRoot, pack := copyPack(t)
	writeFiles(t, filepath.Join(pack, "tasks", "m01"), map[string]string{"acme.jsonl": "{}\n"})
	cases := []struct {
		name     string
		edit     func(s *schemas.Suite)
		severity string
		want     string // "" means no external problem at all
	}{
		{"none", func(*schemas.Suite) {}, "", ""},
		{"good task api", func(s *schemas.Suite) { s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("acme.jsonl")} }, "", ""},
		{"invalid id", func(s *schemas.Suite) {
			a := acmeAPI("acme.jsonl")
			a.ID = "Acme_1"
			s.Tasks[0].External = []schemas.ExternalAPI{a}
		}, SeverityError, "host-name safe"},
		{"collides with a service", func(s *schemas.Suite) {
			s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("acme.jsonl")}
			s.Tasks[0].Services = []schemas.ServiceSpec{{ID: "acme", Image: "x:1"}}
		}, SeverityError, "also a service id"},
		{"suite and task both declare it", func(s *schemas.Suite) {
			s.External = []schemas.ExternalAPI{acmeAPI("")}
			s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("acme.jsonl")}
		}, SeverityError, "already an external api in suite.yaml"},
		{"cassette missing", func(s *schemas.Suite) { s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("none.jsonl")} }, SeverityWarning, "does not exist yet"},
		{"cassette outside the task", func(s *schemas.Suite) { s.Tasks[0].External = []schemas.ExternalAPI{acmeAPI("../../../x.jsonl")} }, SeverityError, "cassette"},
		{"empty allowlist", func(s *schemas.Suite) {
			a := acmeAPI("acme.jsonl")
			a.Allow = nil
			s.Tasks[0].External = []schemas.ExternalAPI{a}
		}, SeverityWarning, "refuses every call"},
		{"suite api without cassette", func(s *schemas.Suite) { s.External = []schemas.ExternalAPI{acmeAPI("")} }, SeverityWarning, "cassette: not set"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := loadMini(t, benchRoot)
			c.edit(s)
			var found []Problem
			for _, p := range Lint(s) {
				if strings.Contains(p.Message, "external") {
					found = append(found, p)
				}
			}
			if c.want == "" {
				if len(found) > 0 {
					t.Fatalf("unexpected problems: %+v", found)
				}
				return
			}
			for _, p := range found {
				if p.Severity == c.severity && strings.Contains(p.Message, c.want) {
					return
				}
			}
			t.Fatalf("no %s containing %q in %+v", c.severity, c.want, found)
		})
	}
}

// TestDecodeExternal: the external fields pass the schema and the strict
// decode for a task, a suite and an experiment, and a bad mode is refused.
func TestDecodeExternal(t *testing.T) {
	task := `id: t1
version: 1.0.0
title: rates
project: acme
kind: feature
instruction: instruction.md
repo_dir: repo
notes: {clean: notes.md, stale: stale.md}
verifier: {tests: tests, required: []}
protected: []
controls: {reference: ref.sh, empty: empty.sh, mutants: [], exploit: exploit.sh}
timeouts: {agent_s: 60}
external:
  - id: acme
    base_url: https://api.acme.test/v1
    allow: [{methods: [GET], path_prefix: /rates}]
    headers: {Accept: application/json}
    auth_env: ACME_KEY
    auth_scheme: Bearer
    mode: replay
    cassette: external/acme.cassette.jsonl
    max_calls: 20
    max_bytes: 65536
    timeout_s: 10
`
	var got schemas.Task
	if err := DecodeYAML([]byte(task), "task.yaml", "task", &got); err != nil {
		t.Fatal(err)
	}
	if len(got.External) != 1 || got.External[0].AuthEnv != "ACME_KEY" || got.External[0].Allow[0].PathPrefix != "/rates" {
		t.Fatalf("decoded %+v", got.External)
	}
	bad := strings.Replace(task, "mode: replay", "mode: sometimes", 1)
	if err := DecodeYAML([]byte(bad), "task.yaml", "task", &schemas.Task{}); err == nil {
		t.Fatal("mode sometimes was accepted")
	}
	exp := "name: x\nsuite: s@1.0.0\nbase: agents/a.yaml\narms: {A: {label: a}, B: {label: b}}\nprimary: {condition: clean, metric: verified_success}\nguards: []\nconditions: [{id: clean, tasks: all, trials: 1}]\nbudget: {total_usd: 1}\nverdict: {policy: paired_signflip}\nseed: 1\nconcurrency: 1\nmanifest_mode: agent_exposure\nexternal: live\n"
	var e schemas.Experiment
	if err := DecodeYAML([]byte(exp), "x.yaml", "experiment", &e); err != nil {
		t.Fatal(err)
	}
	if e.External != schemas.ExternalLive {
		t.Fatalf("experiment external = %q", e.External)
	}
	if err := DecodeYAML([]byte(strings.Replace(exp, "external: live", "external: always", 1)), "x.yaml", "experiment", &schemas.Experiment{}); err == nil {
		t.Fatal("experiment external always was accepted")
	}
}
