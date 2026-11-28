package modelswitch

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/schemas"
)

// BifrostSchemaURL is the $schema of a Bifrost config.json.
const BifrostSchemaURL = "https://www.getbifrost.ai/schema"

// Routing defaults.
const (
	DefaultHeaderPrefix  = "x-heimdall-"
	DefaultBasePriority  = 100
	DefaultBudgetSaverAt = 80 // budget_used percent at which the budget saver rule routes to the cheapest model
	maxDefaultTargets    = 3
	maxFallbacks         = 4
	minTargetWeight      = 0.05
)

// Rule kinds.
const (
	RuleBudget  = "budget_saver"
	RuleSegment = "segment"
	RuleDefault = "default"
)

// RoutingOptions shape the generated rules.
type RoutingOptions struct {
	// Prefix starts every rule name; empty is "heimdall-<evaluation id>".
	Prefix string `json:"prefix,omitempty"`
	// HeaderPrefix starts the request header a segment rule matches; the
	// app sets x-heimdall-<segment key> on each request.
	HeaderPrefix string `json:"header_prefix,omitempty"`
	// Scope and ScopeID scope every rule (global, team, customer or
	// virtual_key with its id); empty is global.
	Scope   string `json:"scope,omitempty"`
	ScopeID string `json:"scope_id,omitempty"`
	// BasePriority is the first rule's priority; the others follow.
	BasePriority int `json:"base_priority,omitempty"`
	// BudgetSaverAt is the budget_used percent of the budget saver rule.
	BudgetSaverAt float64 `json:"budget_saver_at,omitempty"`
}

// BifrostConfig is the downloadable config.json fragment: only the
// governance routing rules, in Bifrost's config schema.
type BifrostConfig struct {
	Schema     string            `json:"$schema"`
	Governance BifrostGovernance `json:"governance"`
}

// BifrostGovernance is the governance section with its routing rules.
type BifrostGovernance struct {
	RoutingRules []RoutingRule `json:"routing_rules"`
}

// RoutingRule is one Bifrost routing rule (config schema $defs.routing_rule,
// the same fields POST /api/routing/rules reads).
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

// Export is GET /evaluations/{id}/model-switch/bifrost-config: the config
// and, per rule, why it is there.
type Export struct {
	Evaluation string         `json:"evaluation"`
	Ready      bool           `json:"ready"` // false when no model has runs: Config has no rule
	Config     BifrostConfig  `json:"config"`
	Rules      []RuleWhy      `json:"rules"`
	Headers    []HeaderHint   `json:"headers"` // the request headers segment rules read
	Budget     []BudgetHint   `json:"budget_hints"`
	Options    RoutingOptions `json:"options"`
	Notes      []string       `json:"notes"`
}

// RuleWhy explains one rule from the measured numbers.
type RuleWhy struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // budget_saver | segment | default
	Enabled  bool   `json:"enabled"`
	Why      string `json:"why"`
	Evidence string `json:"evidence"` // significant | not_significant | measured
}

