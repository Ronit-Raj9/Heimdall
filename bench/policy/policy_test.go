package policy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestShippedPoliciesValidate(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "policies", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"default.yaml": false, "strict.yaml": false}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			p, raw, err := Load(f)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if p.Name == "" || len(p.Checks) == 0 {
				t.Fatalf("policy = %+v", p)
			}
			if sum, _ := utils.SHA256File(f); Hash(raw) != sum {
				t.Fatalf("hash %s differs from the file's sha256 %s", Hash(raw), sum)
			}
		})
		want[filepath.Base(f)] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("policies/%s is missing", name)
		}
	}
}

func TestDefaultPolicyChecks(t *testing.T) {
	p, _, err := Load(filepath.Join("..", "policies", "default.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if p.OnUnknown != "" {
		t.Fatalf("default on_unknown = %q, want unset so a small sample only warns", p.OnUnknown)
	}
	byID := map[string]schemas.PolicyCheck{}
	for _, c := range p.Checks {
		byID[c.ID] = c
	}
	tests := []struct {
		id   string
		ok   func(c schemas.PolicyCheck) bool
		want string
	}{
		{"clean-no-regression", func(c schemas.PolicyCheck) bool {
			return c.Metric == "verified_success" && c.Condition == "clean" && c.Rule == RuleNoRegression
		}, "clean verified_success no_regression"},
		{"false-completion-ceiling", func(c schemas.PolicyCheck) bool {
			return c.Metric == "false_completion" && c.Max != nil && *c.Max == 0.05 && c.MinN == 10
		}, "false_completion max 0.05 min_n 10"},
		{"no-integrity-violations", func(c schemas.PolicyCheck) bool {
			return c.Metric == MetricIntegrityViolations && c.Equals != nil && *c.Equals == 0
		}, "integrity_violations equals 0"},
		{"retry-amplification-ceiling", func(c schemas.PolicyCheck) bool {
			return c.Metric == "retry_amplification" && c.Max != nil && *c.Max == 3
		}, "retry_amplification max 3"},
		{"cost-per-verified-success", func(c schemas.PolicyCheck) bool {
			return c.Metric == MetricCostPerVerifiedSuccess && c.Max != nil && c.Severity == SeverityWarn
		}, "cost_per_verified_success max, severity warn"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			c, ok := byID[tt.id]
			if !ok || !tt.ok(c) {
				t.Fatalf("check %s = %+v, want %s", tt.id, c, tt.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	const head = "policy_version: 1\nname: p\nchecks:\n"
	tests := []struct {
		name, doc string
		want      []string // substrings of the error; empty means valid
	}{
		{"valid max", head + "  - {id: a, metric: false_completion, max: 0.05}\n", nil},
		{"valid rule", head + "  - {id: a, metric: verified_success, rule: no_regression, condition: all}\n", nil},
		{"unknown field", head + "  - {id: a, metric: steps, max: 1, maxx: 2}\n", []string{"checks[0].maxx: unknown field"}},
		{"two thresholds", head + "  - {id: a, metric: steps, max: 1, min: 0}\n", []string{"checks[0]"}},
		{"no threshold", head + "  - {id: a, metric: steps}\n", []string{"checks[0]"}},
		{"bad rule", head + "  - {id: a, metric: steps, rule: better}\n", []string{"checks[0].rule"}},
		{"bad severity", head + "  - {id: a, metric: steps, max: 1, severity: fatal}\n", []string{"checks[0].severity"}},
		{"bad on_unknown", "policy_version: 1\nname: p\non_unknown: ignore\nchecks:\n  - {id: a, metric: steps, max: 1}\n", []string{"on_unknown"}},
		{"min_n zero", head + "  - {id: a, metric: steps, max: 1, min_n: 0}\n", []string{"checks[0].min_n"}},
		{"version 2", "policy_version: 2\nname: p\nchecks:\n  - {id: a, metric: steps, max: 1}\n", []string{"policy_version"}},
		{"no checks", "policy_version: 1\nname: p\nchecks: []\n", []string{"checks"}},
		{"duplicate id", head + "  - {id: a, metric: steps, max: 1}\n  - {id: a, metric: tokens, max: 1}\n", []string{"duplicate id"}},
		{"rule on aggregate", head + "  - {id: a, metric: integrity_violations, rule: no_regression}\n", []string{"needs a per-run metric"}},
		{"rule on baseline arm", head + "  - {id: a, metric: verified_success, rule: improved, arm: A}\n", []string{"arm cannot be A"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse([]byte(tt.doc), "p.yaml")
			if len(tt.want) == 0 {
				if err != nil || p == nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parsed %+v, want an error", p)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) || !strings.Contains(err.Error(), "p.yaml") {
					t.Errorf("err = %v, want it to contain %q and the file name", err, w)
				}
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadFailureBlocks(t *testing.T) {
	r := LoadFailure("policies/gone.yaml", "abc", os.ErrNotExist)
	if r.Decision != schemas.PolicyBlock || len(r.Checks) != 1 || !r.Checks[0].Blocking ||
		r.Checks[0].Status != schemas.PolicyFail || !strings.Contains(r.Checks[0].Reason, "policies/gone.yaml") {
		t.Fatalf("result = %+v", r)
	}
	if Decide(r.Checks) != schemas.PolicyBlock {
		t.Fatal("load failure does not decide block")
	}
}

func TestHash(t *testing.T) {
	if got := Hash([]byte("x")); got != "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881" {
		t.Fatalf("hash = %s", got)
	}
}
