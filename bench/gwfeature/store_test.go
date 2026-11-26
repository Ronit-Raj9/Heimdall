package gwfeature

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

var computeCalls int

func init() {
	Register(Feature{ID: "test_feature_w4p1", Name: "Test feature", Compute: func(ctx context.Context, in Input) Effect {
		computeCalls++
		p := PairedTasks(in.Runs, in.Test.Control, in.Test.Variants[0], SolveValue)
		return Effect{State: StateMeasured, Headline: "measured", Tasks: p.Tasks, Variant: in.Test.Variants[0]}
	}})
}

func featureSource(t *testing.T, feature string, test bool) *fakeSource {
	src := newFakeSource()
	m := &schemas.Manifest{ExpID: "e1", Arms: map[string]schemas.ArmManifest{"A": {Label: "today"}, "B": {Label: "changed"}}}
	if test {
		m.Experiment.GatewayTest = &schemas.GatewayTest{Feature: feature, Control: "A", Variants: []string{"B"}}
	}
	src.manifests["e1"] = m
	src.addRun(t, "e1", "r1", "t1", "A", "clean", 0, 1, true, 1)
	src.addRun(t, "e1", "r2", "t1", "B", "clean", 0, 1, true, 1)
	return src
}

func TestComputeCachesByFingerprint(t *testing.T) {
	root := t.TempDir()
	src := featureSource(t, "test_feature_w4p1", true)
	computeCalls = 0
	e, err := Compute(context.Background(), src, root, "e1", false)
	if err != nil {
		t.Fatal(err)
	}
	if computeCalls != 1 || e.State != StateMeasured || e.Feature != "test_feature_w4p1" || e.ExpID != "e1" || e.Control != "A" || e.Fingerprint == "" || e.ComputedAt.IsZero() {
		t.Fatalf("first compute %d: %+v", computeCalls, e)
	}
	if e.Source.Kind != "experiment" || e.Source.ExpID != "e1" || e.Source.Runs != 2 {
		t.Fatalf("source: %+v", e.Source)
	}
	if _, ok, _ := Load(root, "e1"); !ok {
		t.Fatal("effect not saved")
	}
	if _, err := Compute(context.Background(), src, root, "e1", false); err != nil || computeCalls != 1 {
		t.Fatalf("second compute called the feature: %d %v", computeCalls, err)
	}
	if _, err := Compute(context.Background(), src, root, "e1", true); err != nil || computeCalls != 2 {
		t.Fatalf("refresh did not call the feature: %d %v", computeCalls, err)
	}
	src.addRun(t, "e1", "r3", "t2", "A", "clean", 0, 1, true, 1)
	if _, err := Compute(context.Background(), src, root, "e1", false); err != nil || computeCalls != 3 {
		t.Fatalf("a new run did not recompute: %d %v", computeCalls, err)
	}
	list, err := List(root)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
}

func TestComputeUnknownFeature(t *testing.T) {
	e, err := Compute(context.Background(), featureSource(t, "no_such_feature", true), t.TempDir(), "e1", false)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != StateUnknown || e.Reason != "this Heimdall does not know the feature no_such_feature" {
		t.Fatalf("%+v", e)
	}
}

func TestComputeArmNotInManifest(t *testing.T) {
	src := featureSource(t, "test_feature_w4p1", true)
	src.manifests["e1"].Experiment.GatewayTest.Variants = []string{"B", "C"}
	e, err := Compute(context.Background(), src, t.TempDir(), "e1", false)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != StateUnknown || e.Reason != "setup C is not in this comparison" {
		t.Fatalf("%+v", e)
	}
}

func TestComputeNotFeatureTest(t *testing.T) {
	_, err := Compute(context.Background(), featureSource(t, "", false), t.TempDir(), "e1", false)
	if !errors.Is(err, ErrNotFeatureTest) {
		t.Fatalf("err = %v", err)
	}
}

func TestComputeNoRunsNotSaved(t *testing.T) {
	root := t.TempDir()
	src := featureSource(t, "test_feature_w4p1", true)
	src.runs["e1"] = nil
	e, err := Compute(context.Background(), src, root, "e1", false)
	if err != nil || e.State != StateUnknown || e.Reason != "no finished runs yet" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, ok, _ := Load(root, "e1"); ok {
		t.Fatal("an effect without runs was saved")
	}
}

func TestSourceConfigRoundTrip(t *testing.T) {
	root := t.TempDir()
	if c, err := LoadSource(root); c != nil || err != nil {
		t.Fatalf("absent: %v %v", c, err)
	}
	if err := SaveSource(root, SourceConfig{GatewayURL: "https://bf.example.com", CredentialSecret: "BF_ADMIN"}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadSource(root)
	if err != nil || c == nil || c.GatewayURL != "https://bf.example.com" || c.CredentialSecret != "BF_ADMIN" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := RemoveSource(root); err != nil {
		t.Fatal(err)
	}
	if c, _ := LoadSource(root); c != nil {
		t.Fatal("not removed")
	}
	if err := RemoveSource(root); err != nil {
		t.Fatal("removing twice must not fail")
	}
}

func TestRuleSettingBody(t *testing.T) {
	s := RuleSetting("Routing rule: x", "note", RoutingRule{Name: "r"})
	if s.Kind != "routing_rule" || !strings.Contains(s.Body, "\n  \"name\": \"r\"") || s.Note != "note" {
		t.Fatalf("%+v", s)
	}
}
