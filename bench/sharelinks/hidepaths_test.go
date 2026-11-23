package sharelinks

import (
	"encoding/json"
	"strings"
	"testing"
)

// A realistic run and trace answer (tool calls with arguments, evidence
// paths, a worker error naming the org's folder) keeps no worker or server
// path, no virtual key, no person and no platform id after RedactJSON and
// HideServerText, and the trace stays renderable: every step keeps its
// kind, tool name and the arguments that are not paths (fix wave 3, N293
// review).
func TestSharedRunPayloadWithToolArguments(t *testing.T) {
	const root = "/data/heimdall/runs"
	run := `{"run_id":"r_1","org_id":"org_01m83tsbn6g7dnf0mtv4hpy6k0","started_by":"usr_01m83tsbn6g7dnf0mtv4hpy6k1",
	"started_by_name":"Ada","worker":"shared-runner-org_01m83tsbn6g7dnf0mtv4hpy6k0","host":"box-7.internal",
	"evidence":{"stdout_path":"/data/heimdall/runs/orgs/org_01m83tsbn6g7dnf0mtv4hpy6k0/r_1/agent.stream.jsonl","virtual_key_id":"vk_9f2",
	"trace_path":"/var/lib/heimdall-runner/work/r_1/trace.json"},
	"error":"read release policy /data/heimdall/runs/projects/org_01m83tsbn6g7dnf0mtv4hpy6k0/policies/default.yaml: open: no such file",
	"reviewer":{"email":"ada@example.com","by":"usr_01m83tsbn6g7dnf0mtv4hpy6k1"},
	"steps":[
	 {"kind":"tool_call","tool":"read_file","args":{"file_path":"/workspace/app/main.py","limit":40}},
	 {"kind":"tool_call","tool":"bash","arguments":"{\"command\":\"pytest -q tests/test_add.py\"}"},
	 {"kind":"tool_result","tool":"bash","output":"1 passed in 0.02s"}]}`
	out, err := json.Marshal(HideServerText(RedactJSON([]byte(run), false), root, ""))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, leak := range []string{"/data/heimdall", "/var/lib/heimdall-runner", "org_01m83", "usr_01m83", "vk_9f2", "ada@example.com", "box-7"} {
		if strings.Contains(s, leak) {
			t.Errorf("shared run keeps %q: %s", leak, s)
		}
	}
	var back struct {
		StartedByName string `json:"started_by_name"`
		Error         string `json:"error"`
		Steps         []struct {
			Kind, Tool string
			Args       map[string]any `json:"args"`
			Arguments  string         `json:"arguments"`
			Output     string         `json:"output"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.StartedByName != "Ada" || !strings.Contains(back.Error, HiddenPath+": open: no such file") {
		t.Fatalf("display name or error text lost: %s", s)
	}
	if len(back.Steps) != 3 || back.Steps[0].Tool != "read_file" || back.Steps[0].Args["limit"] != float64(40) ||
		back.Steps[1].Arguments != `{"command":"pytest -q tests/test_add.py"}` || back.Steps[2].Output != "1 passed in 0.02s" {
		t.Fatalf("trace steps changed shape: %s", s)
	}
}

func TestHideServerTextRootBoundary(t *testing.T) {
	got := HideServerString("a /data/runs/x b /data/runs2/y c /data/runs", "/data/runs")
	if got != "a "+HiddenPath+" b /data/runs2/y c "+HiddenPath {
		t.Fatalf("got %q", got)
	}
	if HideServerString("/etc/passwd", "/") != "/etc/passwd" {
		t.Fatal("the root folder / must never hide every path")
	}
}
