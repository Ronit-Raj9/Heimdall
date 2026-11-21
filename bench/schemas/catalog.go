package schemas

// Mode labels where the data on screen came from. Every API response carries it.
type Mode string

// Modes.
const (
	ModeLive      Mode = "live"
	ModeRecorded  Mode = "recorded"
	ModeSynthetic Mode = "synthetic"
)

// Catalog lists every registered part. It drives the UI, so a new metric,
// probe or gate appears without UI changes.
type Catalog struct {
	Mode    Mode               `json:"mode"`
	Suites  []CatalogSuite     `json:"suites"`
	Probes  []ProbeSpec        `json:"probes"`
	Agents  []CatalogAgent     `json:"agents"`
	Metrics []MetricDescriptor `json:"metrics"`
	Gates   []CatalogGate      `json:"gates"`
	Parts   map[string]string  `json:"parts"` // "<kind>/<name>" -> version
}

// CatalogSuite summarizes a suite.
type CatalogSuite struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Tasks    []string `json:"tasks"`
	Probes   []string `json:"probes"`
	LockHash string   `json:"lock_hash,omitempty"`
	// ProbeLayers maps each probe id of this suite to its layer. Catalog.Probes
	// is flattened across suites, so two suites can share a probe id on
	// different layers; the receipts view reads the layer of the experiment's
	// own suite from here.
	ProbeLayers map[string]ProbeLayerName `json:"probe_layers,omitempty"`
}

// CatalogAgent summarizes an agent config file.
type CatalogAgent struct {
	ID           string               `json:"id"`
	Path         string               `json:"path"`
	Adapter      string               `json:"adapter"`
	Model        string               `json:"model"`
	Parser       string               `json:"parser,omitempty"`
	AgentVersion string               `json:"agent_version,omitempty"`
	ImageDigest  string               `json:"image_digest,omitempty"`
	Capabilities *AdapterCapabilities `json:"capabilities,omitempty"`
}

// CatalogGate summarizes a gate.
type CatalogGate struct {
	Name     string     `json:"name"`
	Version  string     `json:"version"`
	Effect   GateEffect `json:"effect"`
	Baseline bool       `json:"baseline"`
}

// Registry is the set of parts main.go assembles. Each slice comes from one
// package's Builtin(). It is built once and read-only afterwards.
type Registry struct {
	Loaders       []SuiteLoader
	Environments  []Environment
	Adapters      []AgentAdapter
	Parsers       []Parser
	ProbeLayers   []ProbeLayer
	Evidence      []EvidenceSource
	Graders       []Grader
	Gates         []Gate
	Metrics       []Metric
	Policies      []VerdictPolicy
	Reporters     []Reporter
	Gateway       Gateway
	AdapterFaults AdapterFaultController
	Fault         FaultController // nil when no fault proxy is configured
	Store         Store
	GPUs          GPUAllocator // X7: GPU pool of this machine (schemas/gpu.go); nil when GPU tasks are not supported here
	// Secrets resolves the org secrets a run names (external api auth_env,
	// agent env {{secret.NAME}}); nil keeps the bench's own environment
	// (schemas/secrets.go).
	Secrets SecretSource
}
