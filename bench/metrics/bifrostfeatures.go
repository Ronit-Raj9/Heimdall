package metrics

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Metrics of Bifrost features under test (B2, docs/bifrost-features.md).
// They read Bifrost's own rows and, for MCP through Bifrost, compare
// Bifrost's MCP log with the tool proxy ledger, which stays the trusted
// witness of what ran.
const (
	InputTokens               = "input_tokens"
	CacheReadShare            = "cache_read_share"
	SemanticCacheHits         = "semantic_cache_hits"
	SemanticCacheForeignHits  = "semantic_cache_foreign_hits"
	BifrostMCPCalls           = "bifrost_mcp_calls"
	BifrostMCPWitnessMismatch = "bifrost_mcp_witness_mismatch"
)

var (
	_ schemas.Metric = (*inputTokensMetric)(nil)
	_ schemas.Metric = (*cacheReadShareMetric)(nil)
	_ schemas.Metric = (*semanticCacheMetric)(nil)
	_ schemas.Metric = (*bifrostMCPMetric)(nil)
)

// inputTokensMetric totals the input side of every model request: fresh,
// cache read and cache write. It is what MCP Code Mode and prompt caching
// claim to change, kept apart from output.
type inputTokensMetric struct{}

func newInputTokensMetric() *inputTokensMetric { return &inputTokensMetric{} }

// GetName implements schemas.Part.
func (m *inputTokensMetric) GetName() string { return InputTokens }

// GetVersion implements schemas.Part.
func (m *inputTokensMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *inputTokensMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: InputTokens, Version: version, Label: "Input tokens", Kind: schemas.KindTokens,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Input tokens per run (fresh plus cache read plus cache write, the quantity MCP Code Mode claims to cut) over every model request row of the gateway log, as the provider reported them to Bifrost, averaged over runs whose every model request recorded usage.",
	}
}

// Compute implements schemas.Metric.
func (m *inputTokensMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	in, _, err := inputSplit(ev.Requests.Requests)
	if err != "" {
		return schemas.Unknown(err, schemas.EvidenceGateway), nil
	}
	return schemas.Observed(float64(in), schemas.EvidenceGateway), nil
}

// inputSplit returns the input tokens and the cache-read tokens over the
// message rows, or the reason they are unknown. Error rows carry no usage
// and are skipped, as the tokens metric's tally does.
func inputSplit(reqs []schemas.GatewayRequest) (in, read int64, problem string) {
	for _, r := range messageRows(reqs) {
		if r.Status == "error" && r.Tokens == nil {
			continue
		}
		if r.Tokens == nil {
			return 0, 0, fmt.Sprintf("message request %s has no usage", r.ID)
		}
		in += r.Tokens.Fresh + r.Tokens.CacheRead + r.Tokens.CacheWrite
		read += r.Tokens.CacheRead
	}
	return in, read, ""
}

// cacheReadShareMetric is the share of a run's input tokens the provider
// read from its prompt cache.
type cacheReadShareMetric struct{}

func newCacheReadShareMetric() *cacheReadShareMetric { return &cacheReadShareMetric{} }

// GetName implements schemas.Part.
func (m *cacheReadShareMetric) GetName() string { return CacheReadShare }

// GetVersion implements schemas.Part.
func (m *cacheReadShareMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *cacheReadShareMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: CacheReadShare, Version: version, Label: "Prompt cache read share", Kind: schemas.KindRate,
		HigherIsBetter: true, MinEvidence: schemas.EvidenceGateway,
		Description: "Cache-read input tokens over all input tokens of the run's model requests, as the provider reported them to Bifrost (what provider prompt caching, Bifrost's auto prompt caching among it, saved), averaged over runs with input tokens whose every model request recorded usage.",
	}
}

// Compute implements schemas.Metric.
func (m *cacheReadShareMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	in, read, problem := inputSplit(ev.Requests.Requests)
	switch {
	case problem != "":
		return schemas.Unknown(problem, schemas.EvidenceGateway), nil
	case in == 0:
		return schemas.NotApplicable(), nil
	}
	return schemas.Observed(round3(float64(read)/float64(in)), schemas.EvidenceGateway), nil
}

// semanticCacheMetric counts model requests Bifrost answered from its
// semantic cache, or (foreign) those whose cached reply was first produced
// outside the run: a reply replayed from another trial, arm or task.
type semanticCacheMetric struct {
	name    string
	foreign bool
}

func newSemanticCacheMetric(name string) *semanticCacheMetric {
	return &semanticCacheMetric{name: name, foreign: name == SemanticCacheForeignHits}
}

// GetName implements schemas.Part.
func (m *semanticCacheMetric) GetName() string { return m.name }

// GetVersion implements schemas.Part.
func (m *semanticCacheMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *semanticCacheMetric) Descriptor() schemas.MetricDescriptor {
	if m.foreign {
		return schemas.MetricDescriptor{
			Name: m.name, Version: version, Label: "Semantic cache replays from other runs", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
			Description: "Model requests per run answered from Bifrost's semantic cache with an entry (cache_id, else provider message id) no miss row of this run stored (an answer first given to another trial, arm or task and replayed here, so the run is not an independent sample of the model), averaged over runs with a complete gateway log and a model request; report only.",
		}
	}
	return schemas.MetricDescriptor{
		Name: m.name, Version: version, Label: "Semantic cache hits", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Model requests per run that Bifrost answered from its semantic cache (cache_debug.cache_hit on the row: no provider call, no fresh output), averaged over runs with a complete gateway log and a model request; report only.",
	}
}

