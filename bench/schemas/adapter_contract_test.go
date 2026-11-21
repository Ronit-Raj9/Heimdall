package schemas

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestAgentConfigLegacyDecodeRemainsCompatible(t *testing.T) {
	var cfg AgentConfig
	if err := yaml.Unmarshal([]byte("id: legacy\nadapter: command\nimage: image:1\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ID != "legacy" || cfg.Adapter != "command" || cfg.Image != "image:1" {
		t.Fatalf("legacy config = %+v", cfg)
	}
	if cfg.Capabilities != nil || cfg.AgentVersion != "" || cfg.ImageDigest != "" {
		t.Fatalf("new optional fields changed legacy defaults: %+v", cfg)
	}
}

func TestAdapterContractFieldsRoundTrip(t *testing.T) {
	cfg := AgentConfig{
		ID: "agent", Adapter: "command", Image: "image:1", AgentVersion: "2.0.0",
		ImageDigest: "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		AdapterCaps: &AdapterCapabilities{
			SupportsStructuredEvents: true,
			SupportsToolEvents:       true,
			TaskTypes:                []string{"coding"},
			EvidenceLevels:           []EvidenceLevel{EvidenceGateway, EvidenceNative},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got AgentConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.AgentVersion != cfg.AgentVersion || got.ImageDigest != cfg.ImageDigest ||
		got.AdapterCaps == nil || !got.AdapterCaps.SupportsToolEvents ||
		len(got.AdapterCaps.TaskTypes) != 1 || len(got.AdapterCaps.EvidenceLevels) != 2 {
		t.Fatalf("contract fields did not round trip: %+v", got)
	}
}

func TestExecutionPlanIsRunPlanCompatible(t *testing.T) {
	plan := &ExecutionPlan{Image: "image:1", Argv: []string{"agent"}}
	if plan.Image != "image:1" || len(plan.Argv) != 1 {
		t.Fatalf("execution plan = %+v", plan)
	}
	if _, ok := any(plan).(*RunPlan); !ok {
		t.Fatal("execution plan alias is not compatible with RunPlan")
	}
}

func TestCommonEventFieldsRoundTrip(t *testing.T) {
	status := EventStatusSuccess
	exit := 0
	e := Event{
		EventVersion: EventSchemaVersion, EventID: "r:event:1", RunID: "r",
		Seq: 1, Sequence: 1, TS: time.Unix(1, 0).UTC(), Timestamp: time.Unix(1, 0).UTC(),
		Type: EventToolResult, Source: SourceShimLog, Evidence: EvidenceBoundary,
		EvidenceLevel: EvidenceBoundary, Status: status, ParserVersion: "p1",
		ToolName: "shell", ArgsHash: "a", ResultHash: "b", ExitStatus: &exit,
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.EventVersion != EventSchemaVersion || got.Sequence != 1 || got.Status != status ||
		got.EvidenceLevel != EvidenceBoundary || got.ParserVersion != "p1" || got.ExitStatus == nil {
		t.Fatalf("event contract fields did not round trip: %+v", got)
	}
}

func TestRunEvidenceContractRoundTrip(t *testing.T) {
	e := Evidence{Contract: &RunEvidenceContract{
		Required:  []string{"gateway_requests", "tool_execution"},
		Available: []string{"gateway_requests"}, Missing: []string{"tool_execution"},
		Quality: EvidenceQualityPartial,
	}}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got Evidence
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Contract == nil || got.Contract.Quality != EvidenceQualityPartial || len(got.Contract.Missing) != 1 {
		t.Fatalf("evidence contract did not round trip: %+v", got)
	}
}
