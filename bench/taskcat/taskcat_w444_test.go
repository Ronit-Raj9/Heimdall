package taskcat

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func prov(by string) *schemas.Task {
	return &schemas.Task{ID: "t", Provenance: &schemas.TaskProvenance{CreatedBy: by}}
}

func TestRulesOrder(t *testing.T) {
	qa := prov("task from-trace")
	qa.Verifier.Answer = "verifier/answer.yaml"
	tooly := prov("task from-trace")
	tooly.Tags = []string{"tool-use"}
	plain := &schemas.Task{ID: "p"}
	kinded := &schemas.Task{ID: "k", Kind: schemas.TaskRefactor}
	cases := []struct {
		name string
		in   Inputs
		want string
		sure bool
		ok   bool
	}{
		{"api", Inputs{Task: prov("api policy generator"), CommitSubject: "fix: x"}, Policy, true, true},
		{"policy", Inputs{Task: prov("policy generator (m)")}, Policy, true, true},
		{"qa read tools", Inputs{Task: qa, SessionTools: []string{"Read", "grep"}}, Navigation, true, true},
		{"qa write tool", Inputs{Task: qa, SessionTools: []string{"Read", "Edit"}}, Conversation, true, true},
		{"tool use", Inputs{Task: tooly}, ToolUse, true, true},
		{"tests only", Inputs{Task: plain, ChangedFiles: []string{"pkg/a_test.go", "tests/test_b.py"}, CommitSubject: "fix: y"}, TestRepair, true, true},
		{"manifest only", Inputs{Task: plain, ChangedFiles: []string{"go.mod", "go.sum"}}, DependencyUpdate, true, true},
		{"fix subject", Inputs{Task: plain, ChangedFiles: []string{"a.go"}, CommitSubject: "fix: crash"}, BugFix, true, true},
		{"fixes ref", Inputs{Task: plain, CommitSubject: "Handle empty input, fixes #12"}, BugFix, true, true},
		{"feat", Inputs{Task: plain, CommitSubject: "feat: new flag"}, Feature, true, true},
		{"add", Inputs{Task: plain, CommitSubject: "Add a cache"}, Feature, true, true},
		{"refactor", Inputs{Task: plain, CommitSubject: "refactor: split"}, Refactor, true, true},
		{"perf", Inputs{Task: plain, CommitSubject: "perf: faster"}, Performance, true, true},
		{"docs", Inputs{Task: plain, CommitSubject: "docs: readme"}, Docs, true, true},
		{"label bug", Inputs{Task: plain, IssueLabels: []string{"Bug"}}, BugFix, true, true},
		{"label enhancement", Inputs{Task: plain, IssueLabels: []string{"enhancement"}}, Feature, true, true},
		{"kind", Inputs{Task: kinded}, Refactor, false, true},
		{"nothing", Inputs{Task: plain}, "", false, false},
	}
	for _, c := range cases {
		l, ok := Rules(c.in)
		if ok != c.ok || l.Category != c.want || l.Sure != c.sure || (ok && l.Source != SourceRule) {
			t.Errorf("%s: got %+v %v, want %s sure=%v ok=%v", c.name, l, ok, c.want, c.sure, c.ok)
		}
	}
}

func TestPersonLabelKept(t *testing.T) {
	root := t.TempDir()
	if m, err := Read(root); err != nil || len(m) != 0 {
		t.Fatalf("missing sidecar = %v %v", m, err)
	}
	if err := Set(root, "t1", Label{Category: Docs, Source: SourcePerson, Why: "Ana"}); err != nil {
		t.Fatal(err)
	}
	if err := Set(root, "t1", Label{Category: BugFix, Source: SourceRule, Sure: true}); err != nil {
		t.Fatal(err)
	}
	if err := Set(root, "t2", Label{Category: BugFix, Source: SourceRule, Sure: true}); err != nil {
		t.Fatal(err)
	}
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if m["t1"].Category != Docs || m["t1"].Source != SourcePerson || m["t2"].Category != BugFix || m["t2"].At.IsZero() {
		t.Fatalf("labels = %+v", m)
	}
	if err := Set(root, "t1", Label{Category: Refactor, Source: SourcePerson}); err != nil || mustRead(t, root)["t1"].Category != Refactor {
		t.Fatalf("person label not replaced by a person: %v", err)
	}
	if err := Set(root, "t1", Label{Category: "nonsense", Source: SourcePerson}); err == nil {
		t.Fatal("unknown category accepted")
	}
}

func mustRead(t *testing.T, root string) map[string]Label {
	t.Helper()
	m, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
