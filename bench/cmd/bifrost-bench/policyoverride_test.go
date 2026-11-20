package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestReleasePolicyOverrideAndNone covers 7-B-04 and 7-B-05: -policy none
// turns off the frozen policy too, and a deliberate override is not failed by
// the frozen hash check, while drift of the frozen file still is.
func TestReleasePolicyOverrideAndNone(t *testing.T) {
	root := t.TempDir()
	raw, err := os.ReadFile("../../policies/default.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	frozen := filepath.Join(root, "policies", "default.yaml")
	if err := os.WriteFile(frozen, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	alt := filepath.Join(root, "same-rules.yaml")
	if err := os.WriteFile(alt, append(append([]byte{}, raw...), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: &lib.Config{Root: root}, logger: schemas.NoopLogger{}}
	m := &schemas.Manifest{ExpID: "e_x", PolicyHash: policy.Hash(raw)}
	m.Experiment.Policy = "policies/default.yaml"
	an := &schemas.Analysis{}
	hasHashCheck := func(r *schemas.PolicyResult) bool {
		for _, c := range r.Checks {
			if c.ID == policy.CheckPolicyHash {
				return true
			}
		}
		return false
	}

	if r, err := a.releasePolicy(m, an, nil, policyNone); err != nil || r != nil {
		t.Fatalf("none: got %+v, %v; want no policy", r, err)
	}
	r, err := a.releasePolicy(m, an, nil, alt)
	if err != nil {
		t.Fatal(err)
	}
	if hasHashCheck(r) || r.PolicyHash != policy.Hash(append(append([]byte{}, raw...), '\n')) {
		t.Fatalf("override: frozen hash check applied or wrong hash: %+v", r)
	}
	if m.PolicyHash == "" {
		t.Fatal("override cleared the manifest's frozen hash")
	}
	if r, err := a.releasePolicy(m, an, nil, ""); err != nil || hasHashCheck(r) {
		t.Fatalf("unchanged frozen policy: %+v, %v", r, err)
	}
	if err := os.WriteFile(frozen, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := a.releasePolicy(m, an, nil, ""); err != nil || !hasHashCheck(r) || r.Decision != schemas.PolicyBlock {
		t.Fatalf("drifted frozen policy must block: %+v, %v", r, err)
	}
}
