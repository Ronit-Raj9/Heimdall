package gwfeature

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func evidenceJSON(t *testing.T, rows []schemas.GatewayRequest, complete bool) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"requests": schemas.RequestLog{Requests: rows, Complete: complete}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadRowsMergesRawDetail(t *testing.T) {
	src := newFakeSource()
	cost := 0.02
	src.setFile("r1", "evidence.json", evidenceJSON(t, []schemas.GatewayRequest{
		{ID: "g1", Kind: "message", Status: "success", Provider: "openai", ModelRequested: "m1", ModelServed: "m1", LatencyMS: 120, BifrostUSD: &cost},
		{ID: "g2", Kind: "message", Status: "error", Provider: "openai", ModelRequested: "m1", ErrorStatus: 429, BudgetRefused: true},
	}, true))
	src.setFile("r1", "gateway.raw.jsonl", []byte(`{"request_id":"g1","raw_available":true,"raw_detail":{"served_model":"m1-2026","complexity_tier":"complex","complexity_mechanism":"semantic","complexity_score":0.81,"selected_prompt_name":"triage","selected_prompt_version":"3","cache_debug":{"similarity":0.93,"threshold":0.9},"overhead_breakdown":[{"name":"plugin.governance","duration_us":150},{"name":"key.selection","duration_us":20}]}}
{"request_id":"g2","raw_available":false,"raw_detail":null}
`))
	rows, complete, raw, err := ReadRows(src, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if !complete || raw != 1 || len(rows) != 2 {
		t.Fatalf("complete %v raw %d rows %d", complete, raw, len(rows))
	}
	r := rows[0]
	if !r.RawKnown || r.ComplexityTier != "complex" || r.ComplexityMechanism != "semantic" || r.ComplexityScore == nil || *r.ComplexityScore != 0.81 {
		t.Fatalf("row 1 complexity: %+v", r)
	}
	if r.PromptName != "triage" || r.PromptVersion != "3" || r.CacheSimilarity == nil || *r.CacheSimilarity != 0.93 || r.CacheThreshold == nil || *r.CacheThreshold != 0.9 {
		t.Fatalf("row 1 prompt or cache: %+v", r)
	}
	if len(r.Overhead) != 2 || r.Overhead[0] != (Bucket{Name: "plugin.governance", US: 150}) {
		t.Fatalf("row 1 overhead: %+v", r.Overhead)
	}
	if r.ModelServed != "m1-2026" || r.CostUSD == nil || *r.CostUSD != 0.02 || r.LatencyMS != 120 || r.Kind != "message" {
		t.Fatalf("row 1 summary fields: %+v", r)
	}
	r = rows[1]
	if r.RawKnown || r.ComplexityScore != nil || r.Status != "error" || r.ErrorStatus != 429 || !r.BudgetRefused {
		t.Fatalf("row 2: %+v", r)
	}
}

func TestReadRowsServedModelUnknownWithoutEvidence(t *testing.T) {
	src := newFakeSource()
	src.setFile("r1", "evidence.json", evidenceJSON(t, []schemas.GatewayRequest{
		{ID: "g1", Kind: "message", Status: "success", ModelRequested: "m1", ModelServed: "m1"},
		{ID: "g2", Kind: "message", Status: "success", ModelRequested: "m1", ModelServed: "m2"},
		{ID: "g3", Kind: "message", Status: "success", ModelRequested: "m1", ModelServed: "m1"},
	}, false))
	src.setFile("r1", "gateway.raw.jsonl", []byte(`{"request_id":"g3","raw_available":true,"raw_detail":{"raw_response":"{\"model\":\"m1\"}"}}`+"\n"))
	rows, complete, _, err := ReadRows(src, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if complete {
		t.Fatal("complete should follow the request log")
	}
	if rows[0].ModelServed != "" {
		t.Fatalf("row 1 served = %q, want unknown", rows[0].ModelServed)
	}
	if rows[1].ModelServed != "m2" {
		t.Fatalf("row 2 served = %q, want m2", rows[1].ModelServed)
	}
	if rows[2].ModelServed != "m1" {
		t.Fatalf("row 3 served = %q, want m1 from the raw response", rows[2].ModelServed)
	}
}

func TestReadRowsMissingRawFile(t *testing.T) {
	src := newFakeSource()
	src.setFile("r1", "evidence.json", evidenceJSON(t, []schemas.GatewayRequest{{ID: "g1", Kind: "message", Status: "success"}}, true))
	rows, _, raw, err := ReadRows(src, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if raw != 0 || len(rows) != 1 || rows[0].RawKnown {
		t.Fatalf("raw %d rows %+v", raw, rows)
	}
}
