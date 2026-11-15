package agentdojo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlShell(t *testing.T) {
	c := fixtureCatalog(t)
	s, err := controlShell(referencePurpose, "banking", c.UserTasks[1], c.UserTasks[1].GroundTruth, c.UserTasks[1].GroundTruthOutput)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "#!/bin/sh\n") || !strings.Contains(s, "<<'PY'\n") || !strings.Contains(s, "BENCH_MCP_SERVERS") {
		t.Fatalf("script:\n%s", s)
	}
	_, after, ok := strings.Cut(s, "<<'JSON'\n")
	if !ok {
		t.Fatal("no plan here-document")
	}
	var plan struct {
		Server string         `json:"server"`
		Calls  []functionCall `json:"calls"`
		Answer string         `json:"answer"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(after, "\nJSON\n")), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Server != serviceID || len(plan.Calls) != 1 || plan.Calls[0].Function != "get_balance" || plan.Answer != "Your balance is 100.0." {
		t.Fatalf("plan %+v", plan)
	}
	empty, err := controlShell(mutantPurpose, "banking", userTask{ID: "user_task_9"}, nil, "")
	if err != nil || !strings.Contains(empty, `"calls":[]`) {
		t.Fatalf("a task without ground truth still gets an empty call list: %v\n%s", err, empty)
	}
}

func TestInstruction(t *testing.T) {
	got := instruction("travel", "", "Book a hotel.")
	if strings.Contains(got, "## Context") || !strings.Contains(got, "## Task\n\nBook a hotel.") || !strings.Contains(got, "answer.txt") {
		t.Fatalf("instruction:\n%s", got)
	}
}

func TestWriteImageTagFollowsContent(t *testing.T) {
	up, err := openUpstream(filepath.Join("testdata", "upstream"), "banking")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	w := &writer{up: up, suiteDir: dir}
	if err := w.writeImage(); err != nil {
		t.Fatal(err)
	}
	first := w.imageTag
	if err := w.writeImage(); err != nil || w.imageTag != first {
		t.Fatalf("same content, new tag: %q then %q (%v)", first, w.imageTag, err)
	}
	root := copyFixture(t)
	if err := os.WriteFile(filepath.Join(root, "src", "agentdojo", "extra.py"), []byte("X = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w.up, err = openUpstream(root, "banking"); err != nil {
		t.Fatal(err)
	}
	if err := w.writeImage(); err != nil || w.imageTag == first {
		t.Fatalf("changed sources kept tag %q (%v)", w.imageTag, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "image", "upstream", "src", "agentdojo", "extra.py")); err != nil {
		t.Fatal(err)
	}
}

func TestUnionAndFlowList(t *testing.T) {
	got := union([]string{"a", "b"}, []string{"b", "c"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("union %v", got)
	}
	if fl := flowList([]string{"a", `b"c`}); fl != `["a", "b\"c"]` {
		t.Fatalf("flow list %s", fl)
	}
}

func TestRequirements(t *testing.T) {
	got := requirements("banking", "v1.2.2", userTask{ID: "user_task_4", Prompt: "Pay a|b"})
	if !strings.Contains(got, "banking user_task_4 (benchmark v1.2.2)") || !strings.Contains(got, `| Pay a\|b |`) || !strings.Contains(got, "attack_succeeded") {
		t.Fatalf("requirements:\n%s", got)
	}
}
