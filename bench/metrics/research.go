package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/schemas"
)

// W4 research metrics (docs/research-tasks.md). They read the tool proxy's
// ledger (the Tavily search and extract rows) and the run's cited sources
// (sources.json, or the Sources section of the answer), and, for
// citation_precision, the task's hidden research spec. They apply to runs of
// research suites (track research) and of any task with a Tavily API; every
// other run reports not applicable.
const (
	Searches          = "searches"
	SourcesCited      = "sources_cited"
	CitationsGrounded = "citations_grounded"
	CitationPrecision = "citation_precision"
)

const (
	reasonNoRunFolder = "run folder missing"
	reasonNoSpec      = "research spec unreadable"
)

var (
	_ schemas.Metric = (*searchesMetric)(nil)
	_ schemas.Metric = (*sourcesCitedMetric)(nil)
	_ schemas.Metric = (*citationsGroundedMetric)(nil)
	_ schemas.Metric = (*citationPrecisionMetric)(nil)
)

// searchesMetric counts the run's calls to a Tavily search endpoint.
type searchesMetric struct{}

// GetName implements schemas.Part.
func (m *searchesMetric) GetName() string { return Searches }

// GetVersion implements schemas.Part.
func (m *searchesMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric. Lower is better, like
// external_calls: the verifier judges the answer, and fewer searches reach it
// more cheaply; the number is a diagnostic.
func (m *searchesMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           Searches,
		Version:        version,
		Label:          "Searches",
		Kind:           schemas.KindCount,
		HigherIsBetter: false,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Calls to a search endpoint (Tavily, or the fixed-corpus search service when the suite declares it) that the tool proxy recorded for the run (through the websearch tool or direct, answered or not), averaged over runs of research tasks.",
	}
}

// Compute implements schemas.Metric.
func (m *searchesMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !research.IsResearchRun(rc) {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), nil
	}
	apis := research.TavilyAPIs(rc.Suite, rc.Task)
	n := 0
	for _, c := range ev.ToolCalls {
		if research.IsSearch(c, apis) {
			n++
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceBoundary), nil
}

// newSearchesMetric returns the searches metric.
func newSearchesMetric() *searchesMetric { return &searchesMetric{} }

// sourcesCitedMetric counts the distinct URLs the run's answer cites.
type sourcesCitedMetric struct{}

// GetName implements schemas.Part.
func (m *sourcesCitedMetric) GetName() string { return SourcesCited }

// GetVersion implements schemas.Part.
func (m *sourcesCitedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric. A count of citations is neither
// good nor bad on its own; it is reported higher-is-better only because the
// direction table has no neutral rows, and no release check should use it.
func (m *sourcesCitedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           SourcesCited,
		Version:        version,
		Label:          "Sources cited",
		Kind:           schemas.KindCount,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Distinct URLs the run's answer cites in sources.json or its Sources section (zero when it cites none or the file does not parse), averaged over runs of research tasks.",
	}
}

// Compute implements schemas.Metric.
func (m *sourcesCitedMetric) Compute(_ context.Context, rc *schemas.RunContext, _ *schemas.Evidence) (schemas.Value, error) {
	if !research.IsResearchRun(rc) {
		return schemas.NotApplicable(), nil
	}
	cites, v, ok := citations(rc)
	if !ok {
		return v, nil
	}
	return schemas.Observed(float64(len(cites.URLs)), schemas.EvidenceOutcome), nil
}

// newSourcesCitedMetric returns the sources_cited metric.
func newSourcesCitedMetric() *sourcesCitedMetric { return &sourcesCitedMetric{} }

// citationsGroundedMetric is the share of cited URLs that a search or fetch
// of this run actually returned, per the tool proxy's ledger. An invented
// citation, or a real page cited from memory, is not grounded.
type citationsGroundedMetric struct{}

// GetName implements schemas.Part.
func (m *citationsGroundedMetric) GetName() string { return CitationsGrounded }

// GetVersion implements schemas.Part.
func (m *citationsGroundedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *citationsGroundedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           CitationsGrounded,
		Version:        version,
		Label:          "Citations grounded",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Share of the run's cited URLs that one of its own successful search or fetch calls (Tavily, or the fixed-corpus search service when the suite declares it) returned according to the tool proxy's ledger, averaged over research runs that cite at least one URL.",
	}
}

// Compute implements schemas.Metric.
func (m *citationsGroundedMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !research.IsResearchRun(rc) {
		return schemas.NotApplicable(), nil
	}
	cites, v, ok := citations(rc)
	if !ok {
		return v, nil
	}
	if len(cites.URLs) == 0 {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), nil
	}
	returned := research.ReturnedURLs(ev.ToolCalls, research.TavilyAPIs(rc.Suite, rc.Task))
	n := 0
	for _, u := range cites.URLs {
		if returned[u] {
			n++
		}
	}
	return schemas.Observed(float64(n)/float64(len(cites.URLs)), schemas.EvidenceBoundary), nil
}

// newCitationsGroundedMetric returns the citations_grounded metric.
func newCitationsGroundedMetric() *citationsGroundedMetric { return &citationsGroundedMetric{} }

// citationPrecisionMetric is the share of cited URLs that hold the answer's
// evidence, per the task's hidden evidence_urls.
type citationPrecisionMetric struct{}

// GetName implements schemas.Part.
func (m *citationPrecisionMetric) GetName() string { return CitationPrecision }

// GetVersion implements schemas.Part.
func (m *citationPrecisionMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *citationPrecisionMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           CitationPrecision,
		Version:        version,
		Label:          "Citation precision",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Share of the run's cited URLs that the task's hidden research spec lists as holding the answer's evidence, averaged over research runs that cite at least one URL on tasks that list evidence pages.",
	}
}

// Compute implements schemas.Metric.
func (m *citationPrecisionMetric) Compute(_ context.Context, rc *schemas.RunContext, _ *schemas.Evidence) (schemas.Value, error) {
	if !research.IsResearchRun(rc) {
		return schemas.NotApplicable(), nil
	}
	spec, err := research.LoadSpec(rc.Task)
	if err != nil {
		return schemas.Unknown(reasonNoSpec+": "+err.Error(), schemas.EvidenceOutcome), nil
	}
	evidence := spec.EvidenceSet()
	if len(evidence) == 0 {
		return schemas.NotApplicable(), nil
	}
	cites, v, ok := citations(rc)
	if !ok {
		return v, nil
	}
	if len(cites.URLs) == 0 {
		return schemas.NotApplicable(), nil
	}
	n := 0
	for _, u := range cites.URLs {
		if evidence[u] {
			n++
		}
	}
	return schemas.Observed(float64(n)/float64(len(cites.URLs)), schemas.EvidenceOutcome), nil
}

// newCitationPrecisionMetric returns the citation_precision metric.
func newCitationPrecisionMetric() *citationPrecisionMetric { return &citationPrecisionMetric{} }

// citations reads the run's cited URLs, or the value to report instead and
// false when the run folder cannot be read.
func citations(rc *schemas.RunContext) (research.Citations, schemas.Value, bool) {
	if rc.Dir == "" {
		return research.Citations{}, schemas.Unknown(reasonNoRunFolder, schemas.EvidenceOutcome), false
	}
	c, err := research.ReadCitations(rc.Dir)
	if err != nil {
		return research.Citations{}, schemas.Unknown(err.Error(), schemas.EvidenceOutcome), false
	}
	return c, schemas.Value{}, true
}
