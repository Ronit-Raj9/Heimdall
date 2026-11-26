package schemas

// W4 Bifrost feature tests (audit/W4_DESIGN_3_BIFROST.md section 3). An
// experiment that tests one Bifrost gateway setting names it here, and
// bench/gwfeature reads its runs and gateway rows into a feature report.
// nil for every other experiment, so recorded manifests keep their hashes.

// Bifrost feature ids (GatewayTest.Feature).
const (
	GatewayFeatureComplexityRouter = "complexity_router"
	GatewayFeaturePromptVersion    = "prompt_version"
	GatewayFeatureSkillVersion     = "skill_version"
	GatewayFeatureSemanticCache    = "semantic_cache"
	GatewayFeatureMCPTools         = "mcp_tools"
	GatewayFeatureProviders        = "providers"
	GatewayFeatureBudgetCurve      = "budget_curve"
	GatewayFeatureOverhead         = "gateway_overhead"
)

// GatewayTest marks an experiment as a test of one Bifrost feature.
type GatewayTest struct {
	Feature  string            `json:"feature" yaml:"feature"`
	Control  string            `json:"control" yaml:"control"`                     // arm id: the setting as it is today
	Variants []string          `json:"variants" yaml:"variants"`                   // arm ids that change the one setting, in display order
	Options  map[string]string `json:"options,omitempty" yaml:"options,omitempty"` // feature knobs, e.g. "tolerance": "0.03"
}
