package bifrost

import (
	"encoding/json"
	"testing"
)

func TestToolCallRefHints(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: gatekeeper/limiter.py\n@@\n-a\n+b\n*** Add File: tests/test_new.py\n+x\n*** End Patch"
	pj, _ := json.Marshal(patch)
	heredoc, _ := json.Marshal(map[string]any{"cmd": "apply_patch <<'PATCH'\n" + patch + "\nPATCH", "workdir": "/workspace"})
	tests := []struct {
		name, tool, input  string
		target, cmd, paths string
	}{
		{name: "codex exec_command", tool: "exec_command", input: `{"cmd":"pytest -q","workdir":"/workspace"}`, cmd: "pytest -q"},
		{name: "codex shell argv", tool: "shell", input: `{"command":["bash","-lc","ls -la src"],"workdir":"/workspace"}`, cmd: "ls -la src"},
		{name: "local_shell argv", tool: "local_shell", input: `{"type":"exec","command":["python","-m","pytest"]}`, cmd: "python -m pytest"},
		{name: "claude code bash", tool: "Bash", input: `{"command":"pytest -q"}`, target: "pytest -q"},
		{name: "claude code edit", tool: "Edit", input: `{"file_path":"/workspace/a.py","old_string":"a","new_string":"b"}`, target: "/workspace/a.py"},
		{name: "opencode edit", tool: "edit", input: `{"filePath":"/workspace/a.py","oldString":"a","newString":"b"}`, paths: "/workspace/a.py"},
		{name: "opencode bash", tool: "bash", input: `{"command":"go test ./...","description":"run tests"}`, target: "go test ./..."},
		{name: "codex apply_patch freeform", tool: "apply_patch", input: string(pj), paths: "gatekeeper/limiter.py\ntests/test_new.py"},
		{name: "codex apply_patch heredoc", tool: "exec_command", input: string(heredoc), cmd: "apply_patch <<'PATCH'\n" + patch + "\nPATCH", paths: "gatekeeper/limiter.py\ntests/test_new.py"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := toolCallRef("id", tt.tool, json.RawMessage(tt.input))
			if ref.Target != tt.target || ref.Command != tt.cmd || ref.Paths != tt.paths {
				t.Fatalf("target %q command %q paths %q, want %q %q %q", ref.Target, ref.Command, ref.Paths, tt.target, tt.cmd, tt.paths)
			}
		})
	}
}

func TestToolInputHintsCaps(t *testing.T) {
	long := make([]byte, hintCommandMax+100)
	for i := range long {
		long[i] = 'x'
	}
	cmd, _ := toolInputHints(map[string]any{"cmd": string(long)}, "")
	if len(cmd) != hintCommandMax {
		t.Fatalf("command not capped: %d bytes", len(cmd))
	}
	cmd, _ = toolInputHints(map[string]any{"cmd": "x", "command": []any{"a", 1}}, "")
	if cmd != "x" {
		t.Fatalf("a non-string argv replaced the cmd: %q", cmd)
	}
}