// Compute implements schemas.Metric.
func (m *semanticCacheMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	if v, bad := requestLogProblem(ev.Requests); bad {
		return v, nil
	}
	rows := messageRows(ev.Requests.Requests)
	if len(rows) == 0 {
		return schemas.NotApplicable(), nil
	}
	stored := map[string]bool{}
	for _, r := range rows {
		if !r.CacheHit {
			stored[cacheOrigin(r)] = true
		}
	}
	n := 0
	for _, r := range rows {
		if !r.CacheHit {
			continue
		}
		if o := cacheOrigin(r); !m.foreign || o == "" || !stored[o] {
			n++
		}
	}
	return schemas.Observed(float64(n), schemas.EvidenceGateway), nil
}

// bifrostMCPMetric reads a run's MCP-through-Bifrost evidence: the number of
// tool executions Bifrost logged, or the number on which Bifrost's log and
// the tool proxy ledger disagree.
type bifrostMCPMetric struct {
	name     string
	mismatch bool
}

func newBifrostMCPMetric(name string) *bifrostMCPMetric {
	return &bifrostMCPMetric{name: name, mismatch: name == BifrostMCPWitnessMismatch}
}

// GetName implements schemas.Part.
func (m *bifrostMCPMetric) GetName() string { return m.name }

// GetVersion implements schemas.Part.
func (m *bifrostMCPMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *bifrostMCPMetric) Descriptor() schemas.MetricDescriptor {
	if m.mismatch {
		return schemas.MetricDescriptor{
			Name: m.name, Version: version, Label: "Bifrost MCP log vs tool ledger mismatches", Kind: schemas.KindCount,
			HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
			Description: "Tool calls on which Bifrost's mcp_tool_logs and the tool proxy ledger disagree (a call one witness saw and the other did not, matched by service, tool and canonical arguments: a call that bypassed the gateway, or one the gateway logged that never reached the tool), averaged over runs whose MCP servers were served through Bifrost and whose MCP log was read completely; zero means the witnesses agree.",
		}
	}
	return schemas.MetricDescriptor{
		Name: m.name, Version: version, Label: "Tool calls in Bifrost's MCP log", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceGateway,
		Description: "Tool executions Bifrost logged for the run's virtual key in mcp_tool_logs (a Code Mode program logs each tool it calls, never executeToolCode itself), averaged over runs whose MCP servers were served through Bifrost and whose MCP log was read completely.",
	}
}

// Compute implements schemas.Metric.
func (m *bifrostMCPMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	ev = orEmpty(ev)
	b := ev.BifrostMCP
	if b == nil || len(b.Clients) == 0 {
		return schemas.NotApplicable(), nil
	}
	if !b.Complete {
		return schemas.Unknown("Bifrost's MCP log was not read completely: "+b.Reason, schemas.EvidenceGateway), nil
	}
	if !m.mismatch {
		return schemas.Observed(float64(len(b.Calls)), schemas.EvidenceGateway), nil
	}
	only, _ := MCPWitnessDiff(ev.ToolCalls, b.Calls)
	return schemas.Observed(float64(len(only)), schemas.EvidenceBoundary), nil
}

// MCPWitnessDiff matches the tool proxy ledger's MCP tool calls with
// Bifrost's MCP log rows by service, tool and canonical arguments, as
// multisets, and returns one line per call only one witness saw ("ledger
// only: desk get_ticket" or "bifrost only: ...") and the number matched.
func MCPWitnessDiff(ledger []schemas.ToolCall, calls []schemas.BifrostMCPCall) ([]string, int) {
	key := func(svc, tool, args string) string { return svc + "\x00" + tool + "\x00" + args }
	count := map[string]int{}
	label := map[string]string{}
	for _, c := range ledger {
		if c.Kind != "" || c.Protocol != "mcp" {
			continue
		}
		k := key(c.Server, c.Tool, utils.CanonicalArgsSHA256(c.Arguments))
		count[k]++
		label[k] = c.Server + " " + c.Tool
	}
	matched := 0
	var only []string
	for _, c := range calls {
		k := key(c.Service, c.Tool, c.ArgsSHA256)
		if count[k] > 0 {
			count[k]--
			matched++
			continue
		}
		only = append(only, "bifrost only: "+c.Service+" "+c.Tool)
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for i := 0; i < count[k]; i++ {
			only = append(only, "ledger only: "+label[k])
		}
	}
	sort.SliceStable(only, func(i, j int) bool { return strings.Compare(only[i], only[j]) < 0 })
	return only, matched
}

// cacheOrigin identifies the cached reply a row stored or was served: the
// semantic cache entry id Bifrost logged, else the provider message id.
func cacheOrigin(r schemas.GatewayRequest) string {
	if r.CacheID != "" {
		return "cache:" + r.CacheID
	}
	if r.ResponseID != "" {
		return "msg:" + r.ResponseID
	}
	return ""
}
