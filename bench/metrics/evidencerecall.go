package metrics

import (
	"context"
	"encoding/json"

	"github.com/maximhq/bifrost/bench/research"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X5a research importers (docs/importers-research.md): evidence_recall,
// BrowseComp-Plus's retrieval recall. It is the share of the task's
// evidence pages (evidence_urls of its research spec) that the run's own
// searches returned, read from the tool proxy's ledger: results[].url of
// every successful search call to a Tavily API or to a suite's corpus
// service (a Tavily-shaped search over a fixed corpus, reached at
// /s/corpus, protocol http). Fetches do not count: as in BrowseComp-Plus,
// a page counts as retrieved when the search engine returned it.
const EvidenceRecall = "evidence_recall"

// CorpusService is the id of a fixed-corpus search service of an imported
// research suite (importers/browsecompplus writes it).
const CorpusService = research.CorpusService

var _ schemas.Metric = (*evidenceRecallMetric)(nil)

type evidenceRecallMetric struct{}

// GetName implements schemas.Part.
func (m *evidenceRecallMetric) GetName() string { return EvidenceRecall }

// GetVersion implements schemas.Part.
func (m *evidenceRecallMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *evidenceRecallMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           EvidenceRecall,
		Version:        version,
		Label:          "Evidence recall",
		Kind:           schemas.KindRate,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceBoundary,
		Description:    "Share of the task's evidence pages that the run's own successful searches (Tavily or a suite corpus service) returned according to the tool proxy's ledger, averaged over research runs of tasks that list evidence pages.",
	}
}

// Compute implements schemas.Metric.
func (m *evidenceRecallMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !research.IsResearchRun(rc) {
		return schemas.NotApplicable(), nil
	}
	spec, err := research.LoadSpec(rc.Task)
	if err != nil {
		return schemas.Unknown(reasonNoSpec+": "+err.Error(), schemas.EvidenceBoundary), nil
	}
	evidence := spec.EvidenceSet()
	if len(evidence) == 0 {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), nil
	}
	found := searchedURLs(ev.ToolCalls, research.TavilyAPIs(rc.Suite, rc.Task))
	n := 0
	for u := range evidence {
		if found[u] {
			n++
		}
	}
	return schemas.Observed(float64(n)/float64(len(evidence)), schemas.EvidenceBoundary), nil
}

// searchedURLs are the normalized result URLs of the successful search
// calls in the ledger: Tavily search rows (as research.IsSearch) and the
// corpus service's POST /search rows. A poisoned result still counts for its
// results; an error, refusal, unmatched replay or injected failure does not.
func searchedURLs(calls []schemas.ToolCall, apis map[string]bool) map[string]bool {
	var rows []schemas.ToolCall
	for _, c := range calls {
		corpus := c.Protocol == "http" && c.Server == CorpusService && c.Tool == research.SearchTool
		if research.IsSearch(c, apis) || corpus { // the corpus check also covers ledgers read without the suite's services
			rows = append(rows, c)
		}
	}
	out := map[string]bool{}
	for _, c := range rows {
		if c.IsError || c.Blocked || c.Unmatched || c.Status != 200 || (c.Fault != "" && c.Fault != "poison" && c.Fault != "slow" && c.Fault != "duplicate_effect") { // slow and duplicate_effect rows carry the real answer
			continue
		}
		var body struct {
			Results []struct {
				URL string `json:"url"`
			} `json:"results"`
		}
		if json.Unmarshal([]byte(c.Result), &body) == nil {
			for _, r := range body.Results {
				if u := research.NormalizeURL(r.URL); u != "" {
					out[u] = true
				}
			}
			continue
		}
		// A result the ledger clipped: research.ReturnedURLs scans it for
		// url keys; give it the row as a Tavily search row.
		x := c
		x.Protocol, x.Server = "external", "clipped"
		for u := range research.ReturnedURLs([]schemas.ToolCall{x}, map[string]bool{"clipped": true}) {
			out[u] = true
		}
	}
	return out
}

// newEvidenceRecallMetric returns the evidence_recall metric.
func newEvidenceRecallMetric() *evidenceRecallMetric { return &evidenceRecallMetric{} }
