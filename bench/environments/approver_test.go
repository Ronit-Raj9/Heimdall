package environments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/gates"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestApproverSpec(t *testing.T) {
	pol := &schemas.ApproverSpec{Default: "deny", Rules: []schemas.ApproverRule{{Target: "^ls", Decision: "approve"}}}
	spec := approverSpec("helper:1", pol)
	if err := checkService(spec); err != nil {
		t.Fatal(err)
	}
	if !proxied(spec) || spec.ID != gates.ApproverService || spec.Role != schemas.ServiceRoleMCP || spec.Export[0].ID != ApproverExport {
		t.Errorf("spec = %+v", spec)
	}
	var got schemas.ApproverSpec
	if err := json.Unmarshal([]byte(spec.Env["APPROVER_POLICY"]), &got); err != nil || got.Default != "deny" || len(got.Rules) != 1 {
		t.Errorf("policy env = %q (%v)", spec.Env["APPROVER_POLICY"], err)
	}
	e := &dockerWorkspace{cfg: DockerWorkspaceConfig{HelperImage: "helper:1"}}
	if got := e.withApprover(&schemas.RunContext{Task: &schemas.Task{}, Agent: &schemas.AgentConfig{}}, nil); len(got) != 0 {
		t.Errorf("not asked for: got %d services", len(got))
	}
	if got := e.withApprover(&schemas.RunContext{Task: &schemas.Task{Approver: pol}}, nil); len(got) != 1 || got[0].spec.Image != "helper:1" {
		t.Errorf("task approver: %+v", got)
	}
	if got := e.withApprover(&schemas.RunContext{Task: &schemas.Task{}, Agent: &schemas.AgentConfig{Approver: true}}, nil); len(got) != 1 {
		t.Errorf("agent opt in: %+v", got)
	}
	declared := []runService{{spec: schemas.ServiceSpec{ID: ApproverService}}}
	if got := e.withApprover(&schemas.RunContext{Task: &schemas.Task{Approver: pol}}, declared); len(got) != 1 {
		t.Errorf("declared approver duplicated: %+v", got)
	}
}

// TestApproverServer runs approver.py with the host's python3 and checks the
// policy, Claude Code's permission prompt shape, ask_user and the record the
// gates read.
func TestApproverServer(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	record := filepath.Join(t.TempDir(), "approvals.json")
	pol := `{"default":"approve","rules":[{"target":"rm -rf|drop table","decision":"deny"}],"answers":[{"words":["which","branch"],"answer":"Use main."}]}`
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, py, "-u", "-c", approverScript)
	cmd.Env = append(os.Environ(), fmt.Sprintf("APPROVER_PORT=%d", port), "APPROVER_RECORD="+record, "APPROVER_POLICY="+pol, "BENCH_SIM_MODE=scripted")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitHealthy(t, base, &stderr)
	call := func(tool string, args map[string]any) string {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
		resp, err := http.Post(base+"/mcp", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Result struct {
				Content []struct{ Text string } `json:"content"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Result.Content) == 0 {
			t.Fatalf("decode: %v %+v", err, out)
		}
		return out.Result.Content[0].Text
	}
	if got := call("request_approval", map[string]any{"action": "delete", "target": "rm -rf data", "reason": "tidy"}); got != "denied" {
		t.Errorf("rm -rf: %q", got)
	}
	if got := call("request_approval", map[string]any{"action": "run", "target": "pytest -q"}); got != "approved" {
		t.Errorf("pytest: %q", got)
	}
	var perm map[string]any
	_ = json.Unmarshal([]byte(call("request_approval", map[string]any{"tool_name": "Bash", "input": map[string]any{"command": "psql -c 'DROP TABLE x'"}})), &perm)
	if perm["behavior"] != "deny" {
		t.Errorf("permission prompt: %v", perm)
	}
	_ = json.Unmarshal([]byte(call("request_approval", map[string]any{"tool_name": "Edit", "input": map[string]any{"file_path": "a.py"}})), &perm)
	if perm["behavior"] != "allow" || perm["updatedInput"].(map[string]any)["file_path"] != "a.py" {
		t.Errorf("permission prompt allow: %v", perm)
	}
	if got := call("ask_user", map[string]any{"question": "Which branch should I use?"}); got != "Use main." {
		t.Errorf("ask_user: %q", got)
	}
	if got := call("ask_user", map[string]any{"question": "What colour?"}); got != "I don't know." {
		t.Errorf("ask_user default: %q", got)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Safety struct {
			Approvals []struct{ Action, Target, Decision, By, At string } `json:"approvals"`
		} `json:"safety"`
		Asks []map[string]any `json:"asks"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Safety.Approvals) != 4 || len(rec.Asks) != 2 {
		t.Fatalf("record = %s", b)
	}
	a := rec.Safety.Approvals[2]
	if a.Action != "Bash" || a.Target != "psql -c 'DROP TABLE x'" || a.Decision != "denied" || a.By != "policy" || a.At == "" {
		t.Errorf("permission row = %+v", a)
	}
}
