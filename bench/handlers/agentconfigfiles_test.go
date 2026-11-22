package handlers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestAgentConfigListsFiles checks that GET /agents/{id}/config lists the
// container paths of the agent's files map, sorted, so the web can offer them
// as the files an improvement run may change; a PUT that sends them back
// does not change the agent (N128).
func TestAgentConfigListsFiles(t *testing.T) {
	r, _ := builderRouter(t)
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "plain-loop", ID: "loop"}); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	code, body := abCall(r, "GET", APIPrefix+"/agents/loop/config", nil)
	if code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	var doc AgentConfigDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	withFiles := strings.TrimRight(doc.YAML, "\n") + "\nfiles:\n  /home/agent/skills/: \"\"\n  /home/agent/NOTES.md: \"be careful\"\n  /home/agent/AGENTS.md: \"hello\"\n"
	if code, body := abCall(r, "PUT", APIPrefix+"/agents/loop/config", AgentConfigPut{YAML: withFiles}); code != 200 {
		t.Fatalf("put yaml: %d %s", code, body)
	}
	code, body = abCall(r, "GET", APIPrefix+"/agents/loop/config", nil)
	if code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	var raw struct {
		YAML   string `json:"yaml"`
		Fields struct {
			Files []string `json:"files"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"/home/agent/AGENTS.md", "/home/agent/NOTES.md", "/home/agent/skills/"}
	if !reflect.DeepEqual(raw.Fields.Files, want) {
		t.Fatalf("fields.files = %v, want %v", raw.Fields.Files, want)
	}
	// The list is read only: fields sent back with another list leave the file alone.
	var full AgentConfigDoc
	if err := json.Unmarshal(body, &full); err != nil {
		t.Fatal(err)
	}
	f := full.Fields
	f.WallS = 901
	put, err := json.Marshal(map[string]any{"fields": withFilesField(t, f, []string{"/elsewhere.md"})})
	if err != nil {
		t.Fatal(err)
	}
	code, body = abCall(r, "PUT", APIPrefix+"/agents/loop/config", json.RawMessage(put))
	if code != 200 {
		t.Fatalf("put fields: %d %s", code, body)
	}
	var after AgentConfigDoc
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(after.YAML, "/elsewhere.md") || !strings.Contains(after.YAML, "/home/agent/NOTES.md") {
		t.Fatalf("PUT changed the files map:\n%s", after.YAML)
	}
	// An agent with no files map sends no files key.
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "plain-loop", ID: "bare"}); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if _, body := abCall(r, "GET", APIPrefix+"/agents/bare/config", nil); strings.Contains(fieldsJSON(t, body), `"files"`) {
		t.Errorf("an agent without files lists files: %s", fieldsJSON(t, body))
	}
}

// withFilesField is f as a JSON object with "files" set to files.
func withFilesField(t *testing.T, f AgentConfigFields, files []string) map[string]any {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["files"] = files
	return m
}

// fieldsJSON is the "fields" object of a config answer.
func fieldsJSON(t *testing.T, body []byte) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return string(m["fields"])
}
