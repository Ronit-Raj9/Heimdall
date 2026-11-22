// Package apptasks turns a company's OpenAPI file and policy document into
// a benchmark of support tasks against a copy of their app (INFRA_I3 7.4,
// the tau-bench co-design method):
//
//  1. the OpenAPI file becomes a stateful mock service (scaffold's Wave A
//     Y14 mocks: the same routes over HTTP and as MCP tools, over seeded,
//     exported state);
//  2. a pinned model, through the gateway on a budget, drafts the policy as
//     a numbered list of rules, which a person edits and approves once;
//  3. it drafts seed data for the mock, checked against the spec's schemas;
//  4. for each approved rule it drafts scenarios (a customer, their goal,
//     what they know, the calls a correct agent makes and must not make);
//  5. each scenario's reference plan is run on the seed with the mock's
//     rules: every step must succeed, the end state becomes state checks,
//     the forbidden call becomes a policy check, and the controls (empty,
//     skipped key step, forbidden extra step) must fail those checks while
//     the reference passes;
//  6. surviving scenarios become Y14 scenario-row tasks with a checks.yaml,
//     written as pending review (provenance instruction_reviewed: false);
//     the readiness checks run them against the real service image and are
//     the quality filter.
package apptasks

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// Rule kinds.
const (
	RuleMust     = "must"     // the agent must do something
	RuleMustNot  = "must_not" // the agent must never do something
	RuleEscalate = "escalate" // the agent must hand the customer to a person
	RuleInfo     = "info"     // a fact the agent must tell or use
)

// Limits.
const (
	MaxRules       = 40
	MaxRuleRunes   = 600
	MaxPolicyBytes = 256 << 10
	MaxPerRule     = 5
	MaxTasks       = 120
)

// Rule is one numbered policy rule.
type Rule struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Kind       string   `json:"kind"`
	Operations []string `json:"operations"`
}

var ruleIDRE = regexp.MustCompile(`^R[0-9]{1,3}$`)

// CheckRules validates a rule list against the mock's operations, as the
// assist's reply and as a person's edit.
func CheckRules(rules []Rule, m *scaffold.MockSpec) error {
	if len(rules) == 0 {
		return fmt.Errorf("no rules: the policy needs at least one rule a task can test")
	}
	if len(rules) > MaxRules {
		return fmt.Errorf("%d rules, at most %d", len(rules), MaxRules)
	}
	ops := opNames(m)
	seen := map[string]bool{}
	for i, r := range rules {
		where := fmt.Sprintf("rule %d", i+1)
		if !ruleIDRE.MatchString(r.ID) {
			return fmt.Errorf("%s: id %q must be R and a number, such as R3", where, r.ID)
		}
		if seen[r.ID] {
			return fmt.Errorf("%s: id %s is used twice", where, r.ID)
		}
		seen[r.ID] = true
		t := strings.TrimSpace(r.Text)
		if t == "" {
			return fmt.Errorf("%s (%s): the text is empty", where, r.ID)
		}
		if n := len([]rune(t)); n > MaxRuleRunes {
			return fmt.Errorf("%s (%s): %d characters, at most %d", where, r.ID, n, MaxRuleRunes)
		}
		switch r.Kind {
		case RuleMust, RuleMustNot, RuleEscalate, RuleInfo:
		default:
			return fmt.Errorf("%s (%s): kind %q must be must, must_not, escalate or info", where, r.ID, r.Kind)
		}
		for _, o := range r.Operations {
			if !ops[o] {
				return fmt.Errorf("%s (%s): operation %q is not in the API (%s)", where, r.ID, o, strings.Join(sortedKeys(ops), ", "))
			}
		}
	}
	return nil
}

// PolicyText is the approved rules as the agent's instruction lists them.
func PolicyText(rules []Rule) string {
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "%s. %s\n", r.ID, strings.TrimSpace(r.Text))
	}
	return b.String()
}

func opNames(m *scaffold.MockSpec) map[string]bool {
	out := map[string]bool{}
	for _, op := range m.Operations {
		out[op.Name] = true
	}
	return out
}
