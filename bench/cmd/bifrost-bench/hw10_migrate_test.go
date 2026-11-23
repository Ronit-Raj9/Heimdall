package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/migration"
	"github.com/maximhq/bifrost/bench/schemas"
)

// N280: an experiment that froze no release rule set gets the statistics
// alone on Model switch, even when policies/default.yaml exists.
func TestMigrationNoImplicitDefaultPolicy(t *testing.T) {
	a := recordedApp(t)
	rep, err := a.migrationReport(context.Background(), "e_smoke_sdoj", schemas.GradeSourceOriginal, "", true, migration.Query{From: "A"})
	if err != nil {
		t.Fatalf("migrate report: %v", err)
	}
	if rep.Policy != "" {
		t.Errorf("policy %q, want none: the comparison froze no rule set", rep.Policy)
	}
	for _, p := range rep.Pairs {
		if p.Recommendation.Policy != nil {
			t.Errorf("pair %s to %s evaluated rule set %q", p.From.Arm, p.To.Arm, p.Recommendation.Policy.PolicyName)
		}
	}
}

// N280, N61: the rule set is named by its file, never by the name inside it.
func TestMigrationNamesPolicyByFile(t *testing.T) {
	a := recordedApp(t)
	rep, err := a.migrationReport(context.Background(), "e_pilot_mixed_nc93", schemas.GradeSourceOriginal, "", true, migration.Query{})
	if err != nil {
		t.Fatalf("migrate report: %v", err)
	}
	if rep.Policy != "default" {
		t.Errorf("policy %q, want the file name default", rep.Policy)
	}
	pr := rep.Pairs[0].Recommendation.Policy
	if pr == nil || pr.PolicyFile != "default" {
		t.Fatalf("pair policy %+v, want policy_file default", pr)
	}
	if md := migration.Markdown(rep); strings.Contains(md, "enterprise-default") || !strings.Contains(md, "release rule set default") {
		t.Errorf("markdown names the rule set by its inner name or not at all:\n%s", md)
	}
}

// N280: the policy query "none" turns the frozen rule set off; a file stem
// picks that file.
func TestMigrationPolicyQueryNone(t *testing.T) {
	s := serveW6{a: recordedApp(t)}
	ctx := context.Background()
	rep, err := s.MigrationWithPolicy(ctx, "e_pilot_mixed_nc93", schemas.GradeSourceOriginal, "none", migration.Query{})
	if err != nil {
		t.Fatalf("policy none: %v", err)
	}
	if rep.Policy != "" || rep.Pairs[0].Recommendation.Policy != nil {
		t.Errorf("policy none: report policy %q, want none", rep.Policy)
	}
	rep, err = s.MigrationWithPolicy(ctx, "e_smoke_sdoj", schemas.GradeSourceOriginal, "strict", migration.Query{From: "A"})
	if err != nil {
		t.Fatalf("policy strict: %v", err)
	}
	if rep.Policy != "strict" {
		t.Errorf("policy strict: report policy %q, want strict", rep.Policy)
	}
}

// N280: a name that is not a rule set file is refused, paths included.
func TestMigrationPolicyQueryUnknown(t *testing.T) {
	s := serveW6{a: recordedApp(t)}
	for _, name := range []string{"absent", "../policies/default", "policies/default.yaml", "README"} {
		_, err := s.MigrationWithPolicy(context.Background(), "e_pilot_mixed_nc93", schemas.GradeSourceOriginal, name, migration.Query{})
		if !errors.Is(err, handlers.ErrNoRuleSet) {
			t.Errorf("policy %q: err %v, want ErrNoRuleSet", name, err)
		}
	}
}

// N280: analysis and the model switch report choose the rule set through
// one function: with a server default both use it, without one neither
// does, and a frozen rule set wins in both.
func TestAnalyzeAndMigrationUseOnePolicyFunction(t *testing.T) {
	a := recordedApp(t)
	ctx := context.Background()
	m, err := a.store.LoadManifest(ctx, "e_smoke_sdoj")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if f, frozen := a.effectivePolicyFile(m, ""); f != "" || frozen {
		t.Errorf("no frozen, no default: %q %v, want none", f, frozen)
	}
	runs, err := a.store.SealedRuns(ctx, "e_smoke_sdoj", schemas.GradeSourceOriginal)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	strict, err := filepath.Abs(filepath.Join(a.cfg.Root, "policies", "strict.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	a.defaultPolicy = strict
	pr, err := a.releasePolicy(m, nil, runs, "")
	if err != nil || pr == nil || pr.PolicyFile != "strict" {
		t.Fatalf("analysis with a server default: %+v %v, want strict", pr, err)
	}
	rep, err := a.migrationReport(ctx, "e_smoke_sdoj", schemas.GradeSourceOriginal, "", true, migration.Query{From: "A"})
	if err != nil || rep.Policy != "strict" {
		t.Fatalf("model switch with a server default: %v %v, want strict", rep, err)
	}
	pilot, err := a.store.LoadManifest(ctx, "e_pilot_mixed_nc93")
	if err != nil {
		t.Fatal(err)
	}
	if f, frozen := a.effectivePolicyFile(pilot, ""); f != "policies/default.yaml" || !frozen {
		t.Errorf("frozen policy: %q %v, want policies/default.yaml frozen", f, frozen)
	}
	if f, _ := a.effectivePolicyFile(pilot, "none"); f != "" {
		t.Errorf("none: %q, want no rule set", f)
	}
}
