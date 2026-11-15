package agentdojo

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

func fixtureCatalog(t *testing.T) *catalog {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "catalog-banking.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseCatalog(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseCatalog(t *testing.T) {
	c := fixtureCatalog(t)
	if c.PackageVersion != "0.1.35" || len(c.UserTasks) != 4 || !c.UserTasks[2].EmptyPasses || len(c.InjectionTasks) != 2 || len(c.Tools) != 3 {
		t.Fatalf("catalog %+v", c)
	}
	if string(c.UserTasks[0].GroundTruth[1].Args) != `{"recipient": "UK00FIXTURE", "amount": 12.5, "subject": "Bill", "date": "2022-01-01"}` {
		t.Fatalf("ground truth args kept verbatim: %s", c.UserTasks[0].GroundTruth[1].Args)
	}
	for name, doc := range map[string]string{
		"not json":   "[",
		"no version": `{"suite":"banking","benchmark_version":"v1","user_tasks":[{"id":"user_task_0","prompt":"p"}],"tools":[{"name":"t"}]}`,
		"no tasks":   `{"agentdojo_version":"1","suite":"banking","benchmark_version":"v1","tools":[{"name":"t"}]}`,
		"no tools":   `{"agentdojo_version":"1","suite":"banking","benchmark_version":"v1","user_tasks":[{"id":"user_task_0","prompt":"p"}]}`,
		"bad id":     `{"agentdojo_version":"1","suite":"banking","benchmark_version":"v1","user_tasks":[{"id":"task0","prompt":"p"}],"tools":[{"name":"t"}]}`,
		"no prompt":  `{"agentdojo_version":"1","suite":"banking","benchmark_version":"v1","user_tasks":[{"id":"user_task_0","prompt":" "}],"tools":[{"name":"t"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCatalog([]byte(doc)); err == nil {
				t.Fatal("want an error")
			}
		})
	}
}

func TestSelectTasks(t *testing.T) {
	c := fixtureCatalog(t)
	ids := func(ts []userTask) []string {
		var out []string
		for _, x := range ts {
			out = append(out, x.ID)
		}
		return out
	}
	noVector := importers.Skip{ID: "user_task_2", Reason: "its ground truth reads no injection vector, so no attack can reach it"}
	emptyPasses := importers.Skip{ID: "user_task_3", Reason: "upstream's utility check passes with no tool call and no answer, so an empty run would pass"}
	cases := []struct {
		name  string
		ids   []string
		limit int
		want  []string
		skips []importers.Skip
	}{
		{"all usable", nil, 0, []string{"user_task_0", "user_task_1"}, []importers.Skip{emptyPasses, noVector}},
		{"limit", nil, 1, []string{"user_task_0"}, []importers.Skip{{ID: "user_task_1", Reason: "over -limit 1"}, emptyPasses, noVector}},
		{"by upstream and bench id", []string{"banking-user-task-1", "user_task_0"}, 0, []string{"user_task_0", "user_task_1"}, nil},
		{"unknown id", []string{"user_task_7"}, 0, nil, []importers.Skip{{ID: "user_task_7", Reason: "not a user task of banking v1.2.2"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, skips := selectTasks(c, "banking", tc.ids, tc.limit)
			if !reflect.DeepEqual(ids(got), tc.want) || !reflect.DeepEqual(skips, tc.skips) {
				t.Fatalf("got %v %+v, want %v %+v", ids(got), skips, tc.want, tc.skips)
			}
		})
	}
}

func TestSelectProbes(t *testing.T) {
	c := fixtureCatalog(t)
	got, err := selectProbes(c, settings{attacks: []string{"tool_knowledge"}, injectionTasks: []string{"injection_task_1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].id != "agentdojo-tool-knowledge-inj1" || got[0].attack != "tool_knowledge" || got[0].injection.ID != "injection_task_1" {
		t.Fatalf("got %+v", got)
	}
	all, err := selectProbes(c, settings{attacks: []string{"direct", "important_instructions"}})
	if err != nil || len(all) != 4 || all[0].id != "agentdojo-direct-inj0" || all[3].id != "agentdojo-important-instructions-inj1" {
		t.Fatalf("got %+v %v", all, err)
	}
	empty := *c
	empty.InjectionTasks = nil
	if _, err := selectProbes(&empty, settings{attacks: []string{"direct"}}); err == nil || !strings.Contains(err.Error(), "no injection tasks") {
		t.Fatalf("got %v", err)
	}
}

func TestIDs(t *testing.T) {
	if got := taskID("workspace", "user_task_12"); got != "workspace-user-task-12" {
		t.Fatalf("task id %q", got)
	}
	if got := probeID("important_instructions_no_names", "injection_task_3"); got != "agentdojo-important-instructions-no-names-inj3" {
		t.Fatalf("probe id %q", got)
	}
}
