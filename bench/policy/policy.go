// Package policy is release policy as code: a team writes explicit rules in
// policies/<name>.yaml (a false-completion ceiling, zero integrity violations,
// no regression on clean) and a candidate arm that breaks one is blocked even
// when it is better on average.
//
// A policy can only add requirements. It never disables, relaxes or replaces
// the baseline gates (G1 protected paths, G2 test-infra injection, G4
// counterfactual regrade, G6 null agents, egress) or the instrument gates that
// force INVALID: those run inside the verdict whatever the policy says, and no
// policy field reaches them. An "allow" decision never turns an INVALID or
// REGRESSED verdict into a ship; it only means no policy rule objects.
//
// Unknown is never treated as zero or as a pass. A check whose metric was not
// observed, whose data is missing, or that rests on fewer than min_n observed
// runs is unknown. An unknown on a block-severity check blocks under
// on_unknown: block and warns under on_unknown: warn. With on_unknown unset, a
// small sample (fewer runs than min_n even counting the unknown ones) warns as
// needing review, and any other unknown blocks.
package policy

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Version is the only policy_version this package reads.
const Version = 1

// Rules a check can name instead of a threshold. timeout_parity is the
// verdict's guard rule of the same name and reads metric timeout_rate.
const (
	RuleNoRegression  = "no_regression"
	RuleImproved      = "improved"
	RuleTimeoutParity = "timeout_parity"
)

// MetricTimeoutRate is the metric a timeout_parity check names: the share of
// an arm's runs that hit the wall-clock budget, computed from the outcomes.
const MetricTimeoutRate = "timeout_rate"

// Aggregate metrics computed over an arm's runs rather than read per run.
const (
	MetricIntegrityViolations    = "integrity_violations"
	MetricCostPerVerifiedSuccess = "cost_per_verified_success"
	metricCostUSD                = "cost_usd"
	metricVerifiedSuccess        = "verified_success"
)

// Severities and on_unknown values.
const (
	SeverityBlock = "block"
	SeverityWarn  = "warn"
)

// ConditionAll makes a check read every condition.
const ConditionAll = "all"

// DefaultArm is the arm a check reads when it names none: the candidate.
const DefaultArm = "B"

// BaselineArm is the arm rule checks compare against.
const BaselineArm = "A"

// Threshold texts of the two rules. The UI reads them to show a rule's
// observed value (the release effect) in signed percentage points.
const (
	ThresholdNoRegression = "no regression"
	ThresholdImproved     = "improved"
)

// ExitBlock is the exit code of `bifrost-bench check` when the policy decision
// is block. It is checked before the verdict codes of reporters.ExitCode: 0
// ok, 1 regressed, 2 inconclusive, 3 invalid, 4 infrastructure error, 5
// blocked by the release policy.
const ExitBlock = 5

// Check ids of results the evaluator adds itself, not read from the file.
const (
	CheckPolicyHash = "policy_hash"
	CheckPolicyFile = "policy_file"
)

// Load reads a policy file, validates it against spec/policy.schema.json,
// decodes it strictly and checks the rules the schema cannot express. It
// returns the raw bytes too, so the caller hashes exactly what was read.
func Load(path string) (*schemas.Policy, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read release policy %s: %w", path, err)
	}
	p, err := Parse(raw, path)
	if err != nil {
		return nil, nil, err
	}
	return p, raw, nil
}

// Parse decodes and validates one policy document. file names it in errors.
func Parse(raw []byte, file string) (*schemas.Policy, error) {
	var p schemas.Policy
	if err := suite.DecodeYAML(raw, file, suite.SchemaPolicy, &p); err != nil {
		return nil, err
	}
	if err := validate(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return &p, nil
}

// Hash is the sha256 of the policy file's raw bytes, the value the planner
// freezes into Manifest.PolicyHash.
func Hash(raw []byte) string {
	return utils.SHA256Bytes(raw)
}

// LoadFailure is the result when the policy an experiment froze cannot be
// read: one failed, blocking check that says why. A policy that cannot be
// read cannot vouch for a release, so it blocks.
func LoadFailure(path, frozenHash string, err error) *schemas.PolicyResult {
	return &schemas.PolicyResult{
		PolicyName: path,
		PolicyHash: frozenHash,
		Decision:   schemas.PolicyBlock,
		Checks: []schemas.PolicyCheckResult{{
			ID:        CheckPolicyFile,
			Metric:    CheckPolicyFile,
			Status:    schemas.PolicyFail,
			Blocking:  true,
			Observed:  schemas.Unknown("policy file could not be read", ""),
			Threshold: "readable and valid",
			Reason:    fmt.Sprintf("release policy %s could not be loaded: %v", path, err),
		}},
		EvaluatedAt: time.Now().UTC(),
	}
}

// validate checks what the JSON Schema cannot: unique ids, exactly one of
// rule, max, min or equals, and that rules only apply to per-run metrics
// with a baseline to compare against.
func validate(p *schemas.Policy) error {
	if p.PolicyVersion != Version {
		return fmt.Errorf("policy_version %d is not supported, want %d", p.PolicyVersion, Version)
	}
	var errs []error
	seen := map[string]bool{}
	for i, c := range p.Checks {
		where := fmt.Sprintf("checks[%d] %s", i, c.ID)
		if seen[c.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate id", where))
		}
		seen[c.ID] = true
		if n := thresholds(c); n != 1 {
			errs = append(errs, fmt.Errorf("%s: want exactly one of rule, max, min or equals, got %d", where, n))
		}
		if c.Rule == RuleTimeoutParity && c.Metric != MetricTimeoutRate {
			errs = append(errs, fmt.Errorf("%s: rule %s reads metric %s, not %s", where, c.Rule, MetricTimeoutRate, c.Metric))
		}
		if c.Rule != RuleTimeoutParity && c.Metric == MetricTimeoutRate {
			errs = append(errs, fmt.Errorf("%s: metric %s is only read by rule %s", where, MetricTimeoutRate, RuleTimeoutParity))
		}
		if c.Rule != "" && isAggregate(c.Metric) {
			errs = append(errs, fmt.Errorf("%s: rule %s needs a per-run metric, not the aggregate %s", where, c.Rule, c.Metric))
		}
		if c.Rule != "" && armOf(c) == BaselineArm {
			errs = append(errs, fmt.Errorf("%s: rule %s compares an arm against arm %s, so arm cannot be %s", where, c.Rule, BaselineArm, BaselineArm))
		}
	}
	return errors.Join(errs...)
}

// thresholds counts how many of rule, max, min and equals a check sets.
func thresholds(c schemas.PolicyCheck) int {
	n := 0
	if c.Rule != "" {
		n++
	}
	for _, v := range []*float64{c.Max, c.Min, c.Equals} {
		if v != nil {
			n++
		}
	}
	return n
}

// isAggregate reports whether metric is one of the arm-level aggregates.
func isAggregate(metric string) bool {
	return metric == MetricIntegrityViolations || metric == MetricCostPerVerifiedSuccess
}

// armOf is the check's arm with the default applied.
func armOf(c schemas.PolicyCheck) string {
	if c.Arm == "" {
		return DefaultArm
	}
	return c.Arm
}
