package quickstart

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
)

// First workflow: `heimdall compare` writes a normal experiment file from
// one of three flag forms and then plans, runs and checks it like any other.

// Compare defaults.
const (
	DefaultTrials = 3
	DefaultPolicy = "policies/default.yaml"
)

// ArmInput is one side of a comparison.
type ArmInput struct {
	Label string
	Agent string         // agent config path relative to the project root; empty uses Base
	Set   map[string]any // dotted-path overrides
}

// CompareOptions is everything ExperimentYAML needs.
type CompareOptions struct {
	Name        string
	Suite       string
	Base        string // agent config every arm starts from
	A, B        ArmInput
	Trials      int
	Faults      []string // probe ids, one condition each
	Tier        string
	BudgetUSD   float64
	ReserveUSD  float64 // budget.reserve_usd; 0 leaves it out
	Concurrency int
	Policy      string // "" uses DefaultPolicy; "none" leaves it out
	Seed        int64
	Smoke       bool   // every arm scripted: results are synthetic
	Command     string // the command line, recorded in the file header
}

// ParseArm reads one --a/--b value: an agent config path (agents/x.yaml),
// or comma-separated key=value overrides of the base config
// (model=claude-sonnet-4-5,budgets.wall_s=600). Values are read as YAML
// scalars, so numbers and booleans keep their type.
func ParseArm(v string) (ArmInput, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return ArmInput{}, fmt.Errorf("empty arm")
	}
	if !strings.Contains(v, "=") {
		if !strings.HasSuffix(v, ".yaml") && !strings.HasSuffix(v, ".yml") {
			return ArmInput{}, fmt.Errorf("%q: want an agent config file (agents/x.yaml) or key=value overrides (model=...)", v)
		}
		return ArmInput{Agent: v}, nil
	}
	set := map[string]any{}
	for _, kv := range splitOverrides(v) {
		k, val, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return ArmInput{}, fmt.Errorf("%q: want key=value", kv)
		}
		var parsed any
		if err := yaml.Unmarshal([]byte(val), &parsed); err != nil || parsed == nil {
			parsed = val
		}
		if _, isMap := parsed.(map[string]any); isMap {
			parsed = val // a value with ": " stays a string
		}
		set[k] = parsed
	}
	return ArmInput{Set: set}, nil
}

// splitOverrides splits "a=1,b=x,y" on commas that start a new key=value.
func splitOverrides(v string) []string {
	parts := strings.Split(v, ",")
	var out []string
	for _, p := range parts {
		if len(out) > 0 && !strings.Contains(p, "=") {
			out[len(out)-1] += "," + p
			continue
		}
		out = append(out, p)
	}
	return out
}

// Describe is a short label for an arm: the config file, else its overrides.
func (a ArmInput) Describe() string {
	if a.Label != "" {
		return a.Label
	}
	var parts []string
	if a.Agent != "" {
		parts = append(parts, a.Agent)
	}
	keys := make([]string, 0, len(a.Set))
	for k := range a.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, a.Set[k]))
	}
	if len(parts) == 0 {
		return "base config"
	}
	return strings.Join(parts, " ")
}

var nameRE = regexp.MustCompile(`[^a-z0-9-]+`)

