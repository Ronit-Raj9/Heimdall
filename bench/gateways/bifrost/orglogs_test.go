package bifrost

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X03: a key minted for an organization's run carries the organization in
// its metadata, and content logging is off when the organization says so; a
// key of a run with no organization is untouched.
func TestMintKeyTagsOrganization(t *testing.T) {
	f := newFakeBifrost(t)
	g := newTestGateway(t, f)
	ctx := context.Background()
	if err := g.Setup(ctx); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	rc := func(run, exp string) *schemas.RunContext {
		c := runContext(run, testModel)
		c.Run.ExpID = exp
		return c
	}

	g.SetExperimentLogPolicy("e_keep", "org_a", schemas.PromptLogPolicy{KeepContent: true, RetentionDays: 7})
	k, err := g.MintKey(ctx, rc("r_keep0001", "e_keep"))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	body := f.lastBody("POST /api/governance/virtual-keys")
	meta, _ := body["metadata"].(map[string]any)
	if meta[OrgLogMetadataKey] != "org_a" || meta[RunLogMetadataKey] != "r_keep0001" {
		t.Errorf("metadata = %v", body["metadata"])
	}
	if _, set := body["disable_content_logging"]; set {
		t.Errorf("keep_content org: disable_content_logging set: %v", body)
	}
	if err := g.RevokeKey(ctx, k); err != nil {
		t.Fatal(err)
	}

	g.SetExperimentLogPolicy("e_drop", "org_b", schemas.PromptLogPolicy{KeepContent: false, RetentionDays: 7})
	if _, err := g.MintKey(ctx, rc("r_drop0001", "e_drop")); err != nil {
		t.Fatalf("mint: %v", err)
	}
	body = f.lastBody("POST /api/governance/virtual-keys")
	meta, _ = body["metadata"].(map[string]any)
	if meta[OrgLogMetadataKey] != "org_b" || body["disable_content_logging"] != true {
		t.Errorf("content off org body = %v", body)
	}

	if _, err := g.MintKey(ctx, rc("r_local001", "e_local")); err != nil {
		t.Fatalf("mint: %v", err)
	}
	body = f.lastBody("POST /api/governance/virtual-keys")
	if _, set := body["metadata"]; set {
		t.Errorf("run without an organization tagged: %v", body)
	}
	if _, set := body["disable_content_logging"]; set {
		t.Errorf("run without an organization: content flag set: %v", body)
	}
}

// Team members and derived keys share the run id prefix, so they are tagged
// too.
func TestAddLogFieldsMemberKey(t *testing.T) {
	g := &Gateway{}
	g.SetExperimentLogPolicy("e_1", "org_a", schemas.PromptLogPolicy{KeepContent: false})
	g.noteRunOrg(&schemas.RunContext{Run: &schemas.Run{RunID: "r_team0001", ExpID: "e_1"}})
	body := map[string]any{}
	g.addLogFields(body, "r_team0001--critic")
	meta, _ := body["metadata"].(map[string]string)
	if meta[OrgLogMetadataKey] != "org_a" || meta[RunLogMetadataKey] != "r_team0001" || body["disable_content_logging"] != true {
		t.Errorf("member key body = %v", body)
	}
	other := map[string]any{}
	g.addLogFields(other, "not-a-run")
	if len(other) != 0 {
		t.Errorf("non-run key tagged: %v", other)
	}
}
