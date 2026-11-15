package schemas

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalRunVersionedFields(t *testing.T) {
	run := CanonicalRun{
		CanonicalVersion: CanonicalVersion,
		SchemaVersion:    CanonicalSchemaVersion,
		GeneratedAt:      time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		RunID:            "r_test",
		Task:             CanonicalTaskIdentity{TaskID: "t01", TaskHash: "task-hash"},
		RunInput:         RunInputIdentity{TaskHash: "task-hash", RunInputHash: "input-hash", ArmConfigHash: "arm-hash"},
		EvidenceStatus:   EvidenceStatus{Level: EvidenceStrong, Sources: []EvidenceOrigin{EvidenceSourceBifrostLog}},
	}
	b, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, field := range []string{`"canonical_version":"1.0"`, `"schema_version":"bench-run-v2"`, `"generated_at"`, `"run_input"`, `"evidence_status"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("canonical JSON missing %s: %s", field, text)
		}
	}
}

func TestCanonicalTypesReadLegacyValue(t *testing.T) {
	var value Value
	if err := json.Unmarshal([]byte(`{"state":"observed","v":1,"evidence":"gateway"}`), &value); err != nil {
		t.Fatal(err)
	}
	if !value.IsObserved() || value.Evidence != EvidenceGateway || value.Source != "" || value.EvidenceLevel != "" {
		t.Fatalf("legacy value changed after read: %+v", value)
	}
}

func TestLegacyManifestOmitsAdditiveIdentityFields(t *testing.T) {
	var manifest Manifest
	if err := json.Unmarshal([]byte(`{"exp_id":"e_old","arms":{"A":{"label":"old","config_hash":"cfg","files":{}}}}`), &manifest); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"identity"`) || strings.Contains(string(b), `"task_identities"`) || strings.Contains(string(b), `"run_inputs"`) {
		t.Fatalf("legacy manifest gained new identity fields: %s", b)
	}
}
