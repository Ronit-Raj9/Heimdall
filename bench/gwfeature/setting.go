package gwfeature

import "encoding/json"

// RoutingRule is one Bifrost routing rule, with the fields and JSON tags of
// modelswitch.RoutingRule (gwfeature cannot import modelswitch: modelswitch
// imports gwfeature). modelswitch/rulejson_w4p1_test.go keeps them equal.
type RoutingRule struct {
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	Enabled       bool            `json:"enabled"`
	CelExpression string          `json:"cel_expression"`
	Targets       []RoutingTarget `json:"targets"`
	Fallbacks     []string        `json:"fallbacks,omitempty"`
	Scope         string          `json:"scope"`
	ScopeID       string          `json:"scope_id,omitempty"`
	Priority      int             `json:"priority"`
}

// RoutingTarget is one weighted target; an empty model keeps the request's.
type RoutingTarget struct {
	Provider string  `json:"provider,omitempty"`
	Model    string  `json:"model,omitempty"`
	Weight   float64 `json:"weight"`
}

// RuleSetting is the setting to copy for one routing rule.
func RuleSetting(title, note string, r RoutingRule) *BifrostSetting {
	body, _ := json.MarshalIndent(r, "", "  ")
	return &BifrostSetting{
		Kind:  "routing_rule",
		Title: title,
		Where: "Bifrost dashboard, Routing rules, Add rule; or config.json under governance.routing_rules.",
		Body:  string(body),
		Note:  note,
	}
}
