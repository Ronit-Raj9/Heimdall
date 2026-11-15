package traceimport

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReplayServiceID is the service id of a replaying draft's tool server.
const ReplayServiceID = "tools"

// ObservedMutant is the mutant control a draft from a failed session gets:
// the recorded calls replayed as they were.
const ObservedMutant = "controls/mutant-observed.sh"

// replayServer and replayDockerfile are the replay tool server a tool-use or
// mcp draft runs in place of the copied service: standard library Python
// that answers each call with a recorded result and records the agent's
// calls in calls.json for the verifier.
var (
	//go:embed replay/server.py
	replayServer []byte
	//go:embed replay/Dockerfile
	replayDockerfile []byte
)

// tools writes the tool parts of a tool-use or mcp draft: the observed calls
// fixture, the replay server when the copied service is seeded through
// files: (and Replay is set), the reference script and the checks.
func (w *draftWriter) tools(shape draftShape, opts DraftOptions, reqs []string) error {
	s, res := w.s, w.res
	observed, err := observedTools(s, opts.Source)
	if err != nil {
		return err
	}
	w.put(ObservedToolsFile, observed, false)
	svc := serviceID(shape, res.Template)
	seeds, seeded := serviceFiles(shape, svc)
	switch {
	case opts.Replay && seeded:
		res.Replayed = true
		w.putMode("replay/server.py", replayServer, false, 0o755)
		w.put("replay/Dockerfile", replayDockerfile, false)
		for _, rel := range seeds {
			if rel != ObservedToolsFile && !usedByOther(shape, svc, rel) {
				w.remove(rel)
			}
		}
		svc = ReplayServiceID
		if res.Template == KindToolUse && len(shape.Sessions) == 0 {
			w.put(shape.Instruction, instructionDraft(reqs[0], len(reqs), replayHTTPHint), true)
		}
	case opts.Replay:
		res.Warnings = append(res.Warnings, fmt.Sprintf("service %s is not seeded through files:, so it is kept; %s is a fixture only", svc, ObservedToolsFile))
	}
	if strings.HasSuffix(shape.Controls.Reference, ".sh") {
		w.putMode(shape.Controls.Reference, referenceScript(s, res.Template, svc, res.Replayed), true, 0o755)
		if s.Outcome.Status == OutcomeError && len(s.ToolCalls) > 0 && (res.Replayed || res.Template == KindMCP) {
			w.putMode(ObservedMutant, mutantScript(s, res.Template, svc), false, 0o755)
			res.Mutants = append(res.Mutants, ObservedMutant)
		}
	}
	checks := path.Join(shape.Verifier.Tests, "checks.py")
	old, _ := os.ReadFile(filepath.Join(res.Dir, filepath.FromSlash(checks)))
	w.put(checks, checksDraft(old, s, res.Replayed), true)
	return w.err
}

// replayHTTPHint tells the agent of a replaying tool-use draft where the
// tools are; the replay server's plain HTTP routes are its own.
const replayHTTPHint = "The tools are at http://toolproxy:9200/s/" + ReplayServiceID + "/ (a GET on that address lists them;\n" +
	"call one with a POST to /tools/<name> and its arguments as a JSON object)."

// serviceFiles returns the task files the service seeds, and whether it
// seeds any: a template that seeds its tool service supports a replay.
func serviceFiles(shape draftShape, id string) ([]string, bool) {
	for _, sv := range shape.Services {
		if sv.ID != id || len(sv.Files) == 0 {
			continue
		}
		out := make([]string, 0, len(sv.Files))
		for _, rel := range sv.Files {
			out = append(out, rel)
		}
		return sortedCopy(out), true
	}
	return nil, false
}

func usedByOther(shape draftShape, id, rel string) bool {
	for _, sv := range shape.Services {
		if sv.ID == id {
			continue
		}
		for _, r := range sv.Files {
			if path.Clean(r) == path.Clean(rel) {
				return true
			}
		}
	}
	return false
}

// remove deletes a copied file the draft no longer uses, and its folder when
// that is left empty. A path outside the task folder is refused.
func (w *draftWriter) remove(rel string) {
	clean := path.Clean(filepath.ToSlash(rel))
	if w.err != nil || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return
	}
	p := filepath.Join(w.dir, filepath.FromSlash(clean))
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		w.err = err
		return
	}
	if dir := filepath.Dir(p); dir != w.dir {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			if err := os.Remove(dir); err != nil {
				w.err = err
			}
		}
	}
}

// replayTag names the replay server's image by the hash of its source, so a
// newer bench never reuses an older build.
func replayTag() string {
	sum := sha256.Sum256(append(append([]byte(nil), replayServer...), replayDockerfile...))
	return "bench/trace-replay:" + hex.EncodeToString(sum[:])[:12]
}

// replaceService swaps the services: entry with id for the replay server.
func replaceService(m *yaml.Node, id, tmpl string) error {
	services := mapValue(m, "services")
	if services == nil || services.Kind != yaml.SequenceNode {
		return errors.New("no services list to put the replay server in")
	}
	role := "tool"
	if tmpl == KindMCP {
		role = "mcp"
	}
	src := "id: " + ReplayServiceID + "\nrole: " + role + "\nbuild: {dockerfile: replay/Dockerfile, tag: " + replayTag() + "}\nport: 8000\n"
	if role == "mcp" {
		src += "mcp: {path: /mcp}\n"
	}
	src += "files:\n  /seed/tools.observed.json: " + ObservedToolsFile + "\n" +
		"ready:\n  argv: [\"python3\", \"-I\", \"/srv/server.py\", \"ready\"]\n  timeout_s: 30\n" +
		"export:\n  - {id: calls.json, path: /state/calls.json}\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return err
	}
	node := doc.Content[0]
	node.HeadComment = "Replays the recorded tool results of the traced session (" + ObservedToolsFile + ")\n" +
		"and records every call the agent makes in calls.json. Built from replay/ in\n" +
		"this task folder. Replace it with your own tool service to test live tools."
	for i, item := range services.Content {
		if v := mapValue(item, "id"); v != nil && v.Value == id {
			services.Content[i] = node
			return nil
		}
	}
	return fmt.Errorf("no service %q to replace", id)
}