// HeaderHint is one request header the app sets for segment routing.
type HeaderHint struct {
	Header string   `json:"header"`
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// BudgetHint is one model's measured cost at volume.
type BudgetHint struct {
	Label              string        `json:"label"`
	Model              string        `json:"model"`
	CostPerThousandRun schemas.Value `json:"cost_per_1000_runs"`
	Text               string        `json:"text"`
}

var ruleNameSafe = regexp.MustCompile(`[^a-z0-9-]+`)

// RuleSlug turns any text into a rule name part.
func RuleSlug(s string) string {
	s = strings.Trim(ruleNameSafe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return cmp.Or(s, "x")
}

// HeaderFor is the request header a segment key is matched on.
func HeaderFor(prefix, key string) string {
	return cmp.Or(prefix, DefaultHeaderPrefix) + RuleSlug(strings.ReplaceAll(key, "_", "-"))
}

// Routing writes the Bifrost routing config from the report. It is pure.
func Routing(rep *Report, opt RoutingOptions) Export {
	opt.Prefix = cmp.Or(strings.TrimSpace(opt.Prefix), "heimdall-"+RuleSlug(rep.Evaluation))
	opt.HeaderPrefix = cmp.Or(strings.TrimSpace(opt.HeaderPrefix), DefaultHeaderPrefix)
	opt.Scope = cmp.Or(strings.TrimSpace(opt.Scope), "global")
	if opt.Scope == "global" {
		opt.ScopeID = ""
	}
	if opt.BasePriority == 0 {
		opt.BasePriority = DefaultBasePriority
	}
	if opt.BudgetSaverAt == 0 {
		opt.BudgetSaverAt = DefaultBudgetSaverAt
	}
	ex := Export{Evaluation: rep.Evaluation, Options: opt, Config: BifrostConfig{Schema: BifrostSchemaURL, Governance: BifrostGovernance{RoutingRules: []RoutingRule{}}},
		Rules: []RuleWhy{}, Headers: []HeaderHint{}, Budget: []BudgetHint{}, Notes: []string{}}
	byLabel := map[string]Model{}
	for _, m := range rep.Models {
		byLabel[m.Label] = m
	}
	if rep.Recommendation.Action == ActionNothingSolved {
		ex.Notes = append(ex.Notes, NothingSolvedSummary+" No routing rule is offered.")
		return ex
	}
	def, ok := byLabel[rep.Recommendation.Model]
	if !ok || def.Runs == 0 {
		ex.Notes = append(ex.Notes, "No model has graded runs, so no routing rule can be measured. Run the evaluation first.")
		return ex
	}
	ex.Ready = true
	if rep.Recommendation.Action == ActionStillMoving || rep.Recommendation.Action == ActionNotEnough {
		ex.Notes = append(ex.Notes, "Read this config as a draft: "+rep.Recommendation.Summary)
	}
	newRule := func(name, desc, cel string, targets []RoutingTarget, fallbacks []string, enabled bool) RoutingRule {
		return RoutingRule{Name: opt.Prefix + "-" + name, Description: clip(desc, 500), Enabled: enabled, CelExpression: cel,
			Targets: targets, Fallbacks: fallbacks, Scope: opt.Scope, ScopeID: opt.ScopeID}
	}
	chain := fallbackOrder(rep.Models)
	var rules []RoutingRule

	// Budget saver: near the budget, the cheapest model with runs.
	if cheap, ok := cheapest(rep.Models); ok && cheap.Label != def.Label {
		tied := slices.Contains(rep.Recommendation.Tied, cheap.Label)
		why := fmt.Sprintf("When %.0f%% of the budget is used, send requests to %s, the cheapest model per verified success (%s against %s for %s).",
			opt.BudgetSaverAt, display(cheap), usdValue(cheap.CostPerSuccess), usdValue(def.CostPerSuccess), display(def))
		evidence := "measured"
		if tied {
			why += " It cannot be told apart from the best model on these tasks."
		} else {
			why += " It scored lower than the best model (" + qualityText(cheap) + " against " + qualityText(byLabel[rep.Best]) + "), so the rule starts disabled: turn it on if saving money near the budget matters more."
		}
		t, okT := target(cheap)
		if okT {
			rules = append(rules, newRule("budget-saver", why, fmt.Sprintf("budget_used >= %s", strconv.FormatFloat(opt.BudgetSaverAt, 'f', -1, 64)),
				[]RoutingTarget{t}, fallbacks(chain, []string{cheap.Label}), tied))
			ex.Rules = append(ex.Rules, RuleWhy{Kind: RuleBudget, Enabled: tied, Why: why, Evidence: evidence})
		}
	}

	// Segment rules: where the segment's best model differs from the default.
	headers := map[string]*HeaderHint{}
	for _, g := range rep.Segments {
		c := g.VsDefault
		if g.Small || c == nil || c.Result == ResultNoData || !safeValue(g.Value) {
			continue
		}
		best := byLabel[g.Best]
		t, okT := target(best)
		if !okT {
			continue
		}
		h := HeaderFor(opt.HeaderPrefix, g.Key)
		enabled := c.Result == "better"
		evidence := "not_significant"
		why := fmt.Sprintf("Requests with %s: %s go to %s: %s.", h, g.Value, display(best), c.Reason)
		if enabled {
			evidence = "significant"
		} else {
			why += " Not proven at this size, so the rule starts disabled."
		}
		cel := fmt.Sprintf("headers[%s] == %s", strconv.Quote(h), strconv.Quote(g.Value))
		rules = append(rules, newRule(RuleSlug(g.Key)+"-"+RuleSlug(g.Value), why, cel, []RoutingTarget{t}, fallbacks(chain, []string{best.Label}), enabled))
		ex.Rules = append(ex.Rules, RuleWhy{Kind: RuleSegment, Enabled: enabled, Why: why, Evidence: evidence})
		hh := headers[h]
		if hh == nil {
			hh = &HeaderHint{Header: h, Key: g.Key}
			headers[h] = hh
		}
		hh.Values = append(hh.Values, g.Value)
	}
	for _, h := range sortedKeys(headers) {
		ex.Headers = append(ex.Headers, *headers[h])
	}

	// Default rule: weighted over the tied models, more to the cheaper.
	targets, labels, wnote := defaultTargets(rep, byLabel)
	if len(targets) > 0 {
		why := fmt.Sprintf("Every other request goes to %s.", targetWords(targets, labels, byLabel))
		if wnote != "" {
			why += " " + wnote
		}
		evidence := "measured"
		if rep.Recommendation.Strength == StrengthStrong {
			evidence = "significant"
		}
		rules = append(rules, newRule("default", why+" "+rep.Recommendation.Summary, "true", targets, fallbacks(chain, labels), true))
		ex.Rules = append(ex.Rules, RuleWhy{Kind: RuleDefault, Enabled: true, Why: why, Evidence: evidence})
	}
	for i := range rules {
		rules[i].Priority = opt.BasePriority + i
		ex.Rules[i].Name = rules[i].Name
	}
	ex.Config.Governance.RoutingRules = rules

	for _, m := range rep.Models {
		if m.Runs == 0 {
			continue
		}
		h := BudgetHint{Label: m.Label, Model: display(m)}
		if c, ok := m.CostPerRun.Float(); ok {
			h.CostPerThousandRun = schemas.Observed(round2(c*1000), schemas.EvidenceGateway)
			h.Text = fmt.Sprintf("1,000 runs like these cost about %s on %s.", usd(c*1000), display(m))
		} else {
			h.CostPerThousandRun = schemas.Unknown(m.CostPerRun.Reason, schemas.EvidenceGateway)
			h.Text = fmt.Sprintf("The cost of %s is unknown: %s.", display(m), m.CostPerRun.Reason)
		}
		ex.Budget = append(ex.Budget, h)
	}
	if len(ex.Headers) > 0 {
		ex.Notes = append(ex.Notes, "Segment rules match a request header your app sets on each model call (for example "+ex.Headers[0].Header+"); requests without it go to the default rule.")
	}
	ex.Notes = append(ex.Notes, "The budget saver rule reads budget_used, which Bifrost fills only for requests under a governance budget (a virtual key, team or customer budget).")
	for _, m := range rep.Models {
		if m.Open {
			ex.Notes = append(ex.Notes, fmt.Sprintf("%s is served by your own %s endpoint: keep it deployed before you enable a rule that sends traffic to it.", display(m), m.Provider))
		}
		if m.Provider == "" && m.Runs > 0 {
			ex.Notes = append(ex.Notes, fmt.Sprintf("The provider of %s is not recorded, so its targets keep the request's provider and it is left out of fallback chains.", display(m)))
		}
	}
	return ex
}

// target is the routing target of a model; an agent's own model keeps the
// request's model.
func target(m Model) (RoutingTarget, bool) {
	if m.Runs == 0 {
		return RoutingTarget{}, false
	}
	t := RoutingTarget{Provider: m.Provider, Model: m.Model, Weight: 1}
	if m.Model == OwnModel {
		t.Model = ""
	}
	return t, true
}

// fallbackOrder ranks every model with runs by measured quality times
// availability (unknown availability ranks after a known one at the same
// product), so a fallback is the most likely to answer well.
func fallbackOrder(ms []Model) []Model {
	var out []Model
	for _, m := range ms {
		if _, ok := m.Quality.Float(); ok {
			out = append(out, m)
		}
	}
	score := func(m Model) (float64, bool) {
		q, _ := m.Quality.Float()
		a, ok := m.Availability.Float()
		if !ok {
			return q, false
		}
		return q * a, true
	}
	slices.SortStableFunc(out, func(a, b Model) int {
		sa, ka := score(a)
		sb, kb := score(b)
		if c := cmp.Compare(sb, sa); c != 0 {
			return c
		}
		return trueFirst(ka, kb)
	})
	return out
}

// fallbacks is the chain without the rule's own targets, as Bifrost's
// "provider/model" strings; a model without a recorded provider cannot be
// a fallback.
func fallbacks(chain []Model, skip []string) []string {
	var out []string
	for _, m := range chain {
		if slices.Contains(skip, m.Label) || m.Provider == "" {
			continue
		}
		s := m.Provider + "/"
		if m.Model != OwnModel {
			s += m.Model
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
		if len(out) == maxFallbacks {
			break
		}
	}
	return out
}

// cheapest is the model with runs and the lowest known cost per success.
func cheapest(ms []Model) (Model, bool) {
	var best Model
	found := false
	for _, m := range ms {
		c, ok := m.CostPerSuccess.Float()
		if !ok || m.Runs == 0 {
			continue
		}
		if b, _ := best.CostPerSuccess.Float(); !found || c < b {
			best, found = m, true
		}
	}
	return best, found
}

// defaultTargets are the default rule's targets: the recommended model
// alone when the choice is strong or only one model is tied; else up to
// maxDefaultTargets tied models weighted by inverse cost per success. It
// returns the labels in target order and a note on the weights.
func defaultTargets(rep *Report, byLabel map[string]Model) ([]RoutingTarget, []string, string) {
	def := byLabel[rep.Recommendation.Model]
	single := func(note string) ([]RoutingTarget, []string, string) {
		t, ok := target(def)
		if !ok {
			return nil, nil, ""
		}
		return []RoutingTarget{t}, []string{def.Label}, note
	}
	tied := rep.Recommendation.Tied
	if rep.Recommendation.Strength == StrengthStrong || len(tied) < 2 {
		return single("")
	}
	// The recommended model first, then the rest of the tied set by cost.
	order := []string{def.Label}
	rest := slices.DeleteFunc(slices.Clone(tied), func(l string) bool { return l == def.Label })
	slices.SortStableFunc(rest, func(a, b string) int {
		ca, _ := byLabel[a].CostPerSuccess.Float()
		cb, _ := byLabel[b].CostPerSuccess.Float()
		return cmp.Compare(ca, cb)
	})
	order = append(order, rest...)
	if len(order) > maxDefaultTargets {
		order = order[:maxDefaultTargets]
	}
	inv := make([]float64, len(order))
	for i, l := range order {
		c, ok := byLabel[l].CostPerSuccess.Float()
		if !ok || c <= 0 {
			return single(fmt.Sprintf("Traffic is not split: the cost per verified success of %s is unknown, so no weights can be measured.", display(byLabel[l])))
		}
		inv[i] = 1 / c
	}
	w := weights(inv)
	out := make([]RoutingTarget, 0, len(order))
	for i, l := range order {
		t, ok := target(byLabel[l])
		if !ok {
			return single("")
		}
		t.Weight = w[i]
		out = append(out, t)
	}
	return out, order, "The split is weighted by inverse cost per verified success among models that cannot be told apart from the best."
}

// weights normalises raw to weights of two decimals that sum to 1, none
// below minTargetWeight.
func weights(raw []float64) []float64 {
	n := len(raw)
	out := make([]float64, n)
	sum := 0.0
	for _, x := range raw {
		sum += x
	}
	for i, x := range raw {
		out[i] = math.Max(x/sum, minTargetWeight)
	}
	sum = 0
	for _, x := range out {
		sum += x
	}
	acc := 0.0
	for i := range out {
		if i == n-1 {
			out[i] = math.Round((1-acc)*100) / 100
			break
		}
		out[i] = math.Round(out[i]/sum*100) / 100
		acc += out[i]
	}
	return out
}

func targetWords(ts []RoutingTarget, labels []string, byLabel map[string]Model) string {
	if len(ts) == 1 {
		return display(byLabel[labels[0]])
	}
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = fmt.Sprintf("%s (%.0f%%)", display(byLabel[labels[i]]), t.Weight*100)
	}
	return joinWords(parts)
}

func qualityText(m Model) string {
	if q, ok := m.Quality.Float(); ok {
		return fmt.Sprintf("%.0f%%", q*100)
	}
	return "unknown"
}

func usdValue(v schemas.Value) string {
	if x, ok := v.Float(); ok {
		return usd(x)
	}
	return "unknown"
}

// safeValue keeps segment values a CEL string literal and a header value
// can carry as written.
func safeValue(s string) bool {
	if s == "" || len(s) > 120 {
		return false
	}
	for _, r := range s {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || r == '"' || r == '\\' {
			return false
		}
	}
	return true
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-3]) + "..."
}
