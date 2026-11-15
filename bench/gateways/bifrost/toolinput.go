package bifrost

import (
	"path"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/utils"
)

// Caps on the matching hints a tool call reference carries, so a long
// heredoc or a patch with many files cannot bloat the evidence.
const (
	hintCommandMax = 2048
	hintPathsMax   = 32
	hintPathMax    = 1024
)

// shellNames are the shells whose "-c" script is the command a wrapper runs.
var shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

// toolInputHints reads what the timeline needs to link a requested tool call
// to the command or file change that executed it (docs/steps-and-evidence.md)
// and that target does not already hold: the shell command of a "cmd" field
// (Codex exec_command) or an argv array ("command" of Codex shell and
// local_shell), and the file paths of a "filePath" field (OpenCode edit and
// write), a "notebook_path" field, or a patch (Codex apply_patch, as a
// freeform input, a patch field or a heredoc in a command), one per line
// (a path holding a newline is dropped). Both are redacted
// like every other agent-influenced string. It is pure.
func toolInputHints(v any, target string) (string, string) {
	var command string
	var paths []string
	addPatch := func(s string) {
		if strings.Contains(s, "*** Begin Patch") {
			paths = append(paths, patchPaths(s)...)
		}
	}
	switch in := v.(type) {
	case string:
		addPatch(in)
	case map[string]any:
		if s, ok := in["cmd"].(string); ok && s != "" {
			command = s
		}
		if argv, ok := stringList(in["command"]); ok && len(argv) > 0 {
			command = shellScript(argv)
		}
		for _, f := range []string{"filePath", "notebook_path"} {
			if s, ok := in[f].(string); ok && s != "" {
				paths = append(paths, s)
			}
		}
		for _, f := range []string{"input", "patch", "patchText", "cmd", "command"} {
			if s, ok := in[f].(string); ok {
				addPatch(s)
			}
		}
		addPatch(command)
	}
	if command == target {
		command = ""
	}
	out := make([]string, 0, len(paths))
	seen := map[string]bool{target: true}
	for _, p := range paths {
		p = clipHint(strings.TrimSpace(p), hintPathMax)
		if p == "" || strings.Contains(p, "\n") || seen[p] || len(out) == hintPathsMax {
			continue
		}
		seen[p] = true
		out = append(out, utils.RedactSecrets(p))
	}
	if command != "" {
		command = utils.RedactSecrets(clipHint(command, hintCommandMax))
	}
	return command, strings.Join(out, "\n")
}

// stringList returns v as a list of strings when every element is one.
func stringList(v any) ([]string, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// shellScript returns the script of a shell wrapper argv (bash -lc "<script>")
// and the argv joined with spaces otherwise.
func shellScript(argv []string) string {
	if len(argv) >= 3 && shellNames[path.Base(argv[0])] && strings.HasPrefix(argv[1], "-") && !strings.HasPrefix(argv[1], "--") && strings.HasSuffix(argv[1], "c") {
		return argv[len(argv)-1]
	}
	return strings.Join(argv, " ")
}

// patchPaths returns the files a patch in the apply_patch format touches,
// in order: added, updated, deleted and moved-to files.
func patchPaths(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
			if p, ok := strings.CutPrefix(line, prefix); ok && strings.TrimSpace(p) != "" {
				out = append(out, strings.TrimSpace(p))
			}
		}
	}
	return out
}

// clipHint cuts s to at most n bytes on a rune boundary.
func clipHint(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
