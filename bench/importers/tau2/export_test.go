package tau2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

// writeExport writes a tiny `tau2svc.py export` file.
func writeExport(t *testing.T, dir, language, domain string, tasks string) {
	t.Helper()
	doc := `{"format":"tau2-bench-export/1","domain":"` + domain + `","language":"` + language + `","subset":"fixture",` +
		`"policy":"POLICY ` + language + `","mutating_tools":["cancel_booking","call_discoverable_agent_tool"],"tasks":` + tasks + `}`
	if err := os.WriteFile(filepath.Join(dir, language+"-"+domain+".json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportLanguagePack(t *testing.T) {
	dir := t.TempDir()
	writeExport(t, dir, "es", "airline", `[
 {"id":"0_es_identity","user_scenario":{"instructions":{"reason_for_call":"Cancelar","known_info":"x","unknown_info":"","task_instructions":"."}},
  "evaluation_criteria":{"actions":[{"action_id":"0","requestor":"assistant","name":"cancel_booking","arguments":{"booking_id":"B1"}}],"reward_basis":["DB"]}},
 {"id":"1_es_identity","user_scenario":{"instructions":"x"},
  "evaluation_criteria":{"actions":[{"action_id":"1","requestor":"user","name":"toggle","arguments":{}}],"reward_basis":["DB"]}}]`)
	res, root := runImport(t, map[string]string{SetDomains: "airline", SetLanguage: "es", SetExportDir: dir, SetUserTools: UserToolsDraft}, nil, 0)
	if want := []string{"es-airline-0_es_identity", "es-airline-1_es_identity"}; !slices.Equal(res.Tasks, want) {
		t.Fatalf("tasks = %v, want %v", res.Tasks, want)
	}
	task := filepath.Join(importers.SuiteDir(root, "tau2-fixture"), "tasks", "es-airline-0_es_identity")
	b, _ := os.ReadFile(filepath.Join(task, "task.yaml"))
	if !strings.Contains(string(b), "lang-es") {
		t.Fatalf("task.yaml has no language tag:\n%s", b)
	}
	ins, _ := os.ReadFile(filepath.Join(task, "instruction.md"))
	if !strings.Contains(string(ins), "POLICY es") {
		t.Fatal("instruction lacks the exported policy")
	}
	var sc map[string]any
	sb, _ := os.ReadFile(filepath.Join(task, "scenario", "scenario.json"))
	if err := json.Unmarshal(sb, &sc); err != nil || sc["language"] != "es" {
		t.Fatalf("scenario language = %v (%v)", sc["language"], err)
	}
	d, _ := os.ReadFile(filepath.Join(importers.SuiteDir(root, "tau2-fixture"), "tasks", "es-airline-1_es_identity", "task.yaml"))
	if !strings.Contains(string(d), draftUserToolsTag) {
		t.Fatal("user-tool task is not tagged draft")
	}
}

func TestImportBankingKnowledge(t *testing.T) {
	dir := t.TempDir()
	writeExport(t, dir, "en", "banking_knowledge", `[
 {"id":"task_001","user_scenario":{"instructions":"I want a card."},
  "evaluation_criteria":{"actions":[{"action_id":"a","requestor":"assistant","name":"call_discoverable_agent_tool","arguments":{"agent_tool_name":"open_account","arguments":"{}"}}],"reward_basis":["DB"]}}]`)
	res, root := runImport(t, map[string]string{SetDomains: "banking_knowledge", SetExportDir: dir}, nil, 0)
	if want := []string{"banking_knowledge-task_001"}; !slices.Equal(res.Tasks, want) {
		t.Fatalf("tasks = %v, want %v (skipped %+v)", res.Tasks, want, res.Skipped)
	}
	var g map[string]any
	gb, _ := os.ReadFile(filepath.Join(importers.SuiteDir(root, "tau2-fixture"), "tasks", res.Tasks[0], "verifier", "tests", "grading.json"))
	if err := json.Unmarshal(gb, &g); err != nil || g["retrieval"] != "bm25" || len(g["read_log_allowlist"].([]any)) != 1 {
		t.Fatalf("grading = %v (%v)", g, err)
	}
}