// ExperimentName turns free text into an experiment name (and file name).
func ExperimentName(s string) string {
	n := strings.Trim(nameRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(n) > 60 {
		n = strings.Trim(n[:60], "-")
	}
	if n == "" {
		return "compare"
	}
	return n
}

// TierSupported reports whether this build's experiment schema has a tier
// field (W7 suite tiers).
func TierSupported() bool {
	t := reflect.TypeOf(schemas.Experiment{})
	for i := 0; i < t.NumField(); i++ {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag == "tier" {
			return true
		}
	}
	return false
}

// ExperimentYAML renders the experiment file: arms A (baseline) and B
// (candidate), the clean condition plus one condition per fault, all tasks of
// the suite, verified_success as the primary endpoint, the integrity guard,
// the paired sign-flip verdict and the default release policy.
func ExperimentYAML(o CompareOptions) ([]byte, error) {
	if o.Suite == "" {
		return nil, fmt.Errorf("no suite")
	}
	if o.Base == "" && (o.A.Agent == "" || o.B.Agent == "") {
		return nil, fmt.Errorf("an arm without its own agent config needs a base config (--base agents/x.yaml)")
	}
	if o.Trials <= 0 {
		o.Trials = DefaultTrials
	}
	if o.Tier != "" && !TierSupported() {
		return nil, fmt.Errorf("--tier %s: this build's experiment schema has no tier field (suite tiers are not in it)", o.Tier)
	}
	base := o.Base
	if base == "" {
		base = o.A.Agent
	}
	policy := o.Policy
	if policy == "" {
		policy = DefaultPolicy
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by `heimdall compare`; a normal experiment file. Rerun it from the\n# project folder (.heimdall/) with\n#   heimdall check %s\n", "experiments/"+o.Name+".yaml")
	if o.Command != "" {
		fmt.Fprintf(&b, "# from: %s\n", o.Command)
	}
	if o.Smoke {
		b.WriteString("# Every arm is a scripted agent: the results are a synthetic smoke test, never a finding.\n")
	}
	fmt.Fprintf(&b, "name: %s\n", o.Name)
	if o.Smoke {
		b.WriteString("smoke: true\n")
	}
	fmt.Fprintf(&b, "suite: %s\n", o.Suite)
	if o.Tier != "" {
		fmt.Fprintf(&b, "tier: %s\n", o.Tier)
	}
	fmt.Fprintf(&b, "base: %s\n", base)
	b.WriteString("arms:\n")
	for _, arm := range []struct {
		id string
		in ArmInput
	}{{"A", o.A}, {"B", o.B}} {
		fmt.Fprintf(&b, "  %s:\n    label: %s\n", arm.id, yamlScalar(arm.in.Describe()))
		if arm.in.Agent != "" && arm.in.Agent != base {
			fmt.Fprintf(&b, "    agent: %s\n", arm.in.Agent)
		}
		if len(arm.in.Set) > 0 {
			raw, err := yaml.Marshal(arm.in.Set)
			if err != nil {
				return nil, err
			}
			b.WriteString("    set:\n")
			for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
				b.WriteString("      " + line + "\n")
			}
		}
	}
	b.WriteString("primary: {condition: clean, metric: verified_success}\n")
	b.WriteString("guards:\n  - {rule: no_new_integrity_violation}\n")
	b.WriteString("conditions:\n")
	fmt.Fprintf(&b, "  - {id: clean, tasks: all, trials: %d}\n", o.Trials)
	seen := map[string]bool{}
	for _, f := range o.Faults {
		if f == "" || f == schemas.ConditionClean || seen[f] {
			continue
		}
		seen[f] = true
		fmt.Fprintf(&b, "  - {id: %s, tasks: all, trials: %d}\n", f, o.Trials)
	}
	b.WriteString(BudgetLine(o.BudgetUSD, o.ReserveUSD))
	b.WriteString("verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}\n")
	seed := o.Seed
	if seed == 0 {
		seed = 20261118
	}
	fmt.Fprintf(&b, "seed: %d\n", seed)
	conc := o.Concurrency
	if conc <= 0 {
		conc = 2
	}
	fmt.Fprintf(&b, "concurrency: %d\n", conc)
	b.WriteString("manifest_mode: agent_exposure\n")
	if policy != "none" {
		fmt.Fprintf(&b, "policy: %s\n", policy)
	}
	return []byte(b.String()), nil
}

// yamlScalar quotes s when YAML would not read it back as the same string.
func yamlScalar(s string) string {
	var back string
	if err := yaml.Unmarshal([]byte(s), &back); err == nil && back == s && !strings.ContainsAny(s, "#:{}[],&*!|>'\"%@`") {
		return s
	}
	return strconv.Quote(s)
}

// BudgetLine is an experiment file's budget line. reserve_usd is written only
// when positive: it is what the scheduler counts for a run whose cost is
// unknown (an unpriced model), and without it the first such run is counted
// at the whole remaining budget, so an experiment on an unpriced model stops
// after one run (live check, 21 Nov 2026).
func BudgetLine(total, reserve float64) string {
	if reserve > 0 {
		return fmt.Sprintf("budget: {total_usd: %s, reserve_usd: %s}\n", strconv.FormatFloat(total, 'f', -1, 64), strconv.FormatFloat(reserve, 'f', -1, 64))
	}
	return fmt.Sprintf("budget: {total_usd: %s}\n", strconv.FormatFloat(total, 'f', -1, 64))
}
